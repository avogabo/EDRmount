package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

type fileRow struct {
	Idx           int      `json:"idx"`
	Filename      string   `json:"filename"`
	Subject       string   `json:"subject"`
	Poster        string   `json:"poster"`
	Date          int64    `json:"date"`
	Groups        []string `json:"groups"`
	SegmentsCount int      `json:"segments_count"`
	TotalBytes    int64    `json:"total_bytes"`
}

func (s *Server) registerCatalogFileRoutes() {
	// GET /api/v1/catalog/imports/{id}/files
	s.mux.HandleFunc("/api/v1/catalog/imports/", func(w http.ResponseWriter, r *http.Request) {
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

		path := strings.TrimPrefix(r.URL.Path, "/api/v1/catalog/imports/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[1] != "files" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}
		importID := parts[0]
		if importID == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "id required"})
			return
		}

		rows, err := s.jobs.DB().SQL.QueryContext(r.Context(), `SELECT idx,filename,subject,poster,date,groups_json,segments_count,total_bytes FROM nzb_files WHERE import_id=? ORDER BY idx ASC`, importID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()

		out := make([]fileRow, 0)
		for rows.Next() {
			var fr fileRow
			var groupsJSON string
			if err := rows.Scan(&fr.Idx, &fr.Filename, &fr.Subject, &fr.Poster, &fr.Date, &groupsJSON, &fr.SegmentsCount, &fr.TotalBytes); err != nil {
				continue
			}
			_ = json.Unmarshal([]byte(groupsJSON), &fr.Groups)
			out = append(out, fr)
		}
		_ = json.NewEncoder(w).Encode(out)
	})
}
