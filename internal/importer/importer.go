package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/jobs"
	"github.com/gaby/EDRmount/internal/nzb"
	"github.com/gaby/EDRmount/internal/subject"
)

type Importer struct {
	jobs *jobs.Store
}

func New(j *jobs.Store) *Importer { return &Importer{jobs: j} }

func (i *Importer) ImportNZB(ctx context.Context, jobID string, path string) (files int, totalBytes int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	doc, err := nzb.Parse(f)
	if err != nil {
		return 0, 0, err
	}

	files = len(doc.Files)
	for _, nf := range doc.Files {
		for _, s := range nf.Segments {
			totalBytes += s.Bytes
		}
	}

	importID := jobID
	if importID == "" {
		importID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	groupsToJSON := func(groups []string) string {
		b, _ := json.Marshal(groups)
		return string(b)
	}

	// Persist import summary + per-file rows
	db := i.jobs.DB().SQL
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO nzb_imports(id,path,imported_at,files_count,total_bytes) VALUES(?,?,?,?,?)`,
		importID, path, now, files, totalBytes)
	if err != nil {
		return 0, 0, err
	}

	stmtFile, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO nzb_files(import_id,idx,subject,filename,poster,date,groups_json,segments_count,total_bytes) VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, 0, err
	}
	defer stmtFile.Close()
	stmtSeg, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO nzb_segments(import_id,file_idx,number,bytes,message_id) VALUES(?,?,?,?,?)`)
	if err != nil {
		return 0, 0, err
	}
	defer stmtSeg.Close()

	for idx, nf := range doc.Files {
		var fb int64
		for _, s := range nf.Segments {
			fb += s.Bytes
		}
		fn, ok := subject.FilenameFromSubject(nf.Subject)
		if !ok || fn == "" {
			fn = fmt.Sprintf("file_%04d.bin", idx)
		}
		_, err := stmtFile.ExecContext(ctx,
			importID, idx, nf.Subject, fn, nf.Poster, nf.Date, groupsToJSON(nf.Groups), len(nf.Segments), fb)
		if err != nil {
			return 0, 0, err
		}

		// segments
		for _, seg := range nf.Segments {
			mid := strings.TrimSpace(seg.ID)
			if mid == "" {
				continue
			}
			_, err := stmtSeg.ExecContext(ctx,
				importID, idx, seg.Number, seg.Bytes, mid)
			if err != nil {
				return 0, 0, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return files, totalBytes, nil
}
