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
  Path    string    `json:"path"`
  RelPath string    `json:"rel_path"`
  Size    int64     `json:"size"`
  ModTime time.Time `json:"mod_time"`
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

    _ = json.NewEncoder(w).Encode(map[string]any{"root": root, "entries": entries})
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
