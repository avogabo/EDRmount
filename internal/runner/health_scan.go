package runner

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
	"github.com/gaby/EDRmount/internal/nntp"
	"github.com/gaby/EDRmount/internal/nzb"
)

func (r *Runner) runHealthScan(ctx context.Context, j *jobs.Job) {
	_ = r.jobs.AppendLog(ctx, j.ID, "starting health scan job")

	cfg := config.Default()
	if r.GetConfig != nil {
		cfg = r.GetConfig()
	}
	if !cfg.Health.Enabled || !cfg.Health.Scan.Enabled {
		msg := "health scan: disabled by config"
		_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
		_ = r.jobs.SetFailed(ctx, j.ID, msg)
		return
	}

	budget := time.Duration(cfg.Health.Scan.MaxDurationMinutes) * time.Minute
	if budget <= 0 {
		budget = 180 * time.Minute
	}
	deadline := time.Now().Add(budget)

	outRoot := strings.TrimSpace(cfg.NgPost.OutputDir)
	if outRoot == "" {
		outRoot = "/host/inbox/nzb"
	}

	db := r.jobs.DB().SQL

	// Load scan cursor
	var cursor sql.NullString
	_ = db.QueryRowContext(ctx, `SELECT cursor_path FROM health_scan_state WHERE id=1`).Scan(&cursor)
	cursorPath := ""
	if cursor.Valid {
		cursorPath = cursor.String
	}
	if cursorPath == "" {
		_, _ = db.ExecContext(ctx, `UPDATE health_scan_state SET run_started_at=? WHERE id=1`, time.Now().Unix())
	}

	// List all NZBs (deterministic order)
	paths := make([]string, 0, 1024)
	_ = filepath.WalkDir(outRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".nzb") {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("health scan: found %d nzb(s)", len(paths)))

	startIdx := 0
	if cursorPath != "" {
		startIdx = sort.SearchStrings(paths, cursorPath)
		if startIdx < len(paths) && paths[startIdx] == cursorPath {
			startIdx++
		}
		if startIdx < 0 {
			startIdx = 0
		}
	}

	// NNTP client for STAT checks
	pool := nntp.NewPool(nntp.Config{Host: cfg.Download.Host, Port: cfg.Download.Port, SSL: cfg.Download.SSL, User: cfg.Download.User, Pass: cfg.Download.Pass, Timeout: 30 * time.Second}, cfg.Download.Connections)
	cl, err := pool.Acquire(ctx)
	if err != nil {
		msg := "health scan: nntp acquire failed: " + err.Error()
		_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
		_ = r.jobs.SetFailed(ctx, j.ID, msg)
		return
	}
	defer pool.Release(cl)

	checked := 0
	broken := 0
	lastProcessed := ""
	for idx := startIdx; idx < len(paths); idx++ {
		if time.Now().After(deadline) {
			_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("health scan: budget reached (checked=%d broken=%d), pausing", checked, broken))
			if lastProcessed != "" {
				_, _ = db.ExecContext(ctx, `UPDATE health_scan_state SET cursor_path=?, last_chunk_finished_at=? WHERE id=1`, lastProcessed, time.Now().Unix())
			}
			_ = r.jobs.SetDone(ctx, j.ID)
			return
		}

		p := paths[idx]
		lastProcessed = p
		checked++
		if checked%20 == 0 {
			_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("health scan: progress %d/%d (broken=%d)", idx+1, len(paths), broken))
		}

		status, err := healthCheckNZB(ctx, cl, p)
		now := time.Now().Unix()
		if err != nil {
			_, _ = db.ExecContext(ctx, `INSERT INTO health_nzb_state(path,status,last_checked_at,last_error) VALUES(?,?,?,?)
				ON CONFLICT(path) DO UPDATE SET status=excluded.status,last_checked_at=excluded.last_checked_at,last_error=excluded.last_error`, p, "error", now, err.Error())
			continue
		}

		if status == "broken" {
			broken++
			_, _ = db.ExecContext(ctx, `INSERT INTO health_nzb_state(path,status,last_checked_at,last_error) VALUES(?,?,?,NULL)
				ON CONFLICT(path) DO UPDATE SET status=excluded.status,last_checked_at=excluded.last_checked_at,last_error=NULL`, p, "broken", now)
			if cfg.Health.Scan.AutoRepair {
				rep, _ := r.jobs.Enqueue(ctx, jobs.TypeHealthRepair, map[string]string{"path": p})
				jid := ""
				if rep != nil {
					jid = rep.ID
				}
				_, _ = db.ExecContext(ctx, `UPDATE health_nzb_state SET status=?, last_repair_job_id=? WHERE path=?`, "repairing", jid, p)
			}
			// advance cursor
			_, _ = db.ExecContext(ctx, `UPDATE health_scan_state SET cursor_path=?, last_chunk_finished_at=? WHERE id=1`, p, now)
			continue
		}

		_, _ = db.ExecContext(ctx, `INSERT INTO health_nzb_state(path,status,last_checked_at,last_error) VALUES(?,?,?,NULL)
			ON CONFLICT(path) DO UPDATE SET status=excluded.status,last_checked_at=excluded.last_checked_at,last_error=NULL`, p, "ok", now)
		_, _ = db.ExecContext(ctx, `UPDATE health_scan_state SET cursor_path=?, last_chunk_finished_at=? WHERE id=1`, p, now)
	}

	// Completed full run
	_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("health scan: completed (checked=%d broken=%d)", checked, broken))
	_, _ = db.ExecContext(ctx, `UPDATE health_scan_state SET cursor_path=NULL, last_run_completed_at=?, last_chunk_finished_at=? WHERE id=1`, time.Now().Unix(), time.Now().Unix())
	_ = r.jobs.SetDone(ctx, j.ID)
}

func healthCheckNZB(ctx context.Context, cl *nntp.Client, nzbPath string) (string, error) {
	f, err := os.Open(nzbPath)
	if err != nil {
		return "error", err
	}
	doc, err := nzb.Parse(f)
	_ = f.Close()
	if err != nil {
		return "error", err
	}
	for _, file := range doc.Files {
		// Only check MKV segments
		if !strings.Contains(strings.ToLower(file.Subject), ".mkv") {
			continue
		}
		segs := make([]nzb.Segment, 0, len(file.Segments))
		segs = append(segs, file.Segments...)
		sort.Slice(segs, func(i, j int) bool { return segs[i].Number < segs[j].Number })
		for _, s := range segs {
			id := strings.TrimSpace(s.ID)
			if id == "" {
				return "broken", nil
			}
			if err := cl.StatByMessageID(id); err != nil {
				return "broken", nil
			}
		}
	}
	return "ok", nil
}
