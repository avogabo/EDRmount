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
				_ = json.NewEncoder(w).Encode(map[string]any{"entries": out})
				return
			}
		case <-time.After(1200 * time.Millisecond):
			// FUSE readdir can block intermittently. Fall back to DB-derived structure.
		}

		// Fallback fast path for folders/subfolders from NZB tree (no FUSE read).
		rows, err := s.jobs.DB().SQL.QueryContext(r.Context(), `SELECT path FROM nzb_imports ORDER BY imported_at DESC LIMIT 3000`)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		nzbRoot := strings.TrimSpace(cfg.Watch.NZB.Dir)
		if nzbRoot == "" {
			nzbRoot = "/host/inbox/nzb"
		}
		nzbRoot = filepath.Clean(nzbRoot)

		dirs := map[string]*autoEntry{}
		files := map[string]*autoEntry{}
		for rows.Next() {
			var nzbPath string
			if err := rows.Scan(&nzbPath); err != nil {
				continue
			}
			nzbPath = filepath.Clean(nzbPath)
			if !(nzbPath == nzbRoot || strings.HasPrefix(nzbPath, nzbRoot+string(filepath.Separator))) {
				continue
			}
			relNZB, err := filepath.Rel(nzbRoot, nzbPath)
			if err != nil {
				continue
			}
			relNZB = filepath.Clean(relNZB)
			if relNZB == "." || relNZB == "" {
				continue
			}
			if relNZB == rel || !strings.HasPrefix(relNZB, rel+string(filepath.Separator)) {
				continue
			}
			sub := strings.TrimPrefix(relNZB, rel+string(filepath.Separator))
			parts := strings.Split(sub, string(filepath.Separator))
			if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
				continue
			}
			child := parts[0]
			childRel := filepath.Join(rel, child)
			full := filepath.Join(autoRoot, childRel)
			if len(parts) > 1 {
				dirs[child] = &autoEntry{Name: child, Path: full, IsDir: true}
			} else {
				files[child] = &autoEntry{Name: child, Path: full, IsDir: false}
			}
		}
		out := make([]*autoEntry, 0, len(dirs)+len(files))
		for _, e := range dirs {
			out = append(out, e)
		}
		for _, e := range files {
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
