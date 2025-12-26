package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	kafka "github.com/segmentio/kafka-go"

	"github.com/ilya2309548/EventPulse/internal/common"
	"github.com/ilya2309548/EventPulse/internal/storage"
)

type Alert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt"`
	EndsAt      string            `json:"endsAt"`
	Fingerprint string            `json:"fingerprint"`
}

type Webhook struct {
	Status string  `json:"status"`
	Alerts []Alert `json:"alerts"`
}

type Server struct {
	db          *sql.DB
	ready       bool
	alertWriter *kafka.Writer
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.ready {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not-ready"))
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var wh Webhook
	if err := json.Unmarshal(body, &wh); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.Begin()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	// Collect messages to publish after DB commit
	var msgs []kafka.Message
	// Process each alert; upsert-like: increment occurrences by fingerprint
	for _, a := range wh.Alerts {
		labelsJSON, _ := json.Marshal(a.Labels)
		annotationsJSON, _ := json.Marshal(a.Annotations)
		// Try update existing alert by fingerprint; if not exists, insert
		res, err := tx.Exec(`UPDATE alerts SET status=$1, labels=$2, annotations=$3, starts_at=$4, ends_at=$5, last_seen=$6, occurrences=occurrences+1 WHERE fingerprint=$7`,
			a.Status, string(labelsJSON), string(annotationsJSON), a.StartsAt, a.EndsAt, now, a.Fingerprint)
		if err != nil {
			_ = tx.Rollback()
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		rows, _ := res.RowsAffected()
		if rows == 0 {
			_, err = tx.Exec(`INSERT INTO alerts (fingerprint, status, labels, annotations, starts_at, ends_at, first_seen, last_seen, occurrences) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,1)`,
				a.Fingerprint, a.Status, string(labelsJSON), string(annotationsJSON), a.StartsAt, a.EndsAt, now, now)
			if err != nil {
				_ = tx.Rollback()
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		// Write outbox event alert.raised
		// Deduplication key: fingerprint + event_type + status
		dedupKey := fmt.Sprintf("%s:%s:%s", a.Fingerprint, "alert.raised", a.Status)
		payload := map[string]any{
			"type":        "alert.raised",
			"fingerprint": a.Fingerprint,
			"status":      a.Status,
			"labels":      a.Labels,
			"annotations": a.Annotations,
			"dedup_key":   dedupKey,
			"created_at":  now,
		}
		pjson, _ := json.Marshal(payload)
		_, err = tx.Exec(`INSERT INTO outbox_events (type, payload, created_at) VALUES ($1,$2,$3)`, "alert.raised", string(pjson), now)
		if err != nil {
			_ = tx.Rollback()
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Prepare Kafka message (best-effort after commit)
		msgs = append(msgs, kafka.Message{Value: pjson})
	}
	if err := tx.Commit(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	// Publish to Kafka (best-effort)
	if s.alertWriter != nil && len(msgs) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := s.alertWriter.WriteMessages(ctx, msgs...); err != nil {
			log.Printf("kafka publish alert.raised failed: %v", err)
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func main() {
	common.Init("ingest")
	dsn := os.Getenv("INGEST_DB_DSN")
	if dsn == "" {
		dsn = "postgres://ingest:ingest@ingest-db:5432/ingest?sslmode=disable"
	}
	db, err := storage.Open(dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	if err := storage.Migrate(db); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	// Kafka writer setup
	var writer *kafka.Writer
	brokersEnv := strings.TrimSpace(os.Getenv("KAFKA_BROKERS"))
	topicAlert := strings.TrimSpace(os.Getenv("KAFKA_TOPIC_ALERT_RAISED"))
	if topicAlert == "" {
		topicAlert = "alert.raised"
	}
	if brokersEnv != "" {
		brokers := strings.Split(brokersEnv, ",")
		writer = &kafka.Writer{
			Addr:     kafka.TCP(brokers...),
			Topic:    topicAlert,
			Balancer: &kafka.LeastBytes{},
		}
		log.Printf("kafka writer configured: brokers=%v topic=%s", brokers, topicAlert)
	} else {
		log.Printf("kafka writer disabled: KAFKA_BROKERS not set")
	}

	srv := &Server{db: db, ready: true, alertWriter: writer}

	http.HandleFunc("/health", srv.handleHealth)
	http.HandleFunc("/ready", srv.handleReady)
	http.HandleFunc("/alertmanager", srv.handleWebhook)

	// Ensure data dir exists
	_ = os.MkdirAll("/data", 0o755)

	addr := ":8080"
	log.Printf("ingest listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
