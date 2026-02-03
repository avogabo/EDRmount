package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/gaby/EDRmount/internal/db"
)

var ErrNoQueuedJobs = errors.New("no queued jobs")

// ClaimNext sets the oldest queued job to running and returns it.
func (s *Store) ClaimNext(ctx context.Context) (*Job, error) {
	// sqlite: do a small transaction so claim is atomic.
	tx, err := s.db.SQL.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `SELECT id,type,state,created_at,updated_at,payload_json,error FROM jobs WHERE state=? ORDER BY created_at ASC LIMIT 1`, string(StateQueued))
	var (
		id, typ, st, payload string
		created, updated     int64
		errStr               *string
	)
	if err := row.Scan(&id, &typ, &st, &created, &updated, &payload, &errStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoQueuedJobs
		}
		return nil, err
	}

	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?, updated_at=? WHERE id=?`, string(StateRunning), now, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &Job{
		ID:        id,
		Type:      Type(typ),
		State:     StateRunning,
		CreatedAt: time.Unix(created, 0),
		UpdatedAt: time.Unix(now, 0),
		Payload:   []byte(payload),
		Error:     errStr,
	}, nil
}

func (s *Store) SetDone(ctx context.Context, jobID string) error {
	_, err := s.db.SQL.ExecContext(ctx, `UPDATE jobs SET state=?, updated_at=?, error=NULL WHERE id=?`, string(StateDone), time.Now().Unix(), jobID)
	return err
}

func (s *Store) SetFailed(ctx context.Context, jobID string, errMsg string) error {
	_, err := s.db.SQL.ExecContext(ctx, `UPDATE jobs SET state=?, updated_at=?, error=? WHERE id=?`, string(StateFailed), time.Now().Unix(), errMsg, jobID)
	return err
}

// Expose underlying DB for internal packages that need to store extra state.
func (s *Store) DB() *db.DB { return s.db }
