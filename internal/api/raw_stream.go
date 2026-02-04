package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/streamer"
	"github.com/gaby/EDRmount/internal/subject"
)

func (s *Server) handleRawFileStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/raw/imports/")
	parts := strings.SplitN(path, "/files/", 2)
	if len(parts) != 2 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	importID := strings.Trim(parts[0], "/")
	filename := filepath.Base(parts[1])
	if importID == "" || filename == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Find matching file_idx by subject-derived filename
	rows, err := s.jobs.DB().SQL.QueryContext(r.Context(), `SELECT idx,subject FROM nzb_files WHERE import_id=? ORDER BY idx ASC`, importID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	fileIdx := -1
	for rows.Next() {
		var idx int
		var subj string
		_ = rows.Scan(&idx, &subj)
		fn, ok := subject.FilenameFromSubject(subj)
		if ok && fn == filename {
			fileIdx = idx
			break
		}
	}
	if fileIdx < 0 {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "file not found in import"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	st := streamer.New(s.cfg.Download, s.jobs, s.cfg.Paths.CacheDir)
	localPath, err := st.EnsureFile(ctx, importID, fileIdx, filename)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Serve bytes (with optional Range)
	f, err := os.Open(localPath)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	if fi == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "stat failed"})
		return
	}
	size := fi.Size()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Accept-Ranges", "bytes")

	br, perr := parseRange(r.Header.Get("Range"), size)
	if perr != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if br == nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
		return
	}

	length := (br.End - br.Start) + 1
	if _, err := f.Seek(br.Start, 0); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", length))
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", br.Start, br.End, size))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = io.CopyN(w, f, length)
}
