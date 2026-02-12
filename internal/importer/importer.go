package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/jobs"
	"github.com/gaby/EDRmount/internal/nzb"
	"github.com/gaby/EDRmount/internal/subject"
	"github.com/google/uuid"
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

	// Deduplicate by NZB path: if this path was already imported, skip creating a second import.
	var existingID string
	var existingFiles int
	var existingBytes int64
	if err := db.QueryRowContext(ctx, `SELECT id,files_count,total_bytes FROM nzb_imports WHERE path=? ORDER BY imported_at DESC LIMIT 1`, path).Scan(&existingID, &existingFiles, &existingBytes); err == nil {
		return existingFiles, existingBytes, nil
	}

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

	// Seed Manual tree from NZB path (idempotent):
	// /host/inbox/nzb/PELICULAS/1080/A/Avatar (2009).nzb ->
	// root/PELICULAS/1080/A/Avatar (2009) + manual_items for file_idx
	if err := seedManualFromNZB(ctx, tx, importID, path); err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return files, totalBytes, nil
}

func seedManualFromNZB(ctx context.Context, tx *sql.Tx, importID, nzbPath string) error {
	// already seeded somewhere in manual tree
	var exists int
	_ = tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM manual_items WHERE import_id=?`, importID).Scan(&exists)
	if exists > 0 {
		return nil
	}

	rel := strings.TrimPrefix(filepath.Clean(nzbPath), "/host/inbox/nzb/")
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" || rel == "." {
		return nil
	}
	base := strings.TrimSuffix(rel, filepath.Ext(rel))
	parts := []string{}
	for _, p := range strings.Split(filepath.ToSlash(base), "/") {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return nil
	}

	ensureDir := func(parent, name string) (string, error) {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM manual_dirs WHERE parent_id=? AND name=? LIMIT 1`, parent, name).Scan(&id)
		if err == nil && strings.TrimSpace(id) != "" {
			return id, nil
		}
		id = uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO manual_dirs(id,parent_id,name) VALUES(?,?,?)`, id, parent, name); err != nil {
			return "", err
		}
		return id, nil
	}

	leaf := "root"
	for _, name := range parts {
		nextID, err := ensureDir(leaf, name)
		if err != nil {
			return err
		}
		leaf = nextID
	}

	rows, err := tx.QueryContext(ctx, `SELECT idx, COALESCE(filename,'') FROM nzb_files WHERE import_id=? ORDER BY idx`, importID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var idx int
		var fn string
		if err := rows.Scan(&idx, &fn); err != nil {
			return err
		}
		if strings.TrimSpace(fn) == "" {
			fn = fmt.Sprintf("file_%04d.bin", idx)
		}
		itemID := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO manual_items(id,dir_id,label,import_id,file_idx) VALUES(?,?,?,?,?)`, itemID, leaf, fn, importID, idx); err != nil {
			return err
		}
	}
	return nil
}
