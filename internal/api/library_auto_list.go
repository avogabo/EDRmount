package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/fusefs"
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

		// Collect children under rel.
		entries := map[string]*autoEntry{}
		addDir := func(name, childRel string) {
			key := "D:" + name
			if _, ok := entries[key]; ok {
				return
			}
			full := filepath.Join(autoRoot, childRel)
			entries[key] = &autoEntry{Name: name, Path: full, IsDir: true, Size: 0, ModTime: ""}
		}
		addFile := func(name, childRel string, importID string, idx int, size int64) {
			key := "F:" + importID + ":" + name
			if _, ok := entries[key]; ok {
				return
			}
			full := filepath.Join(autoRoot, childRel)
			entries[key] = &autoEntry{Name: name, Path: full, IsDir: false, Size: size, ModTime: time.Now().Format(time.RFC3339), ImportID: importID, FileIdx: idx}
		}

		// Iterate all imports, build their virtual paths, and keep only those under rel.
		// Limit to last N imports for safety.
		rows, err := s.jobs.DB().SQL.QueryContext(r.Context(), `SELECT id FROM nzb_imports ORDER BY imported_at DESC LIMIT 300`)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		for rows.Next() {
			var importID string
			if err := rows.Scan(&importID); err != nil {
				continue
			}
			paths, err := fusefs.AutoVirtualPathsForImport(r.Context(), cfg, s.jobs, importID)
			if err != nil {
				continue
			}
			for _, vr := range paths {
				vr = filepath.Clean(vr)
				if vr == "." || vr == "" {
					continue
				}
				// Match prefix
				if rel != "" {
					if vr == rel {
						// file path can't equal a dir prefix; ignore
						continue
					}
					if !strings.HasPrefix(vr, rel+string(filepath.Separator)) {
						continue
					}
				}
				// Determine immediate child after rel.
				sub := vr
				if rel != "" {
					sub = strings.TrimPrefix(vr, rel+string(filepath.Separator))
				}
				parts := strings.Split(sub, string(filepath.Separator))
				if len(parts) == 0 {
					continue
				}
				child := parts[0]
				childRel := child
				if rel != "" {
					childRel = filepath.Join(rel, child)
				}
				if len(parts) > 1 {
					addDir(child, childRel)
					continue
				}

				// It's a file directly under this dir; find its file_idx & size.
				// We resolve idx by looking up nzb_files filename/subject basename match.
				name := child
				var idx int
				var bytes int64
				// best-effort: match by mkv filename
				_ = s.jobs.DB().SQL.QueryRowContext(r.Context(), `SELECT idx,total_bytes FROM nzb_files WHERE import_id=? AND filename=? LIMIT 1`, importID, name).Scan(&idx, &bytes)
				if bytes == 0 {
					// fallback by subject basename
					var subj string
					_ = s.jobs.DB().SQL.QueryRowContext(r.Context(), `SELECT idx,subject,total_bytes FROM nzb_files WHERE import_id=? LIMIT 2000`, importID).Scan(&idx, &subj, &bytes)
				}
				addFile(child, childRel, importID, idx, bytes)
			}
		}

		out := make([]*autoEntry, 0, len(entries))
		for _, e := range entries {
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
