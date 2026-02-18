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

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
)

type Watcher struct {
	jobs *jobs.Store

	NZB   config.WatchKind
	Media config.WatchKind

	Interval time.Duration
}

func New(j *jobs.Store, nzb, media config.WatchKind) *Watcher {
	return &Watcher{jobs: j, NZB: nzb, Media: media, Interval: 5 * time.Second}
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
	if w.NZB.Enabled {
		if err := w.scanNZB(ctx); err != nil {
			_ = w.jobs.AppendLog(ctx, "watch", fmt.Sprintf("watch scanNZB error: %v", err))
		}
	}
	if w.Media.Enabled {
		if err := w.scanMedia(ctx); err != nil {
			_ = w.jobs.AppendLog(ctx, "watch", fmt.Sprintf("watch scanMedia error: %v", err))
		}
	}
	return nil
}

func (w *Watcher) scanNZB(ctx context.Context) error {
	root := w.NZB.Dir
	if root == "" {
		return nil
	}

	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if !w.NZB.Recursive {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".nzb") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if ok, _ := w.markSeen(ctx, path, "nzb", info); ok {
			_, _ = w.jobs.Enqueue(ctx, jobs.TypeImport, map[string]string{"path": path})
		}
		return nil
	}

	return filepath.WalkDir(root, walkFn)
}

func (w *Watcher) scanMedia(ctx context.Context) error {
	root := w.Media.Dir
	if root == "" {
		return nil
	}
	// Avoid processing incomplete files while they are being copied into the inbox.
	// Require the file/dir signature to be unchanged for this duration before enqueueing.
	// NOTE: do NOT rely on file ModTime age for readiness: many copy/move tools preserve
	// source mtimes, which can make fresh files look "old" and trigger premature uploads.
	stableFor := 60 * time.Second
	// Folders are trickier (series/season copies can pause between episodes).
	// Use a stricter two-window confirmation for folder enqueueing.
	folderStableFor := 60 * time.Second

	isVideo := func(name string) bool {
		low := strings.ToLower(name)
		return strings.HasSuffix(low, ".mkv") || strings.HasSuffix(low, ".mp4") || strings.HasSuffix(low, ".avi") || strings.HasSuffix(low, ".m4v")
	}
	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if !w.Media.Recursive {
				return fs.SkipDir
			}

			// Generic folder handling:
			// - wait until folder signature is stable (bytes/count/mtime)
			// - then enqueue as folder pack
			// - special case: if folder has exactly one video, enqueue that file path
			//   so movie-in-folder behaves like single-file upload.
			vidCount, totalBytes, maxMtime, _ := mediaDirSignature(path, isVideo)
			if vidCount > 0 {
				sigPath := path + "#pack"

				// If we detect temporary/in-progress artifacts (e.g. rsync ".file.mkv.XYZ"),
				// force the folder to stay pending.
				if hasInProgressArtifacts(path) {
					_, _ = w.markStableSignature(ctx, sigPath, "media_pack_pending", "media_pack", totalBytes+int64(vidCount), time.Now().Unix(), folderStableFor)
					return fs.SkipDir
				}

				if ok, _ := w.markStableSignatureConfirmed(ctx, sigPath, "media_pack_pending", "media_pack_armed", "media_pack", totalBytes+int64(vidCount), maxMtime, folderStableFor); ok {
					enqueuePath := path
					// Only collapse folder->single file when the folder is flat (no subdirs).
					// If subdirs exist (e.g. "Serie/Temporada 1/..."), keep folder enqueue to
					// avoid early single-episode uploads while the season is still copying.
					if vidCount == 1 && !hasSubdirs(path) {
						if one, ok := singleVideoPath(path, isVideo); ok {
							enqueuePath = one
						}
					}
					_, _ = w.jobs.Enqueue(ctx, jobs.TypeUpload, map[string]string{"path": enqueuePath})
				}
				// Never descend into non-empty media folders: avoids racing uploads
				// of individual files while a copy is still in progress.
				return fs.SkipDir
			}

			return nil
		}

		if !isVideo(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if ok, _ := w.markStable(ctx, path, "media_pending", "media", info, stableFor); ok {
			_, _ = w.jobs.Enqueue(ctx, jobs.TypeUpload, map[string]string{"path": path})
		}
		return nil
	}
	return filepath.WalkDir(root, walkFn)
}

// markSeen returns ok=true if this path is new or changed and should be processed.
func mediaDirSignature(root string, isVideo func(string) bool) (videoCount int, totalBytes int64, maxMtime int64, newestAge time.Duration) {
	now := time.Now()
	newestAge = 365 * 24 * time.Hour
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return nil
		}
		if !isVideo(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		videoCount++
		totalBytes += info.Size()
		mt := info.ModTime().Unix()
		if mt > maxMtime {
			maxMtime = mt
		}
		age := now.Sub(info.ModTime())
		if age < newestAge {
			newestAge = age
		}
		return nil
	})
	if videoCount == 0 {
		newestAge = 0
	}
	return
}

func singleVideoPath(root string, isVideo func(string) bool) (string, bool) {
	var first string
	count := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return nil
		}
		if !isVideo(d.Name()) {
			return nil
		}
		count++
		if count == 1 {
			first = p
		}
		return nil
	})
	return first, count == 1 && strings.TrimSpace(first) != ""
}

func hasSubdirs(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
}

