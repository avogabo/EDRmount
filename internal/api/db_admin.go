package api

import (
	"encoding/json"
	"net/http"
	"os"
)

// POST /api/v1/db/reset
// Creates a marker file to reset ONLY the sqlite DB on next restart.
func (s *Server) handleDBReset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}
	// Marker lives in /config because it's bind-mounted and available on boot.
	marker := "/config/.reset-db"
	_ = os.WriteFile(marker, []byte("1\n"), 0o644)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "marker": marker, "note": "DB will be reset on next restart"})
}
