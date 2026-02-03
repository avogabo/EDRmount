package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	SQL *sql.DB
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	// modernc sqlite uses file: URI as well; keep it simple.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	s, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s.SetMaxOpenConns(1) // sqlite (single writer) + simplifies concurrency early.

	d := &DB{SQL: s}
	if err := d.migrate(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error { return d.SQL.Close() }

func (d *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			state TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			payload_json TEXT NOT NULL,
			error TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_state_updated ON jobs(state, updated_at);`,
		`CREATE TABLE IF NOT EXISTS job_logs (
			job_id TEXT NOT NULL,
			ts INTEGER NOT NULL,
			line TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_job_logs_job_ts ON job_logs(job_id, ts);`,
		`CREATE TABLE IF NOT EXISTS ingest_seen (
			path TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			size INTEGER NOT NULL,
			mtime INTEGER NOT NULL,
			seen_at INTEGER NOT NULL
		);`,
	}
	for _, s := range stmts {
		if _, err := d.SQL.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func nowUnix() int64 { return time.Now().Unix() }
