package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

type Runner struct {
	db            *sql.DB
	ready         bool
	reader        *kafka.Reader
	completedSink *kafka.Writer
	failedSink    *kafka.Writer
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS inbox (
			id SERIAL PRIMARY KEY,
			dedup_key TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS outbox_events (
			id SERIAL PRIMARY KEY,
			type TEXT NOT NULL,
			payload TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS action_exec (
			id SERIAL PRIMARY KEY,
			action_id TEXT NOT NULL UNIQUE,
			kind TEXT NOT NULL,
			desired_replicas INTEGER NOT NULL,
			alert_fp TEXT,
			status TEXT NOT NULL,
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

func (r *Runner) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (r *Runner) handleReady(w http.ResponseWriter, _ *http.Request) {
	if r.ready {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not-ready"))
}

func insertInbox(db *sql.DB, key, now string) error {
	_, err := db.Exec(`INSERT INTO inbox (dedup_key, created_at) VALUES ($1,$2)`, key, now)
	return err
}

func writeOutbox(db *sql.DB, typ, payload, now string) error {
	_, err := db.Exec(`INSERT INTO outbox_events (type, payload, created_at) VALUES ($1,$2,$3)`, typ, payload, now)
	return err
}

func (r *Runner) recordAction(actionID, kind string, desired int, alertFP, status, errText, now string) error {
	// Upsert-like by action_id
	_, err := r.db.Exec(`INSERT INTO action_exec (action_id, kind, desired_replicas, alert_fp, status, error, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7)
		ON CONFLICT (action_id) DO UPDATE SET status=EXCLUDED.status, error=EXCLUDED.error, updated_at=EXCLUDED.updated_at`,
		actionID, kind, desired, alertFP, status, errText, now,
	)
	return err
}

func (r *Runner) publishResult(ctx context.Context, topic string, payload map[string]any) error {
	pjson, _ := json.Marshal(payload)
	now := time.Now().UTC().Format(time.RFC3339)
	if err := writeOutbox(r.db, payload["type"].(string), string(pjson), now); err != nil {
		return err
	}
	var w *kafka.Writer
	switch topic {
	case r.completedSink.Topic:
		w = r.completedSink
	case r.failedSink.Topic:
		w = r.failedSink
	default:
		return errors.New("unknown topic")
	}
	return w.WriteMessages(ctx, kafka.Message{Value: pjson})
}

func (r *Runner) executeScale(kind string, desired int) error {
	// Minimal stub: optionally probe health to simulate convergence
	// In a fuller version, this would call Docker API to add/remove containers with label service=app
	// Here we just wait a short time and check Traefik/app health endpoint.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://traefik/healthz")
		if err == nil && resp.StatusCode == 200 {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("readiness not achieved for kind=%s desired=%d", kind, desired)
}

func (r *Runner) processAction(msg kafka.Message) error {
	var m map[string]any
	if err := json.Unmarshal(msg.Value, &m); err != nil {
		return err
	}
	if m["type"] != "action.requested" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	dedup, _ := m["dedup_key"].(string)
	if dedup == "" {
		// Fallback action id
		dedup = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	// Inbox dedup
	if err := insertInbox(r.db, dedup, now); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return nil
		}
		return err
	}
	kind, _ := m["kind"].(string)
	desired := 1
	if v, ok := m["desired_replicas"].(float64); ok {
		desired = int(v)
	}
	alertFP, _ := m["alert_fp"].(string)

	// Record start
	if err := r.recordAction(dedup, kind, desired, alertFP, "running", "", now); err != nil {
		log.Printf("record action start failed: %v", err)
	}

	// Execute (stub)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.executeScale(kind, desired); err != nil {
		// Failure path
		_ = r.recordAction(dedup, kind, desired, alertFP, "failed", err.Error(), now)
		payload := map[string]any{
			"type":             "action.failed",
			"action_id":        dedup,
			"kind":             kind,
			"desired_replicas": desired,
			"alert_fp":         alertFP,
			"error":            err.Error(),
			"created_at":       now,
			"dedup_key":        dedup + ":failed",
		}
		if perr := r.publishResult(ctx, r.failedSink.Topic, payload); perr != nil {
			log.Printf("publish action.failed error: %v", perr)
		}
		return nil
	}

	// Success path
	_ = r.recordAction(dedup, kind, desired, alertFP, "completed", "", now)
	payload := map[string]any{
		"type":             "action.completed",
		"action_id":        dedup,
		"kind":             kind,
		"desired_replicas": desired,
		"alert_fp":         alertFP,
		"created_at":       now,
		"dedup_key":        dedup + ":completed",
	}
	if perr := r.publishResult(ctx, r.completedSink.Topic, payload); perr != nil {
		log.Printf("publish action.completed error: %v", perr)
	}
	return nil
}

func main() {
	common.Init("action-runner")

	// DB connect
	dsn := os.Getenv("ACTION_DB_DSN")
	if dsn == "" {
		dsn = "postgres://action:action@action-db:5432/action?sslmode=disable"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	if err := migrate(db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// Kafka wiring
	brokersEnv := strings.TrimSpace(os.Getenv("KAFKA_BROKERS"))
	brokers := []string{"redpanda:9092"}
	if brokersEnv != "" {
		brokers = strings.Split(brokersEnv, ",")
	}
	topicIn := os.Getenv("KAFKA_TOPIC_ACTION_REQUESTED")
	if topicIn == "" {
		topicIn = "action.requested"
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
		Brokers: brokers,
		Topic:   topicIn,
		GroupID: "action-runner",
	})
	completedWriter := &kafka.Writer{Addr: kafka.TCP(brokers...), Topic: topicCompleted, Balancer: &kafka.LeastBytes{}}
	failedWriter := &kafka.Writer{Addr: kafka.TCP(brokers...), Topic: topicFailed, Balancer: &kafka.LeastBytes{}}

	r := &Runner{db: db, ready: true, reader: reader, completedSink: completedWriter, failedSink: failedWriter}

	http.HandleFunc("/health", r.handleHealth)
	http.HandleFunc("/ready", r.handleReady)
	go func() {
		log.Printf("action-runner listening on :8092")
		if err := http.ListenAndServe(":8092", nil); err != nil {
			log.Fatal(err)
		}
	}()

	log.Printf("action-runner consuming from %s", topicIn)
	for {
		ctx := context.Background()
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			log.Printf("read error: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}
		if err := r.processAction(msg); err != nil {
			log.Printf("process error: %v", err)
		}
	}
}
