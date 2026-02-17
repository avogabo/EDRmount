package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func (s *Server) registerFileBotRoutes() {
	s.mux.HandleFunc("/api/v1/filebot/license/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Config()
		bin := strings.TrimSpace(cfg.Rename.FileBot.Binary)
		if bin == "" {
			bin = "/usr/local/bin/filebot"
		}
		licensePath := strings.TrimSpace(cfg.Rename.FileBot.LicensePath)
		if licensePath == "" {
			licensePath = "/config/filebot/license.psm"
		}
		if _, err := os.Stat(licensePath); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "license file not found", "path": licensePath})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		// Do not execute `--license` here (can block on some setups).
		// This endpoint validates that binary and license file are present and callable.
		cmd := exec.CommandContext(ctx, bin, "-version")
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		resp := map[string]any{
			"ok":              err == nil,
			"binary":          bin,
			"path":            licensePath,
			"license_present": true,
			"output":          truncateOutput(out.String(), 2000),
		}
		if err != nil {
			resp["error"] = err.Error()
			w.WriteHeader(http.StatusBadRequest)
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
}

func truncateOutput(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + " ..."
}
