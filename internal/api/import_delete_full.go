package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Server) registerImportDeleteRoutes() {
	// POST /api/v1/catalog/imports/delete_full { id }
	// Deletes import from DB AND moves NZB + related PAR2 to /host/inbox/.trash (best-effort).
	s.mux.HandleFunc("/api/v1/catalog/imports/delete_full", func(w http.ResponseWriter, r *http.Request) {
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
		id := strings.TrimSpace(req.ID)
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "id required"})
			return
		}

		cfg := s.Config()
		nzbRoot := strings.TrimSpace(cfg.Watch.NZB.Dir)
		if nzbRoot == "" {
			nzbRoot = strings.TrimSpace(cfg.Paths.NzbInbox)
		}
		if nzbRoot == "" {
			nzbRoot = "/host/inbox/nzb"
		}
		nzbRoot = filepath.Clean(nzbRoot)

		parRoot := strings.TrimSpace(cfg.Upload.Par.Dir)
		if parRoot == "" {
			parRoot = "/host/inbox/par2"
		}
		parRoot = filepath.Clean(parRoot)

		trashRoot := filepath.Join("/host", "inbox", ".trash")
		trashRoot = filepath.Clean(trashRoot)

		// Look up the NZB path before we delete it from DB.
		var nzbPath string
		row := s.jobs.DB().SQL.QueryRowContext(r.Context(), `SELECT path FROM nzb_imports WHERE id=?`, id)
		if err := row.Scan(&nzbPath); err != nil {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "import not found"})
			return
		}
		nzbPath = filepath.Clean(nzbPath)

		// Move NZB to trash (best-effort)
		movedNZB := ""
		if strings.HasPrefix(nzbPath, nzbRoot+string(filepath.Separator)) || nzbPath == nzbRoot {
			rel, _ := filepath.Rel(nzbRoot, nzbPath)
			movedNZB, _ = moveToTrash(nzbPath, filepath.Join(trashRoot, "nzb"), rel)
		} else {
			// If it's outside nzbRoot, still trash it under a flat name.
			movedNZB, _ = moveToTrash(nzbPath, filepath.Join(trashRoot, "nzb"), filepath.Base(nzbPath))
		}

		// Move matching PAR2 files (best-effort)
		// PAR2 keep directory mirrors the NZB directory (relative to nzbRoot).
		parMoved := 0
		baseNoExt := strings.TrimSuffix(filepath.Base(nzbPath), filepath.Ext(nzbPath))
		nzbDir := filepath.Dir(nzbPath)
		relDir := ""
		if strings.HasPrefix(nzbDir, nzbRoot+string(filepath.Separator)) || nzbDir == nzbRoot {
			relDir, _ = filepath.Rel(nzbRoot, nzbDir)
		}
		parDir := filepath.Join(parRoot, relDir)
		if entries, err := os.ReadDir(parDir); err == nil {
			for _, e := range entries {
				name := e.Name()
				low := strings.ToLower(name)
				if !strings.HasSuffix(low, ".par2") {
					continue
				}
				if !strings.HasPrefix(name, baseNoExt) {
					continue
				}
				src := filepath.Join(parDir, name)
				relP := filepath.Join(relDir, name)
				if _, err := moveToTrash(src, filepath.Join(trashRoot, "par2"), relP); err == nil {
					parMoved++
				}
			}
		}

		// Finally delete DB rows (global)
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
		for _, q := range stmts {
			if _, err := tx.ExecContext(r.Context(), q, id); err != nil {
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

		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "trashed_nzb": movedNZB, "trashed_par2": parMoved})
	})
}

func moveToTrash(src, trashBase, rel string) (string, error) {
	src = filepath.Clean(src)
	if strings.TrimSpace(src) == "" {
		return "", errors.New("src required")
	}
	if _, err := os.Stat(src); err != nil {
		return "", err
	}
	if rel == "" {
		rel = filepath.Base(src)
	}
	rel = strings.TrimPrefix(rel, string(filepath.Separator))
	rel = filepath.Clean(rel)

	stamp := time.Now().Format("20060102-150405")
	dst := filepath.Join(trashBase, stamp, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}

	// Best effort atomic move; fallback to copy+remove.
	if err := os.Rename(src, dst); err == nil {
		return dst, nil
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	if err := copyFileLocal(src, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	_ = os.Remove(src)
	return dst, nil
}

func copyFileLocal(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return out.Close()
}
