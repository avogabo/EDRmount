package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

		// Fast listing from mounted filesystem to avoid expensive global DB scans.
		dirEntries, err := os.ReadDir(p)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		out := make([]*autoEntry, 0, len(dirEntries))
		for _, de := range dirEntries {
			name := de.Name()
			full := filepath.Join(p, name)
			st, _ := de.Info()
			e := &autoEntry{Name: name, Path: full, IsDir: de.IsDir(), Size: 0, ModTime: ""}
			if st != nil {
				e.Size = st.Size()
				e.ModTime = st.ModTime().Format(time.RFC3339)
			}
			out = append(out, e)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"entries": out})
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
