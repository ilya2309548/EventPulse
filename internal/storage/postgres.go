package storage

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Open opens a PostgreSQL database using pgx stdlib driver.
func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate creates minimal tables if not exist (text-based times for simplicity).
func Migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS alerts (
			id SERIAL PRIMARY KEY,
			fingerprint TEXT,
			status TEXT,
			labels TEXT,
			annotations TEXT,
			starts_at TEXT,
			ends_at TEXT,
			first_seen TEXT,
			last_seen TEXT,
			occurrences INTEGER DEFAULT 1
		)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_fp ON alerts(fingerprint)`,
		`CREATE TABLE IF NOT EXISTS outbox_events (
			id SERIAL PRIMARY KEY,
			type TEXT NOT NULL,
			payload TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS inbox (
			id SERIAL PRIMARY KEY,
			dedup_key TEXT NOT NULL UNIQUE,
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
