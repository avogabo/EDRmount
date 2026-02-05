package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// Allow concurrent readers while keeping WAL + busy_timeout.
	// modernc.org/sqlite is fine with multiple conns; writes will serialize.
	s.SetMaxOpenConns(4)
	s.SetMaxIdleConns(4)

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

		`CREATE TABLE IF NOT EXISTS nzb_imports (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL,
			imported_at INTEGER NOT NULL,
			files_count INTEGER NOT NULL,
			total_bytes INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_nzb_imports_time ON nzb_imports(imported_at);`,

		`CREATE TABLE IF NOT EXISTS nzb_files (
			import_id TEXT NOT NULL,
			idx INTEGER NOT NULL,
			subject TEXT NOT NULL,
			filename TEXT,
			poster TEXT,
			date INTEGER,
			groups_json TEXT NOT NULL,
			segments_count INTEGER NOT NULL,
			total_bytes INTEGER NOT NULL,
			PRIMARY KEY(import_id, idx)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_nzb_files_import ON nzb_files(import_id);`,
		// Backward-compatible migration for older DBs
		`ALTER TABLE nzb_files ADD COLUMN filename TEXT;`,

		`CREATE TABLE IF NOT EXISTS nzb_segments (
			import_id TEXT NOT NULL,
			file_idx INTEGER NOT NULL,
			number INTEGER NOT NULL,
			bytes INTEGER NOT NULL,
			message_id TEXT NOT NULL,
			PRIMARY KEY(import_id, file_idx, number)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_nzb_segments_file ON nzb_segments(import_id, file_idx);`,
	}
	for _, s := range stmts {
		if _, err := d.SQL.Exec(s); err != nil {
			// ignore duplicate-column errors for ALTER TABLE
			es := err.Error()
			if strings.Contains(es, "duplicate") || strings.Contains(es, "already exists") {
				continue
			}
			return err
		}
	}
	// Best-effort backfill filename for older imports
	_ = backfillFilenames(d.SQL)
	return nil
}

func nowUnix() int64 { return time.Now().Unix() }
