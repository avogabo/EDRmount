package watch

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/jobs"
)

type Watcher struct {
	jobs *jobs.Store

	NzbInbox   string
	MediaInbox string

	Interval time.Duration
}

func New(j *jobs.Store, nzbInbox, mediaInbox string) *Watcher {
	return &Watcher{jobs: j, NzbInbox: nzbInbox, MediaInbox: mediaInbox, Interval: 5 * time.Second}
}

func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.Interval)
	defer t.Stop()

	// Initial scan
	_ = w.scanOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = w.scanOnce(ctx)
		}
	}
}

func (w *Watcher) scanOnce(ctx context.Context) error {
	if w.jobs == nil {
		return nil
	}
	if err := w.scanNZB(ctx); err != nil {
		_ = w.jobs.AppendLog(ctx, "watch", fmt.Sprintf("watch scanNZB error: %v", err))
	}
	if err := w.scanMedia(ctx); err != nil {
		_ = w.jobs.AppendLog(ctx, "watch", fmt.Sprintf("watch scanMedia error: %v", err))
	}
	return nil
}

func (w *Watcher) scanNZB(ctx context.Context) error {
	root := w.NzbInbox
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".nzb") {
			continue
		}
		path := filepath.Join(root, name)
		info, err := e.Info()
		if err != nil {
			continue
		}
		if ok, _ := w.markSeen(ctx, path, "nzb", info); ok {
			_, _ = w.jobs.Enqueue(ctx, jobs.TypeImport, map[string]string{"path": path})
		}
	}
	return nil
}

func (w *Watcher) scanMedia(ctx context.Context) error {
	root := w.MediaInbox
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(root, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		// For now: enqueue upload for any file/dir. Classification comes later.
		if ok, _ := w.markSeen(ctx, path, "media", info); ok {
			_, _ = w.jobs.Enqueue(ctx, jobs.TypeUpload, map[string]string{"path": path})
		}
	}
	return nil
}

// markSeen returns ok=true if this path is new or changed and should be processed.
func (w *Watcher) markSeen(ctx context.Context, path, kind string, info fs.FileInfo) (bool, error) {
	d := w.jobs.DB().SQL
	size := info.Size()
	mtime := info.ModTime().Unix()

	var oldSize int64
	var oldMtime int64
	err := d.QueryRowContext(ctx, `SELECT size,mtime FROM ingest_seen WHERE path=?`, path).Scan(&oldSize, &oldMtime)
	if err != nil {
		if err == sql.ErrNoRows {
			_, err2 := d.ExecContext(ctx, `INSERT INTO ingest_seen(path,kind,size,mtime,seen_at) VALUES(?,?,?,?,?)`, path, kind, size, mtime, time.Now().Unix())
			return err2 == nil, err2
		}
		return false, err
	}

	if oldSize == size && oldMtime == mtime {
		return false, nil
	}
	_, err = d.ExecContext(ctx, `UPDATE ingest_seen SET kind=?, size=?, mtime=?, seen_at=? WHERE path=?`, kind, size, mtime, time.Now().Unix(), path)
	return err == nil, err
}
