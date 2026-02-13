package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/jobs"
)

type healthScanEntry struct {
	Path              string    `json:"path"`
	RelPath           string    `json:"rel_path"`
	Size              int64     `json:"size"`
	ModTime           time.Time `json:"mod_time"`
	Status            string    `json:"status,omitempty"`
	LastCheckedAt     int64     `json:"last_checked_at,omitempty"`
	LastRepairedAt    int64     `json:"last_repaired_at,omitempty"`
	LastRepairJobID   string    `json:"last_repair_job_id,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	LastRepairOutcome string    `json:"last_repair_outcome,omitempty"`
}

func (s *Server) registerHealthRoutes() {
	// Scan NZBs under RAW/output_dir (recursive)
	s.mux.HandleFunc("/api/v1/health/scan", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		root := strings.TrimSpace(s.Config().NgPost.OutputDir)
		if root == "" {
			root = "/host/inbox/nzb"
		}

		// If root doesn't exist, return empty list (not error) to keep UI friendly.
		st, err := os.Stat(root)
		if err != nil || !st.IsDir() {
			_ = json.NewEncoder(w).Encode(map[string]any{"root": root, "entries": []healthScanEntry{}})
			return
		}

		entries := make([]healthScanEntry, 0, 256)
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				// skip hidden folders, and the health backup folder
				if strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				if d.Name() == ".health-bak" {
					return filepath.SkipDir
				}
				return nil
			}
			name := strings.ToLower(d.Name())
			if !strings.HasSuffix(name, ".nzb") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rp, _ := filepath.Rel(root, p)
			entries = append(entries, healthScanEntry{Path: p, RelPath: rp, Size: info.Size(), ModTime: info.ModTime()})
			return nil
		})

		states := map[string]healthScanEntry{}
		totalCheckedNow := 0
		var lastFullRun int64
		var currentRunStart int64
		if s.jobs != nil && s.jobs.DB() != nil && s.jobs.DB().SQL != nil {
			db := s.jobs.DB().SQL
			rows, err := db.QueryContext(r.Context(), `SELECT path, status, COALESCE(last_checked_at,0), COALESCE(last_repaired_at,0), COALESCE(last_repair_job_id,''), COALESCE(last_error,'') FROM health_nzb_state`)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var st healthScanEntry
					if err := rows.Scan(&st.Path, &st.Status, &st.LastCheckedAt, &st.LastRepairedAt, &st.LastRepairJobID, &st.LastError); err == nil {
						states[st.Path] = st
					}
				}
			}
			_ = db.QueryRowContext(r.Context(), `SELECT COALESCE(run_started_at,0), COALESCE(last_run_completed_at,0) FROM health_scan_state WHERE id=1`).Scan(&currentRunStart, &lastFullRun)
		}

		for i := range entries {
			if st, ok := states[entries[i].Path]; ok {
				entries[i].Status = st.Status
				entries[i].LastCheckedAt = st.LastCheckedAt
				entries[i].LastRepairedAt = st.LastRepairedAt
				entries[i].LastRepairJobID = st.LastRepairJobID
				entries[i].LastError = st.LastError
				if currentRunStart > 0 && st.LastCheckedAt >= currentRunStart {
					totalCheckedNow++
				}
				if st.LastRepairJobID != "" && s.jobs != nil && s.jobs.DB() != nil {
					var outcome string
					_ = s.jobs.DB().SQL.QueryRowContext(r.Context(), `SELECT state FROM jobs WHERE id=?`, st.LastRepairJobID).Scan(&outcome)
					entries[i].LastRepairOutcome = outcome
				}
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"root":    root,
			"entries": entries,
			"summary": map[string]any{
				"total":                  len(entries),
				"checked_in_current_run": totalCheckedNow,
				"current_run_started_at": currentRunStart,
				"last_full_run_at":       lastFullRun,
			},
		})
	})

	// Enqueue a full health scan job
	s.mux.HandleFunc("/api/v1/jobs/enqueue/health-scan", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "jobs db not configured"})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		job, err := s.jobs.Enqueue(r.Context(), jobs.TypeHealthScan, map[string]string{})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(job)
	})

	// Enqueue a repair job for a specific NZB path
	s.mux.HandleFunc("/api/v1/jobs/enqueue/health-repair", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "jobs db not configured"})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var payload struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if strings.TrimSpace(payload.Path) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "path required"})
			return
		}
		job, err := s.jobs.Enqueue(r.Context(), jobs.TypeHealthRepair, payload)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(job)
	})
}
