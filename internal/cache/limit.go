package cache

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

type fileInfo struct {
	path string
	size int64
	mt   time.Time
}

// EnforceSizeLimit removes oldest files under dir until total <= maxBytes.
// Best-effort; ignores errors.
func EnforceSizeLimit(dir string, maxBytes int64) {
	if maxBytes <= 0 {
		return
	}
	var files []fileInfo
	var total int64
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, fileInfo{path: p, size: st.Size(), mt: st.ModTime()})
		total += st.Size()
		return nil
	})
	if total <= maxBytes {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mt.Before(files[j].mt) })
	for _, f := range files {
		if total <= maxBytes {
			break
		}
		_ = os.Remove(f.path)
		total -= f.size
	}
}
