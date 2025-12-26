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

type RuleEngine struct {
	db             *sql.DB
	ready          bool
	alertReader    *kafka.Reader
	incidentWriter *kafka.Writer
	actionWriter   *kafka.Writer
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
		`CREATE TABLE IF NOT EXISTS decisions_log (
			id SERIAL PRIMARY KEY,
			decision TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (re *RuleEngine) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (re *RuleEngine) handleReady(w http.ResponseWriter, r *http.Request) {
	if re.ready {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not-ready"))
}

func insertInbox(db *sql.DB, key string, now string) error {
	_, err := db.Exec(`INSERT INTO inbox (dedup_key, created_at) VALUES ($1,$2)`, key, now)
	return err
}

func writeOutbox(db *sql.DB, typ string, payload string, now string) error {
	_, err := db.Exec(`INSERT INTO outbox_events (type, payload, created_at) VALUES ($1,$2,$3)`, typ, payload, now)
	return err
}

func (re *RuleEngine) processAlert(msg kafka.Message) error {
	var payload map[string]any
	if err := json.Unmarshal(msg.Value, &payload); err != nil {
		return err
	}
	// Expect fields: type, fingerprint, status, labels, annotations, dedup_key
	typ, _ := payload["type"].(string)
	if typ != "alert.raised" {
		return nil // ignore
	}
	fingerprint, _ := payload["fingerprint"].(string)
	status, _ := payload["status"].(string)
	now := time.Now().UTC().Format(time.RFC3339)

	// Inbox dedup: fingerprint + event_type + status at Rule Engine scope
	dedup := fmt.Sprintf("%s:%s:%s", fingerprint, "alert.raised", status)
	if err := insertInbox(re.db, dedup, now); err != nil {
		// unique violation -> skip
		if strings.Contains(err.Error(), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return nil
		}
		return err
	}

	// Decision: for firing -> open incident + request scale to 2; for resolved -> request scale to 1
	var outMsgs []struct {
		topic string
		typ   string
		body  map[string]any
	}

	switch status {
	case "firing":
		outMsgs = append(outMsgs,
			struct {
				topic, typ string
				body       map[string]any
			}{
				topic: re.incidentWriter.Topic,
				typ:   "incident.opened",
				body: map[string]any{
					"type":       "incident.opened",
					"alert_fp":   fingerprint,
					"created_at": now,
					"dedup_key":  fmt.Sprintf("%s:%s", fingerprint, "incident.opened"),
				},
			},
			struct {
				topic, typ string
				body       map[string]any
			}{
				topic: re.actionWriter.Topic,
				typ:   "action.requested",
				body: map[string]any{
					"type":             "action.requested",
					"kind":             "scale_docker",
					"desired_replicas": 2,
					"created_at":       now,
					"dedup_key":        fmt.Sprintf("%s:%s:%d", fingerprint, "action.requested", 2),
				},
			},
		)
	case "resolved":
		outMsgs = append(outMsgs,
			struct {
				topic, typ string
				body       map[string]any
			}{
				topic: re.actionWriter.Topic,
				typ:   "action.requested",
				body: map[string]any{
					"type":             "action.requested",
					"kind":             "scale_docker",
					"desired_replicas": 1,
					"created_at":       now,
					"dedup_key":        fmt.Sprintf("%s:%s:%d", fingerprint, "action.requested", 1),
				},
			},
		)
	default:
		// ignore other statuses
		return nil
	}

	// Write outbox and publish
	for _, m := range outMsgs {
		pjson, _ := json.Marshal(m.body)
		if err := writeOutbox(re.db, m.typ, string(pjson), now); err != nil {
			return err
		}
	}
	// Publish to Kafka
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, m := range outMsgs {
		pjson, _ := json.Marshal(m.body)
		var writer *kafka.Writer
		switch m.topic {
		case re.incidentWriter.Topic:
			writer = re.incidentWriter
		case re.actionWriter.Topic:
			writer = re.actionWriter
		default:
			return errors.New("unknown topic")
		}
		if err := writer.WriteMessages(ctx, kafka.Message{Value: pjson}); err != nil {
			log.Printf("kafka publish failed: %v", err)
		}
	}
	return nil
}

func main() {
	common.Init("rule-engine")

	// DB connect
	dsn := os.Getenv("RULES_DB_DSN")
	if dsn == "" {
		dsn = "postgres://rules:rules@rules-db:5432/rules?sslmode=disable"
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
	topicIn := os.Getenv("KAFKA_TOPIC_ALERT_RAISED")
	if topicIn == "" {
		topicIn = "alert.raised"
	}
	topicIncident := os.Getenv("KAFKA_TOPIC_INCIDENT_OPENED")
	if topicIncident == "" {
		topicIncident = "incident.opened"
	}
	topicAction := os.Getenv("KAFKA_TOPIC_ACTION_REQUESTED")
	if topicAction == "" {
		topicAction = "action.requested"
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topicIn,
		GroupID: "rule-engine",
	})
	incidentWriter := &kafka.Writer{Addr: kafka.TCP(brokers...), Topic: topicIncident, Balancer: &kafka.LeastBytes{}}
	actionWriter := &kafka.Writer{Addr: kafka.TCP(brokers...), Topic: topicAction, Balancer: &kafka.LeastBytes{}}

	re := &RuleEngine{db: db, ready: true, alertReader: reader, incidentWriter: incidentWriter, actionWriter: actionWriter}

	http.HandleFunc("/health", re.handleHealth)
	http.HandleFunc("/ready", re.handleReady)

	go func() {
		log.Printf("rule-engine listening on :8090")
		if err := http.ListenAndServe(":8090", nil); err != nil {
			log.Fatal(err)
		}
	}()

	log.Printf("rule-engine consuming from %s", topicIn)
	for {
		ctx := context.Background()
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			log.Printf("read error: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}
		if err := re.processAlert(msg); err != nil {
			log.Printf("process error: %v", err)
		}
	}
}
