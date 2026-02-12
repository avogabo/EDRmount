package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type manualDir struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
}

type manualItem struct {
	ID       string `json:"id"`
	DirID    string `json:"dir_id"`
	Label    string `json:"label"`
	ImportID string `json:"import_id"`
	FileIdx  int    `json:"file_idx"`
	Bytes    int64  `json:"bytes"`
	Filename string `json:"filename"`
}

func (s *Server) registerManualLibraryRoutes() {
	// Path breadcrumb (root -> current)
	s.mux.HandleFunc("/api/v1/manual/path", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "db not configured"})
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		dir := strings.TrimSpace(r.URL.Query().Get("dir_id"))
		if dir == "" {
			dir = "root"
		}

		// Build [current..root] then reverse.
		out := make([]manualDir, 0, 8)
		if dir == "root" {
			out = append(out, manualDir{ID: "root", ParentID: "", Name: "root"})
			_ = json.NewEncoder(w).Encode(out)
			return
		}

		cur := dir
		seen := map[string]bool{}
		for {
			if cur == "" || cur == "root" {
				break
			}
			if seen[cur] {
				// Cycle protection
				break
			}
			seen[cur] = true

			row := s.jobs.DB().SQL.QueryRowContext(r.Context(), `SELECT id,parent_id,name FROM manual_dirs WHERE id=?`, cur)
			var d manualDir
			if err := row.Scan(&d.ID, &d.ParentID, &d.Name); err != nil {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			out = append(out, d)
			cur = d.ParentID
		}
		// Ensure root at the beginning
		out = append(out, manualDir{ID: "root", ParentID: "", Name: "root"})

		// reverse
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	// All dirs (for UI pickers)
	s.mux.HandleFunc("/api/v1/manual/dirs/all", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "db not configured"})
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		rows, err := s.jobs.DB().SQL.QueryContext(r.Context(), `SELECT id,parent_id,name FROM manual_dirs ORDER BY name`)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		out := make([]manualDir, 0)
		for rows.Next() {
			var d manualDir
			if err := rows.Scan(&d.ID, &d.ParentID, &d.Name); err != nil {
				continue
			}
			out = append(out, d)
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	// Dirs list/create
	s.mux.HandleFunc("/api/v1/manual/dirs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "db not configured"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			parent := r.URL.Query().Get("parent_id")
			if parent == "" {
				parent = "root"
			}
			rows, err := s.jobs.DB().SQL.QueryContext(r.Context(), `SELECT id,parent_id,name FROM manual_dirs WHERE parent_id=? AND id<>'root' ORDER BY name`, parent)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			defer rows.Close()
			out := make([]manualDir, 0)
			for rows.Next() {
				var d manualDir
				if err := rows.Scan(&d.ID, &d.ParentID, &d.Name); err != nil {
					continue
				}
				out = append(out, d)
			}
			_ = json.NewEncoder(w).Encode(out)
		case http.MethodPost:
			var req struct {
				ParentID string `json:"parent_id"`
				Name     string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			if strings.TrimSpace(req.Name) == "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "name required"})
				return
			}
			if req.ParentID == "" {
				req.ParentID = "root"
			}
			id := uuid.NewString()
			_, err := s.jobs.DB().SQL.ExecContext(r.Context(), `INSERT INTO manual_dirs(id,parent_id,name) VALUES(?,?,?)`, id, req.ParentID, req.Name)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(manualDir{ID: id, ParentID: req.ParentID, Name: req.Name})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Dir update/delete
	s.mux.HandleFunc("/api/v1/manual/dirs/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "db not configured"})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/manual/dirs/")
		id = strings.Trim(id, "/")
		if id == "" || id == "root" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid dir id"})
			return
		}
		switch r.Method {
		case http.MethodPut:
			var req struct {
				Name     string `json:"name"`
				ParentID string `json:"parent_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			// Fetch existing
			row := s.jobs.DB().SQL.QueryRowContext(r.Context(), `SELECT parent_id,name FROM manual_dirs WHERE id=?`, id)
			var parent, name string
			if err := row.Scan(&parent, &name); err != nil {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			if strings.TrimSpace(req.Name) != "" {
				name = req.Name
			}
			if strings.TrimSpace(req.ParentID) != "" {
				parent = req.ParentID
			}
			_, err := s.jobs.DB().SQL.ExecContext(r.Context(), `UPDATE manual_dirs SET parent_id=?, name=? WHERE id=?`, parent, name, id)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(manualDir{ID: id, ParentID: parent, Name: name})
		case http.MethodDelete:
			// refuse delete if has children
			row := s.jobs.DB().SQL.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(1) FROM manual_dirs WHERE parent_id=?)+(SELECT COUNT(1) FROM manual_items WHERE dir_id=?)`, id, id)
			var c int
			_ = row.Scan(&c)
			if c > 0 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "folder not empty"})
				return
			}
			_, err := s.jobs.DB().SQL.ExecContext(r.Context(), `DELETE FROM manual_dirs WHERE id=?`, id)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "deleted": id, "ts": time.Now().Unix()})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Items list/create
	s.mux.HandleFunc("/api/v1/manual/items", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "db not configured"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			dir := r.URL.Query().Get("dir_id")
			if dir == "" {
				dir = "root"
			}
			q := `
				SELECT i.id, i.dir_id, i.label, i.import_id, i.file_idx, f.total_bytes, f.filename
				FROM manual_items i
				JOIN nzb_files f ON f.import_id=i.import_id AND f.idx=i.file_idx
				WHERE i.dir_id=?
				ORDER BY i.label
			`
			rows, err := s.jobs.DB().SQL.QueryContext(r.Context(), q, dir)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			defer rows.Close()
			out := make([]manualItem, 0)
			for rows.Next() {
				var it manualItem
				var fn sql.NullString
				if err := rows.Scan(&it.ID, &it.DirID, &it.Label, &it.ImportID, &it.FileIdx, &it.Bytes, &fn); err != nil {
					continue
				}
				if fn.Valid {
					it.Filename = fn.String
				}
				out = append(out, it)
			}
			_ = json.NewEncoder(w).Encode(out)
		case http.MethodPost:
			var req struct {
				DirID    string `json:"dir_id"`
				Label    string `json:"label"`
				ImportID string `json:"import_id"`
				FileIdx  int    `json:"file_idx"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			if req.DirID == "" {
				req.DirID = "root"
			}
			if strings.TrimSpace(req.ImportID) == "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "import_id required"})
				return
			}
			id := uuid.NewString()
			label := req.Label
			if strings.TrimSpace(label) == "" {
				label = "(item)"
			}
			_, err := s.jobs.DB().SQL.ExecContext(r.Context(), `INSERT INTO manual_items(id,dir_id,label,import_id,file_idx) VALUES(?,?,?,?,?)`, id, req.DirID, label, req.ImportID, req.FileIdx)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(manualItem{ID: id, DirID: req.DirID, Label: label, ImportID: req.ImportID, FileIdx: req.FileIdx})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Item update/delete
	s.mux.HandleFunc("/api/v1/manual/items/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "db not configured"})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/manual/items/")
		id = strings.Trim(id, "/")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "id required"})
			return
		}
		switch r.Method {
		case http.MethodPut:
			var req struct {
				DirID string `json:"dir_id"`
				Label string `json:"label"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			row := s.jobs.DB().SQL.QueryRowContext(r.Context(), `SELECT dir_id,label,import_id,file_idx FROM manual_items WHERE id=?`, id)
			var dir, label, imp string
			var idx int
			if err := row.Scan(&dir, &label, &imp, &idx); err != nil {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			if strings.TrimSpace(req.DirID) != "" {
				dir = req.DirID
			}
			if strings.TrimSpace(req.Label) != "" {
				label = req.Label
			}
			_, err := s.jobs.DB().SQL.ExecContext(r.Context(), `UPDATE manual_items SET dir_id=?, label=? WHERE id=?`, dir, label, id)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(manualItem{ID: id, DirID: dir, Label: label, ImportID: imp, FileIdx: idx})
		case http.MethodDelete:
			_, err := s.jobs.DB().SQL.ExecContext(r.Context(), `DELETE FROM manual_items WHERE id=?`, id)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "deleted": id, "ts": time.Now().Unix()})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}
