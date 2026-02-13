package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gaby/EDRmount/internal/config"
)

type autoEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	ModTime  string `json:"mod_time"`
	ImportID string `json:"import_id,omitempty"`
	FileIdx  int    `json:"file_idx,omitempty"`
}

type autoCacheEntry struct {
	Entries []*autoEntry
	At      time.Time
}

var autoFuseCache sync.Map

func (s *Server) registerLibraryAutoListRoutes() {
	// GET /api/v1/library/auto/list?path=/mount/library-auto/PELICULAS/...
	// Returns a virtual listing with (import_id,file_idx) for files so UI can delete globally/fully.
	s.mux.HandleFunc("/api/v1/library/auto/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "db not configured"})
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		cfg := s.Config()
		mp := strings.TrimSpace(cfg.Paths.MountPoint)
		if mp == "" {
			mp = "/host/mount"
		}
		autoRoot := filepath.Join(filepath.Clean(mp), "library-auto")

		p := strings.TrimSpace(r.URL.Query().Get("path"))
		if p == "" {
			p = autoRoot
		}
		p = filepath.Clean(p)
		if p == autoRoot {
			// ok
		} else if !strings.HasPrefix(p, autoRoot+string(filepath.Separator)) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "path outside library-auto"})
			return
		}

		rel := strings.TrimPrefix(p, autoRoot)
		rel = strings.TrimPrefix(rel, string(filepath.Separator))
		rel = filepath.Clean(rel)
		if rel == "." {
			rel = ""
		}

		// Fast path: root listing doesn't need DB scans.
		if rel == "" {
			l := cfg.Library.Defaults()
			movies := filepath.Join(autoRoot, l.MoviesRoot)
			series := filepath.Join(autoRoot, l.SeriesRoot)
			_ = json.NewEncoder(w).Encode(map[string]any{"entries": []*autoEntry{
				{Name: l.MoviesRoot, Path: movies, IsDir: true, Size: 0, ModTime: ""},
				{Name: l.SeriesRoot, Path: series, IsDir: true, Size: 0, ModTime: ""},
			}})
			return
		}

		// FUSE-driven listing first (as requested), with timeout guard.
		type rdRes struct {
			des []os.DirEntry
			err error
		}
		rc := make(chan rdRes, 1)
		go func() {
			des, err := os.ReadDir(p)
			rc <- rdRes{des: des, err: err}
		}()
		select {
		case rr := <-rc:
			if rr.err == nil {
				out := make([]*autoEntry, 0, len(rr.des))
				for _, de := range rr.des {
					name := de.Name()
					full := filepath.Join(p, name)
					out = append(out, &autoEntry{Name: name, Path: full, IsDir: de.IsDir(), Size: 0, ModTime: ""})
				}
				autoFuseCache.Store(p, autoCacheEntry{Entries: out, At: time.Now()})
				_ = json.NewEncoder(w).Encode(map[string]any{"entries": out})
				return
			}
			if v, ok := autoFuseCache.Load(p); ok {
				ce := v.(autoCacheEntry)
				_ = json.NewEncoder(w).Encode(map[string]any{"entries": ce.Entries, "cached": true, "stale_seconds": int(time.Since(ce.At).Seconds())})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": rr.err.Error()})
			return
		case <-time.After(1200 * time.Millisecond):
			if v, ok := autoFuseCache.Load(p); ok {
				ce := v.(autoCacheEntry)
				_ = json.NewEncoder(w).Encode(map[string]any{"entries": ce.Entries, "cached": true, "stale_seconds": int(time.Since(ce.At).Seconds())})
				return
			}
			w.WriteHeader(http.StatusGatewayTimeout)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "fuse listing timeout"})
			return
		}
	})

	// GET /api/v1/library/auto/root
	s.mux.HandleFunc("/api/v1/library/auto/root", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Config()
		mp := strings.TrimSpace(cfg.Paths.MountPoint)
		if mp == "" {
			mp = "/host/mount"
		}
		autoRoot := filepath.Join(filepath.Clean(mp), "library-auto")
		_ = json.NewEncoder(w).Encode(map[string]string{"root": autoRoot})
	})
}

// silence unused import on some builds
var _ = sql.ErrNoRows
var _ = config.Config{}
