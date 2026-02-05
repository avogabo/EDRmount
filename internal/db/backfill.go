package db

import (
	"database/sql"
	"fmt"

	"github.com/gaby/EDRmount/internal/subject"
)

func backfillFilenames(db *sql.DB) error {
	rows, err := db.Query(`SELECT import_id, idx, subject FROM nzb_files WHERE filename IS NULL OR filename = '' LIMIT 5000`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		importID string
		idx      int
		subj     string
	}
	items := make([]row, 0, 1024)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.importID, &r.idx, &r.subj); err != nil {
			continue
		}
		items = append(items, r)
	}
	for _, it := range items {
		fn, ok := subject.FilenameFromSubject(it.subj)
		if !ok || fn == "" {
			fn = fmt.Sprintf("file_%04d.bin", it.idx)
		}
		_, _ = db.Exec(`UPDATE nzb_files SET filename=? WHERE import_id=? AND idx=? AND (filename IS NULL OR filename = '')`, fn, it.importID, it.idx)
	}
	return nil
}
