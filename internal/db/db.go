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
		`CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at);`,
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

		// Manual library view (UI-managed)
		`CREATE TABLE IF NOT EXISTS manual_dirs (
			id TEXT PRIMARY KEY,
			parent_id TEXT NOT NULL,
			name TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_manual_dirs_parent ON manual_dirs(parent_id);`,
		`CREATE TABLE IF NOT EXISTS manual_items (
			id TEXT PRIMARY KEY,
			dir_id TEXT NOT NULL,
			label TEXT NOT NULL,
			import_id TEXT NOT NULL,
			file_idx INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_manual_items_dir ON manual_items(dir_id);`,

		// Auto-library overrides (so Plex can still point at library-auto)
		`CREATE TABLE IF NOT EXISTS library_overrides (
			import_id TEXT NOT NULL,
			file_idx INTEGER NOT NULL,
			kind TEXT NOT NULL, -- "movie" | "tv" (reserved)
			title TEXT NOT NULL,
			year INTEGER NOT NULL,
			quality TEXT NOT NULL,
			tmdb_id INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(import_id, file_idx)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_library_overrides_updated ON library_overrides(updated_at);`,

		`CREATE TABLE IF NOT EXISTS library_review_dismissed (
			import_id TEXT NOT NULL,
			file_idx INTEGER NOT NULL,
			dismissed_at INTEGER NOT NULL,
			PRIMARY KEY(import_id, file_idx)
		);`,

		`CREATE TABLE IF NOT EXISTS library_resolved (
			import_id TEXT NOT NULL,
			file_idx INTEGER NOT NULL,
			kind TEXT NOT NULL,
			title TEXT NOT NULL,
			year INTEGER NOT NULL,
			quality TEXT NOT NULL,
			tmdb_id INTEGER NOT NULL,
			series_status TEXT NOT NULL,
			season INTEGER NOT NULL,
			episode INTEGER NOT NULL,
			episode_title TEXT NOT NULL,
			virtual_dir TEXT NOT NULL DEFAULT '',
			virtual_name TEXT NOT NULL DEFAULT '',
			virtual_path TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(import_id, file_idx)
		);`,
		`ALTER TABLE library_resolved ADD COLUMN virtual_dir TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE library_resolved ADD COLUMN virtual_name TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE library_resolved ADD COLUMN virtual_path TEXT NOT NULL DEFAULT '';`,
		`CREATE INDEX IF NOT EXISTS idx_library_resolved_import ON library_resolved(import_id);`,

		// Health scanning state
		`CREATE TABLE IF NOT EXISTS health_nzb_state (
			path TEXT PRIMARY KEY,
			status TEXT NOT NULL, -- "unknown"|"ok"|"broken"|"repairing"|"repaired"|"error"
			last_checked_at INTEGER,
			last_error TEXT,
			last_repair_job_id TEXT,
			last_repaired_at INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_health_nzb_status ON health_nzb_state(status);`,
		`CREATE INDEX IF NOT EXISTS idx_health_nzb_checked ON health_nzb_state(last_checked_at);`,

		`CREATE TABLE IF NOT EXISTS health_scan_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			run_id TEXT,
			cursor_path TEXT,
			run_started_at INTEGER,
			last_chunk_finished_at INTEGER,
			last_run_completed_at INTEGER
		);`,
		`INSERT OR IGNORE INTO health_scan_state(id) VALUES (1);`,
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
	seedManualRoot(d.SQL)

	// Recovery: if the container restarted mid-job, some jobs may be stuck in "running"
	// (or legacy "pending"). Normalize both to "queued" so the runner can pick them up.
	_, _ = d.SQL.Exec(`UPDATE jobs SET state='queued', updated_at=? WHERE state IN ('running','pending')`, nowUnix())
	return nil
}

func nowUnix() int64 { return time.Now().Unix() }
