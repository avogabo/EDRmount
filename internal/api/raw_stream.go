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

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	st := streamer.New(s.cfg.Download, s.jobs, s.cfg.Paths.CacheDir, s.cfg.Paths.CacheMaxBytes)

	// Find matching file_idx by subject-derived filename and also get total bytes.
	rows, err := s.jobs.DB().SQL.QueryContext(ctx, `SELECT idx,subject,total_bytes FROM nzb_files WHERE import_id=? ORDER BY idx ASC`, importID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	fileIdx := -1
	var size int64
	for rows.Next() {
		var idx int
		var subj string
		var bytes int64
		_ = rows.Scan(&idx, &subj, &bytes)
		fn, ok := subject.FilenameFromSubject(subj)
		if ok && fn == filename {
			fileIdx = idx
			size = bytes
			break
		}
	}
	if fileIdx < 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "file not found in import"})
		return
	}
	if size <= 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid file size"})
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Accept-Ranges", "bytes")

	mr, perr := parseRanges(r.Header.Get("Range"), size)
	if perr != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}

	// No Range: ensure full file cached and serve it.
	if mr == nil {
		localPath, err := st.EnsureFile(ctx, importID, fileIdx, filename)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		f, err := os.Open(localPath)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer f.Close()
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
		return
	}

	// Range: lazy streaming via per-segment cache (does not require full file cache).
	if len(mr.Ranges) == 1 {
		br := mr.Ranges[0]
		length := (br.End - br.Start) + 1
		w.Header().Set("Content-Length", fmt.Sprintf("%d", length))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", br.Start, br.End, size))
		w.WriteHeader(http.StatusPartialContent)
		prefetchSegs := s.cfg.Download.PrefetchSegments
		if prefetchSegs < 0 {
			prefetchSegs = 0
		}
		_ = st.StreamRange(ctx, importID, fileIdx, filename, br.Start, br.End, w, prefetchSegs)
		return
	}

	// Multi-range: we currently require full-file cache (for simplicity).
	localPath, err := st.EnsureFile(ctx, importID, fileIdx, filename)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	f, err := os.Open(localPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	_ = serveMultiRangeFromFile(w, r, f, size, "application/octet-stream", mr)
}