func hasInProgressArtifacts(root string) bool {
	isTempName := func(name string) bool {
		n := strings.ToLower(strings.TrimSpace(name))
		if n == "" {
			return false
		}
		if strings.HasPrefix(n, ".") && strings.Contains(n, ".mkv.") {
			return true // common rsync temp pattern: .file.mkv.XXXXXX
		}
		return strings.HasSuffix(n, ".part") || strings.HasSuffix(n, ".partial") || strings.HasSuffix(n, ".tmp") || strings.HasSuffix(n, ".!qb")
	}
	found := false
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil || d == nil {
			return nil
		}
		if isTempName(d.Name()) {
			found = true
			return fs.SkipDir
		}
		return nil
	})
	return found
}

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

// markStable returns ok=true once the item has been unchanged for at least stableFor.
// We store seen_at as "last_changed_at" for pending kinds.
func (w *Watcher) markStable(ctx context.Context, path, pendingKind, readyKind string, info fs.FileInfo, stableFor time.Duration) (bool, error) {
	return w.markStableSignature(ctx, path, pendingKind, readyKind, info.Size(), info.ModTime().Unix(), stableFor)
}

func (w *Watcher) markStableSignature(ctx context.Context, path, pendingKind, readyKind string, size, mtime int64, stableFor time.Duration) (bool, error) {
	d := w.jobs.DB().SQL
	now := time.Now().Unix()
	stableSecs := int64(stableFor.Seconds())
	if stableSecs < 1 {
		stableSecs = 1
	}

	var oldKind string
	var oldSize int64
	var oldMtime int64
	var lastChangedAt int64
	err := d.QueryRowContext(ctx, `SELECT kind,size,mtime,seen_at FROM ingest_seen WHERE path=?`, path).Scan(&oldKind, &oldSize, &oldMtime, &lastChangedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			// First time we see it: mark pending and wait for stability.
			_, err2 := d.ExecContext(ctx, `INSERT INTO ingest_seen(path,kind,size,mtime,seen_at) VALUES(?,?,?,?,?)`, path, pendingKind, size, mtime, now)
			return false, err2
		}
		return false, err
	}

	// If already ready/enqueued, don't trigger again.
	if oldKind == readyKind {
		return false, nil
	}

	// If it changed, keep it pending and update last_changed_at.
	if oldSize != size || oldMtime != mtime {
		_, err = d.ExecContext(ctx, `UPDATE ingest_seen SET kind=?, size=?, mtime=?, seen_at=? WHERE path=?`, pendingKind, size, mtime, now, path)
		return false, err
	}

	// Unchanged: if pending and old enough, mark ready.
	if oldKind == pendingKind {
		if now-lastChangedAt >= stableSecs {
			_, err = d.ExecContext(ctx, `UPDATE ingest_seen SET kind=?, size=?, mtime=?, seen_at=? WHERE path=?`, readyKind, size, mtime, now, path)
			return err == nil, err
		}
		return false, nil
	}

	// Unknown kind: treat it as pending (backward compat).
	_, err = d.ExecContext(ctx, `UPDATE ingest_seen SET kind=?, size=?, mtime=?, seen_at=? WHERE path=?`, pendingKind, size, mtime, now, path)
	return false, err
}

// markStableSignatureConfirmed is like markStableSignature but requires two
// consecutive stable windows before returning ready=true:
// pendingKind --(stable window 1)--> armedKind --(stable window 2)--> readyKind.
func (w *Watcher) markStableSignatureConfirmed(ctx context.Context, path, pendingKind, armedKind, readyKind string, size, mtime int64, stableFor time.Duration) (bool, error) {
	d := w.jobs.DB().SQL
	now := time.Now().Unix()
	stableSecs := int64(stableFor.Seconds())
	if stableSecs < 1 {
		stableSecs = 1
	}

	var oldKind string
	var oldSize int64
	var oldMtime int64
	var lastChangedAt int64
	err := d.QueryRowContext(ctx, `SELECT kind,size,mtime,seen_at FROM ingest_seen WHERE path=?`, path).Scan(&oldKind, &oldSize, &oldMtime, &lastChangedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			_, err2 := d.ExecContext(ctx, `INSERT INTO ingest_seen(path,kind,size,mtime,seen_at) VALUES(?,?,?,?,?)`, path, pendingKind, size, mtime, now)
			return false, err2
		}
		return false, err
	}

	if oldKind == readyKind {
		return false, nil
	}

	if oldSize != size || oldMtime != mtime {
		_, err = d.ExecContext(ctx, `UPDATE ingest_seen SET kind=?, size=?, mtime=?, seen_at=? WHERE path=?`, pendingKind, size, mtime, now, path)
		return false, err
	}

	if oldKind == pendingKind {
		if now-lastChangedAt >= stableSecs {
			_, err = d.ExecContext(ctx, `UPDATE ingest_seen SET kind=?, size=?, mtime=?, seen_at=? WHERE path=?`, armedKind, size, mtime, now, path)
			return false, err
		}
		return false, nil
	}
	if oldKind == armedKind {
		if now-lastChangedAt >= stableSecs {
			_, err = d.ExecContext(ctx, `UPDATE ingest_seen SET kind=?, size=?, mtime=?, seen_at=? WHERE path=?`, readyKind, size, mtime, now, path)
			return err == nil, err
		}
		return false, nil
	}

	_, err = d.ExecContext(ctx, `UPDATE ingest_seen SET kind=?, size=?, mtime=?, seen_at=? WHERE path=?`, pendingKind, size, mtime, now, path)
	return false, err
}
