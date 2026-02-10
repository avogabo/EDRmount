package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// registerManualImportRoutes implements "manual import" helpers that are safe
// even when watch folders are enabled (they write into the watched inbox).
func (s *Server) registerManualImportRoutes() {
	// Upload an NZB and drop it into the watched NZB inbox (cfg.Watch.NZB.Dir).
	// This is the preferred manual workflow: UI uploads here, then the watcher
	// will pick it up and run the normal import pipeline.
	// POST multipart/form-data with field:
	// - file: .nzb content
	s.mux.HandleFunc("/api/v1/import/nzb/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		cfg := s.Config()
		dir := strings.TrimSpace(cfg.Watch.NZB.Dir)
		if dir == "" {
			dir = strings.TrimSpace(cfg.Paths.NzbInbox)
		}
		if dir == "" {
			dir = "/host/inbox/nzb"
		}
		dir = filepath.Clean(dir)

		// Keep form memory modest.
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
		if !strings.HasSuffix(strings.ToLower(name), ".nzb") {
			// Be strict to avoid surprises.
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "only .nzb files allowed"})
			return
		}

		if err := os.MkdirAll(dir, 0o755); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Ensure we don't overwrite an existing file; add suffix if needed.
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

		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": final})
	})
}

func uniquePath(p string) string {
	// If file does not exist, use it.
	if _, err := os.Stat(p); err != nil {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(filepath.Base(p), ext)
	dir := filepath.Dir(p)
	for i := 2; i < 1000; i++ {
		cand := filepath.Join(dir, base+"_"+itoa(i)+ext)
		if _, err := os.Stat(cand); err != nil {
			return cand
		}
	}
	// fallback: always return original (last resort)
	return p
}

func itoa(i int) string {
	// tiny helper to avoid fmt import in hot path
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [32]byte
	n := 0
	for i > 0 {
		b[n] = byte('0' + (i % 10))
		n++
		i /= 10
	}
	if neg {
		b[n] = '-'
		n++
	}
	// reverse
	for j := 0; j < n/2; j++ {
		b[j], b[n-1-j] = b[n-1-j], b[j]
	}
	return string(b[:n])
}
