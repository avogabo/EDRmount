package api

import (
	"encoding/json"
	"net/http"
	"time"
)

type importRow struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	ImportedAt string `json:"imported_at"`
	FilesCount int    `json:"files_count"`
	TotalBytes int64  `json:"total_bytes"`
}

func (s *Server) registerCatalogRoutes() {
	s.mux.HandleFunc("/api/v1/catalog/imports", func(w http.ResponseWriter, r *http.Request) {
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
		rows, err := s.jobs.DB().SQL.QueryContext(r.Context(), `SELECT id,path,imported_at,files_count,total_bytes FROM nzb_imports ORDER BY imported_at DESC LIMIT 50`)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		out := make([]importRow, 0)
		for rows.Next() {
			var id, path string
			var tUnix int64
			var fc int
			var tb int64
			if err := rows.Scan(&id, &path, &tUnix, &fc, &tb); err != nil {
				continue
			}
			out = append(out, importRow{ID: id, Path: path, ImportedAt: time.Unix(tUnix, 0).Format(time.RFC3339), FilesCount: fc, TotalBytes: tb})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
}
