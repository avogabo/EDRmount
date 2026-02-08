package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gaby/EDRmount/internal/backup"
)

func (s *Server) registerBackupRoutes(dbPath string) {
	// List backups
	s.mux.HandleFunc("/api/v1/backups", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Config()
		items, err := backup.List(cfg.Backups.Dir)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"dir": cfg.Backups.Dir, "items": items})
	})

	// Backup now
	s.mux.HandleFunc("/api/v1/backups/run", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Config()
		path, err := backup.RunOnce(r.Context(), dbPath, cfg.Backups.Dir, cfg.Backups.CompressGZ)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		backup.Rotate(cfg.Backups.Dir, cfg.Backups.Keep)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": path, "ts": time.Now().Unix()})
	})

	// Restore
	s.mux.HandleFunc("/api/v1/backups/restore", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		cfg := s.Config()
		if req.Name == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "name required"})
			return
		}
		// prevent path traversal
		name := filepath.Base(req.Name)
		full := filepath.Join(cfg.Backups.Dir, name)
		if _, err := os.Stat(full); err != nil {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "backup not found"})
			return
		}
		if err := backup.RestoreFrom(r.Context(), full, dbPath); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "restored": name, "restarting": true})

		// Restart pattern A: exit after response so Docker restarts us.
		go func() {
			<-time.After(500 * time.Millisecond)
			os.Exit(0)
		}()
	})

	// Backups status: dir checks + last/next
	s.mux.HandleFunc("/api/v1/backups/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Config()
		dir := cfg.Backups.Dir
		st := map[string]any{
			"dir":         dir,
			"enabled":     cfg.Backups.Enabled,
			"every_mins":  cfg.Backups.EveryMins,
			"keep":        cfg.Backups.Keep,
			"compress_gz": cfg.Backups.CompressGZ,
		}
		if info, err := os.Stat(dir); err == nil {
			st["exists"] = true
			st["is_dir"] = info.IsDir()
		} else {
			st["exists"] = false
			st["error"] = err.Error()
		}
		// Best-effort write check
		writable := false
		if err := os.MkdirAll(dir, 0o755); err == nil {
			p := filepath.Join(dir, ".edrmount_write_test")
			if err := os.WriteFile(p, []byte("ok"), 0o644); err == nil {
				_ = os.Remove(p)
				writable = true
			}
		}
		st["writable"] = writable

		// Last/next based on actual files (works even after restart)
		items, err := backup.List(dir)
		if err == nil && len(items) > 0 {
			st["last_backup"] = items[0]
			if t, err := time.Parse(time.RFC3339, items[0].Time); err == nil {
				st["last_backup_unix"] = t.Unix()
				if cfg.Backups.Enabled && cfg.Backups.EveryMins > 0 {
					next := t.Add(time.Duration(cfg.Backups.EveryMins) * time.Minute)
					st["next_due"] = next.Format(time.RFC3339)
					st["overdue"] = time.Now().After(next)
				}
			}
		} else {
			st["last_backup"] = nil
			if cfg.Backups.Enabled && cfg.Backups.EveryMins > 0 {
				next := time.Now().Add(time.Duration(cfg.Backups.EveryMins) * time.Minute)
				st["next_due"] = next.Format(time.RFC3339)
				st["overdue"] = false
			}
		}

		_ = json.NewEncoder(w).Encode(st)
	})

	// Debug: expose effective backup config
	s.mux.HandleFunc("/api/v1/backups/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Config()
		_ = json.NewEncoder(w).Encode(cfg.Backups)
	})

	_ = context.Canceled
}
