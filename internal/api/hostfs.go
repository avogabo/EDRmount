package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type hostEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

func (s *Server) registerHostFSRoutes() {
	// Upload a file into host root (write).
	// POST multipart/form-data with fields:
	// - path: directory (relative inside host root, like /inbox/nzb)
	// - file: file content
	s.mux.HandleFunc("/api/v1/hostfs/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Config()
		root := cfg.Paths.HostRoot
		if root == "" {
			root = "/host"
		}
		// Stream upload; keep form memory modest.
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		base := strings.TrimSpace(r.FormValue("path"))
		if base == "" {
			base = "/"
		}
		base = filepath.Clean("/" + strings.TrimPrefix(base, "/"))

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

		fullDir := filepath.Join(root, base)
		fullDir = filepath.Clean(fullDir)
		rootClean := filepath.Clean(root)
		if fullDir != rootClean && !strings.HasPrefix(fullDir, rootClean+string(os.PathSeparator)) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "path outside host root"})
			return
		}
		if err := os.MkdirAll(fullDir, 0o755); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		finalFull := filepath.Join(fullDir, name)
		finalFull = filepath.Clean(finalFull)
		if finalFull != rootClean && !strings.HasPrefix(finalFull, rootClean+string(os.PathSeparator)) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "path outside host root"})
			return
		}

		tmp := finalFull + ".tmp"
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
		_ = os.Remove(finalFull)
		if err := os.Rename(tmp, finalFull); err != nil {
			_ = os.Remove(tmp)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		rel := strings.TrimPrefix(finalFull, rootClean)
		rel = strings.ReplaceAll(rel, "\\", "/")
		if rel == "" {
			rel = "/"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": rel, "filename": name})
	})

	// List host paths (read-only).
	// This is intentionally limited to inside cfg.Paths.HostRoot (usually "/host").
	s.mux.HandleFunc("/api/v1/hostfs/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Config()
		root := cfg.Paths.HostRoot
		if root == "" {
			root = "/host"
		}

		p := strings.TrimSpace(r.URL.Query().Get("path"))
		if p == "" {
			p = "/"
		}
		// Force absolute inside root.
		p = filepath.Clean("/" + strings.TrimPrefix(p, "/"))
		full := filepath.Join(root, p)
		full = filepath.Clean(full)

		// Ensure the resolved path stays within root.
		rootClean := filepath.Clean(root)
		if full != rootClean && !strings.HasPrefix(full, rootClean+string(os.PathSeparator)) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "path outside host root"})
			return
		}

		ents, err := os.ReadDir(full)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		out := make([]hostEntry, 0, len(ents))
		for _, e := range ents {
			info, err := e.Info()
			if err != nil {
				continue
			}
			rel := filepath.Join(p, e.Name())
			// normalize for UI
			rel = strings.ReplaceAll(rel, "\\", "/")
			out = append(out, hostEntry{
				Name:    e.Name(),
				Path:    rel,
				IsDir:   e.IsDir(),
				Size:    info.Size(),
				ModTime: info.ModTime().UTC().Format(time.RFC3339),
			})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].IsDir != out[j].IsDir {
				return out[i].IsDir // dirs first
			}
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})

		_ = json.NewEncoder(w).Encode(map[string]any{
			"root":    root,
			"path":    p,
			"entries": out,
		})
	})

	// Mkdir inside host root.
	s.mux.HandleFunc("/api/v1/hostfs/mkdir", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Config()
		root := cfg.Paths.HostRoot
		if root == "" {
			root = "/host"
		}
		var req struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		base := filepath.Clean("/" + strings.TrimPrefix(strings.TrimSpace(req.Path), "/"))
		name := strings.TrimSpace(req.Name)
		if name == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "name required"})
			return
		}
		// basic sanitization
		name = strings.ReplaceAll(name, "\\", "-")
		name = strings.ReplaceAll(name, "/", "-")

		full := filepath.Join(root, base, name)
		full = filepath.Clean(full)
		rootClean := filepath.Clean(root)
		if full != rootClean && !strings.HasPrefix(full, rootClean+string(os.PathSeparator)) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "path outside host root"})
			return
		}
		if err := os.MkdirAll(full, 0o755); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		// return the created relative path
		rel := strings.TrimPrefix(full, rootClean)
		rel = strings.ReplaceAll(rel, "\\", "/")
		if rel == "" {
			rel = "/"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": rel})
	})
}
