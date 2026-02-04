package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gaby/EDRmount/internal/streamer"
	"github.com/gaby/EDRmount/internal/subject"
)

func (s *Server) registerRawStreamRoutes() {
	// GET /api/v1/raw/imports/{id}/files/{filename}
	s.mux.HandleFunc("/api/v1/raw/imports/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/files/") {
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "db not configured"})
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/api/v1/raw/imports/")
		parts := strings.SplitN(path, "/files/", 2)
		if len(parts) != 2 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		importID := strings.Trim(parts[0], "/")
		filename := parts[1]
		filename = filepath.Base(filename)
		if importID == "" || filename == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Find matching file_idx by subject-derived filename
		rows, err := s.jobs.DB().SQL.QueryContext(r.Context(), `SELECT idx,subject FROM nzb_files WHERE import_id=? ORDER BY idx ASC`, importID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		fileIdx := -1
		for rows.Next() {
			var idx int
			var subj string
			_ = rows.Scan(&idx, &subj)
			fn, ok := subject.FilenameFromSubject(subj)
			if ok && fn == filename {
				fileIdx = idx
				break
			}
		}
		if fileIdx < 0 {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "file not found in import"})
			return
		}

		st := streamer.New(s.cfg.Download, s.jobs, s.cfg.Paths.CacheDir)
		localPath, err := st.EnsureFile(r.Context(), importID, fileIdx, filename)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		f, err := os.Open(localPath)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer f.Close()
		fi, _ := f.Stat()
		w.Header().Set("Content-Type", "application/octet-stream")
		if fi != nil {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size()))
		}
		_, _ = io.Copy(w, f)
	})
}
