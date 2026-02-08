package health

import (
	"context"
	"database/sql"
	"time"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
)

type Scheduler struct {
	Jobs *jobs.Store
	Cfg  func() config.HealthConfig

	Tick time.Duration
}

func (s *Scheduler) Run(ctx context.Context) {
	if s.Jobs == nil || s.Cfg == nil {
		return
	}
	if s.Tick <= 0 {
		s.Tick = 60 * time.Second
	}
	t := time.NewTicker(s.Tick)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cfg := s.Cfg()
			if !cfg.Enabled || !cfg.Scan.Enabled {
				continue
			}

			// Don't enqueue if a scan is already queued/running.
			if hasActiveHealthScan(ctx, s.Jobs.DB().SQL) {
				continue
			}

			cursor, lastChunk, lastRun := loadState(ctx, s.Jobs.DB().SQL)
			now := time.Now().Unix()

			if cursor != "" {
				// Incomplete run: resume after chunk interval.
				wait := int64(cfg.Scan.ChunkEveryHours) * 3600
				if wait <= 0 {
					wait = 24 * 3600
				}
				if lastChunk == 0 || (now-lastChunk) >= wait {
					_, _ = s.Jobs.Enqueue(ctx, jobs.TypeHealthScan, map[string]string{})
				}
				continue
			}

			// Completed run: start new full run after interval.
			wait := int64(cfg.Scan.IntervalHours) * 3600
			if wait <= 0 {
				wait = 24 * 3600
			}
			if lastRun == 0 || (now-lastRun) >= wait {
				_, _ = s.Jobs.Enqueue(ctx, jobs.TypeHealthScan, map[string]string{})
			}
		}
	}
}

func hasActiveHealthScan(ctx context.Context, db *sql.DB) bool {
	row := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM jobs WHERE type=? AND state IN (?,?)`, string(jobs.TypeHealthScan), string(jobs.StateQueued), string(jobs.StateRunning))
	var n int
	if err := row.Scan(&n); err != nil {
		return false
	}
	return n > 0
}

func loadState(ctx context.Context, db *sql.DB) (cursor string, lastChunk int64, lastRun int64) {
	var c sql.NullString
	_ = db.QueryRowContext(ctx, `SELECT cursor_path, COALESCE(last_chunk_finished_at,0), COALESCE(last_run_completed_at,0) FROM health_scan_state WHERE id=1`).Scan(&c, &lastChunk, &lastRun)
	if c.Valid {
		cursor = c.String
	}
	return
}
