package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) registerJobLogRoutes() {
	// GET /api/v1/jobs/{id}/logs?limit=500
	s.mux.HandleFunc("/api/v1/jobs/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "jobs db not configured"})
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
		// expected: {id}/logs
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[1] != "logs" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}
		jobID := parts[0]
		if jobID == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "job id required"})
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		limit := 500
		if q := r.URL.Query().Get("limit"); q != "" {
			// best-effort parse
			var n int
			_, _ = fmt.Sscanf(q, "%d", &n)
			if n > 0 && n <= 5000 {
				limit = n
			}
		}

		lines, err := s.jobs.GetLogs(r.Context(), jobID, limit)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"job_id": jobID, "lines": lines})
	})
}
