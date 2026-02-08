package fusefs

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
)

// AutoVirtualPathsForImport returns the virtual library-auto paths (relative to the mount root)
// for MKV payloads of a given import.
//
// This uses the same path-building logic as the LibraryFS.
func AutoVirtualPathsForImport(ctx context.Context, cfg config.Config, st *jobs.Store, importID string) ([]string, error) {
	if st == nil {
		return nil, fmt.Errorf("jobs store required")
	}
	lfs := &LibraryFS{Cfg: cfg, Jobs: st}
	// ensure resolver init
	_, _ = lfs.Root()
	ld := &libDir{fs: lfs, rel: ""}

	rows, err := st.DB().SQL.QueryContext(ctx, `SELECT idx, filename, subject, total_bytes FROM nzb_files WHERE import_id=? ORDER BY idx`, importID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]string, 0)
	seen := map[string]bool{}
	for rows.Next() {
		var idx int
		var fn sql.NullString
		var subj string
		var bytes int64
		if err := rows.Scan(&idx, &fn, &subj, &bytes); err != nil {
			continue
		}
		name := ""
		if fn.Valid {
			name = fn.String
		}
		if strings.TrimSpace(name) == "" {
			name = filepath.Base(subj)
		}
		if strings.ToLower(filepath.Ext(name)) != ".mkv" {
			continue
		}

		p := ld.buildPath(ctx, libRow{ImportID: importID, Idx: idx, Filename: name, Bytes: bytes})
		p = filepath.Clean(p)
		p = strings.TrimPrefix(p, string(filepath.Separator))
		if p == "." || p == "" {
			continue
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}
