package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gaby/EDRmount/internal/jobs"
)

func (s *Server) registerManualMediaUploadRoutes() {
	// Manual media upload:
	// - stores file under /cache (NOT watched)
	// - enqueues an upload job pointing to that cache path
	// This prevents duplicate enqueues when the media watch folder is enabled.
	// POST multipart/form-data:
	// - file: media content
	s.mux.HandleFunc("/api/v1/upload/media/manual", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "jobs db not configured"})
			return
		}

		cfg := s.Config()
		cacheDir := strings.TrimSpace(cfg.Paths.CacheDir)
		if cacheDir == "" {
			cacheDir = "/cache"
		}
		dir := filepath.Join(cacheDir, "manual-media")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		if err := r.ParseMultipartForm(32 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer f.Close()

		name := strings.TrimSpace(hdr.Filename)
		name = strings.ReplaceAll(name, "\\", "-")
		name = strings.ReplaceAll(name, "/", "-")
		if name == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "filename required"})
			return
		}

		final := uniquePath(filepath.Join(dir, name))
		tmp := final + ".tmp"
		_ = os.Remove(tmp)
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_, copyErr := io.Copy(out, f)
		_ = out.Close()
		if copyErr != nil {
			_ = os.Remove(tmp)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": copyErr.Error()})
			return
		}
		if err := os.Rename(tmp, final); err != nil {
			_ = os.Remove(tmp)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		job, err := s.jobs.Enqueue(r.Context(), jobs.TypeUpload, map[string]string{"path": final})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": final, "job": job})
	})
}
