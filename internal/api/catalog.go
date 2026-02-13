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
		switch r.Method {
		case http.MethodGet:
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
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// POST /api/v1/catalog/imports/delete { id }
	s.mux.HandleFunc("/api/v1/catalog/imports/delete", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "db not configured"})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		id := req.ID
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "id required"})
			return
		}

		// Delete import and all associated DB rows. Does NOT delete the NZB file on disk.
		tx, err := s.jobs.DB().SQL.BeginTx(r.Context(), nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer func() { _ = tx.Rollback() }()

		stmts := []string{
			`DELETE FROM nzb_segments WHERE import_id=?`,
			`DELETE FROM nzb_files WHERE import_id=?`,
			`DELETE FROM library_overrides WHERE import_id=?`,
			`DELETE FROM library_review_dismissed WHERE import_id=?`,
			`DELETE FROM library_resolved WHERE import_id=?`,
			`DELETE FROM manual_items WHERE import_id=?`,
			`DELETE FROM nzb_imports WHERE id=?`,
		}
		for _, s := range stmts {
			if _, err := tx.ExecContext(r.Context(), s, id); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
		}
		if err := tx.Commit(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
}
