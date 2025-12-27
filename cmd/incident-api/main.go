package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	kafka "github.com/segmentio/kafka-go"

	"github.com/ilya2309548/EventPulse/internal/common"
)

type API struct {
	db     *sql.DB
	ready  bool
	reader *kafka.Reader
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS inbox (
			id SERIAL PRIMARY KEY,
			dedup_key TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS incidents (
			id SERIAL PRIMARY KEY,
			incident_id TEXT NOT NULL UNIQUE,
			alert_fp TEXT,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_incidents_fp ON incidents(alert_fp)`,
		`CREATE TABLE IF NOT EXISTS incident_events (
			id SERIAL PRIMARY KEY,
			incident_id TEXT NOT NULL,
			type TEXT NOT NULL,
			payload TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS actions (
			id SERIAL PRIMARY KEY,
			action_id TEXT NOT NULL UNIQUE,
			incident_id TEXT,
			kind TEXT,
			desired_replicas INTEGER,
			status TEXT,
			error TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (a *API) handleReady(w http.ResponseWriter, _ *http.Request) {
	if a.ready {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not-ready"))
}

func (a *API) listIncidents(w http.ResponseWriter, _ *http.Request) {
	rows, err := a.db.Query(`SELECT incident_id, alert_fp, status, created_at, updated_at FROM incidents ORDER BY id DESC LIMIT 200`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type item struct {
		IncidentID string `json:"incident_id"`
		AlertFP    string `json:"alert_fp"`
		Status     string `json:"status"`
		CreatedAt  string `json:"created_at"`
		UpdatedAt  string `json:"updated_at"`
	}
	var out []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.IncidentID, &it.AlertFP, &it.Status, &it.CreatedAt, &it.UpdatedAt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out = append(out, it)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (a *API) getIncident(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/incidents/")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	var (
		incidentID, alertFP, status, createdAt, updatedAt string
	)
	err := a.db.QueryRow(`SELECT incident_id, alert_fp, status, created_at, updated_at FROM incidents WHERE incident_id=$1`, id).
		Scan(&incidentID, &alertFP, &status, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type Event struct {
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
		CreatedAt string          `json:"created_at"`
	}
	type Action struct {
		ActionID        string `json:"action_id"`
		Kind            string `json:"kind"`
		DesiredReplicas int    `json:"desired_replicas"`
		Status          string `json:"status"`
		Error           string `json:"error"`
		CreatedAt       string `json:"created_at"`
		UpdatedAt       string `json:"updated_at"`
	}
	var events []Event
	erows, err := a.db.Query(`SELECT type, payload, created_at FROM incident_events WHERE incident_id=$1 ORDER BY id`, id)
	if err == nil {
		defer erows.Close()
		for erows.Next() {
			var e Event
			if err := erows.Scan(&e.Type, &e.Payload, &e.CreatedAt); err == nil {
				events = append(events, e)
			}
		}
	}
	var actions []Action
	arows, err := a.db.Query(`SELECT action_id, kind, desired_replicas, status, COALESCE(error,''), created_at, updated_at FROM actions WHERE incident_id=$1 ORDER BY id`, id)
	if err == nil {
		defer arows.Close()
		for arows.Next() {
			var ac Action
			if err := arows.Scan(&ac.ActionID, &ac.Kind, &ac.DesiredReplicas, &ac.Status, &ac.Error, &ac.CreatedAt, &ac.UpdatedAt); err == nil {
				actions = append(actions, ac)
			}
		}
	}
	resp := map[string]any{
		"incident_id": incidentID,
		"alert_fp":    alertFP,
		"status":      status,
		"created_at":  createdAt,
		"updated_at":  updatedAt,
		"events":      events,
		"actions":     actions,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func insertInbox(db *sql.DB, key, now string) error {
	_, err := db.Exec(`INSERT INTO inbox (dedup_key, created_at) VALUES ($1,$2)`, key, now)
	return err
}

func (a *API) appendIncidentEvent(incidentID, typ string, payload []byte, now string) {
	_, _ = a.db.Exec(`INSERT INTO incident_events (incident_id, type, payload, created_at) VALUES ($1,$2,$3,$4)`,
		incidentID, typ, string(payload), now)
}

func (a *API) upsertIncident(alertFP, status, now string) (string, error) {
	// Try to find latest open/mitigating incident for this alert_fp
	var id string
	err := a.db.QueryRow(`SELECT incident_id FROM incidents WHERE alert_fp=$1 AND status IN ('open','mitigating') ORDER BY id DESC LIMIT 1`, alertFP).Scan(&id)
	if err == sql.ErrNoRows {
		id = fmt.Sprintf("inc-%d", time.Now().UnixNano())
		_, err = a.db.Exec(`INSERT INTO incidents (incident_id, alert_fp, status, created_at, updated_at) VALUES ($1,$2,$3,$4,$4)`,
			id, alertFP, status, now)
		if err != nil {
			return "", err
		}
		return id, nil
	}
	if err != nil {
		return "", err
	}
	// Update status
	_, err = a.db.Exec(`UPDATE incidents SET status=$1, updated_at=$2 WHERE incident_id=$3`, status, now, id)
	return id, err
}

func (a *API) setIncidentStatusByFP(alertFP, status, now string) (string, error) {
	var id string
	err := a.db.QueryRow(`SELECT incident_id FROM incidents WHERE alert_fp=$1 ORDER BY id DESC LIMIT 1`, alertFP).Scan(&id)
	if err != nil {
		return "", err
	}
	_, err = a.db.Exec(`UPDATE incidents SET status=$1, updated_at=$2 WHERE incident_id=$3`, status, now, id)
	return id, err
}

func (a *API) processMessage(msg kafka.Message) error {
	var m map[string]any
	if err := json.Unmarshal(msg.Value, &m); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	dedup, _ := m["dedup_key"].(string)
	if dedup == "" {
		dedup = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if err := insertInbox(a.db, dedup, now); err != nil {
		// Best-effort unique detection
		s := strings.ToLower(err.Error())
		if strings.Contains(s, "unique") || strings.Contains(s, "duplicate") {
			return nil
		}
		return err
	}

	typ, _ := m["type"].(string)
	switch typ {
	case "incident.opened":
		alertFP, _ := m["alert_fp"].(string)
		if alertFP == "" {
			// ignore if missing; nothing to correlate
			return nil
		}
		id, err := a.upsertIncident(alertFP, "open", now)
		if err != nil {
			return err
		}
		pjson, _ := json.Marshal(m)
		a.appendIncidentEvent(id, typ, pjson, now)
	case "action.completed":
		actionID, _ := m["action_id"].(string)
		alertFP, _ := m["alert_fp"].(string)
		kind, _ := m["kind"].(string)
		desired := 0
		if v, ok := m["desired_replicas"].(float64); ok {
			desired = int(v)
		}
		// Link to latest incident by alert_fp
		id, err := a.setIncidentStatusByFP(alertFP, "resolved", now)
		if err != nil {
			// If no incident found, just ignore linking
			log.Printf("action.completed: incident not found for fp=%s: %v", alertFP, err)
		} else {
			pjson, _ := json.Marshal(m)
			a.appendIncidentEvent(id, typ, pjson, now)
		}
		// Upsert action
		_, _ = a.db.Exec(`INSERT INTO actions (action_id, incident_id, kind, desired_replicas, status, created_at, updated_at)
			VALUES ($1,$2,$3,$4,'completed',$5,$5)
			ON CONFLICT (action_id) DO UPDATE SET status='completed', updated_at=$5`, actionID, id, kind, desired, now)
	case "action.failed":
		actionID, _ := m["action_id"].(string)
		alertFP, _ := m["alert_fp"].(string)
		errText, _ := m["error"].(string)
		kind, _ := m["kind"].(string)
		desired := 0
		if v, ok := m["desired_replicas"].(float64); ok {
			desired = int(v)
		}
		id, err := a.setIncidentStatusByFP(alertFP, "failed", now)
		if err != nil {
			log.Printf("action.failed: incident not found for fp=%s: %v", alertFP, err)
		} else {
			pjson, _ := json.Marshal(m)
			a.appendIncidentEvent(id, typ, pjson, now)
		}
		_, _ = a.db.Exec(`INSERT INTO actions (action_id, incident_id, kind, desired_replicas, status, error, created_at, updated_at)
			VALUES ($1,$2,$3,$4,'failed',$5,$6,$6)
			ON CONFLICT (action_id) DO UPDATE SET status='failed', error=$5, updated_at=$6`, actionID, id, kind, desired, errText, now)
	default:
		// ignore
		return nil
	}
	return nil
}

func main() {
	common.Init("incident-api")

	dsn := os.Getenv("INCIDENT_DB_DSN")
	if dsn == "" {
		dsn = "postgres://incident:incident@incident-db:5432/incident?sslmode=disable"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	if err := migrate(db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	brokersEnv := strings.TrimSpace(os.Getenv("KAFKA_BROKERS"))
	brokers := []string{"redpanda:9092"}
	if brokersEnv != "" {
		brokers = strings.Split(brokersEnv, ",")
	}
	topicIncident := os.Getenv("KAFKA_TOPIC_INCIDENT_OPENED")
	if topicIncident == "" {
		topicIncident = "incident.opened"
	}
	topicCompleted := os.Getenv("KAFKA_TOPIC_ACTION_COMPLETED")
	if topicCompleted == "" {
		topicCompleted = "action.completed"
	}
	topicFailed := os.Getenv("KAFKA_TOPIC_ACTION_FAILED")
	if topicFailed == "" {
		topicFailed = "action.failed"
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     brokers,
		GroupID:     "incident-api",
		GroupTopics: []string{topicIncident, topicCompleted, topicFailed},
	})

	api := &API{db: db, ready: true, reader: reader}

	http.HandleFunc("/health", api.handleHealth)
	http.HandleFunc("/ready", api.handleReady)
	http.HandleFunc("/incidents", api.listIncidents)
	http.HandleFunc("/incidents/", api.getIncident)

	go func() {
		log.Printf("incident-api listening on :8091")
		if err := http.ListenAndServe(":8091", nil); err != nil {
			log.Fatal(err)
		}
	}()

	log.Printf("incident-api consuming topics: %s, %s, %s", topicIncident, topicCompleted, topicFailed)
	for {
		ctx := context.Background()
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			log.Printf("read error: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}
		if err := api.processMessage(msg); err != nil {
			log.Printf("process error: %v", err)
		}
	}
}
