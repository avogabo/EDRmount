package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gaby/EDRmount/internal/subject"
)

type rawItem struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Bytes    int64  `json:"bytes"`
	Segments int    `json:"segments"`
	Subject  string `json:"subject"`
}

func (s *Server) registerRawRoutes() {
	// GET /api/v1/raw/imports/{id}
	// GET /api/v1/raw/imports/{id}/files/{filename}
	s.mux.HandleFunc("/api/v1/raw/imports/", func(w http.ResponseWriter, r *http.Request) {
		if s.jobs == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "db not configured"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Stream variant
		if strings.Contains(r.URL.Path, "/files/") {
			s.handleRawFileStream(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		importID := strings.TrimPrefix(r.URL.Path, "/api/v1/raw/imports/")
		importID = strings.Trim(importID, "/")
		if importID == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "id required"})
			return
		}

		rows, err := s.jobs.DB().SQL.QueryContext(r.Context(), `SELECT idx,subject,segments_count,total_bytes FROM nzb_files WHERE import_id=? ORDER BY idx ASC`, importID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()

		out := make([]rawItem, 0)
		for rows.Next() {
			var idx int
			var subj string
			var segs int
			var bytes int64
			if err := rows.Scan(&idx, &subj, &segs, &bytes); err != nil {
				continue
			}
			fn, ok := subject.FilenameFromSubject(subj)
			if !ok {
				fn = "file_" + strings.ReplaceAll(strings.ReplaceAll(subj, " ", "_"), "/", "_")
			}
			out = append(out, rawItem{
				Path:     "/raw/" + importID + "/" + fn,
				Filename: fn,
				Bytes:    bytes,
				Segments: segs,
				Subject:  subj,
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	// GET|HEAD /api/v1/play/{importId}/{fileIdx}
	// Optional query param: ?filename=<name> (only used for cache naming/content-disposition)
	s.mux.HandleFunc("/api/v1/play/", func(w http.ResponseWriter, r *http.Request) {
		if s.jobs == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "db not configured"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.handlePlayStream(w, r)
	})
}
