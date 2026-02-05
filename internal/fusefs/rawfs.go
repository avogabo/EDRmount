package fusefs

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
	"github.com/gaby/EDRmount/internal/streamer"
	"github.com/gaby/EDRmount/internal/subject"
)

// RawFS exposes a read-only filesystem:
//   /raw/<importId>/<filename>
// where <filename> comes from NZB subject parsing (best-effort).

type RawFS struct {
	Cfg  config.Config
	Jobs *jobs.Store
}

func (r *RawFS) Root() (fs.Node, error) {
	return &rawRoot{fs: r}, nil
}

type rawRoot struct{ fs *RawFS }

func (n *rawRoot) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0o555
	return nil
}

func (n *rawRoot) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	return []fuse.Dirent{{Name: "raw", Type: fuse.DT_Dir}}, nil
}

func (n *rawRoot) Lookup(ctx context.Context, name string) (fs.Node, error) {
	if name == "raw" {
		return &rawImportsDir{fs: n.fs}, nil
	}
	return nil, fuse.ENOENT
}

type rawImportsDir struct{ fs *RawFS }

func (n *rawImportsDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0o555
	return nil
}

func (n *rawImportsDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	rows, err := n.fs.Jobs.DB().SQL.QueryContext(ctx, `SELECT id FROM nzb_imports ORDER BY imported_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]fuse.Dirent, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		out = append(out, fuse.Dirent{Name: id, Type: fuse.DT_Dir})
	}
	return out, nil
}

func (n *rawImportsDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	// validate import exists
	row := n.fs.Jobs.DB().SQL.QueryRowContext(ctx, `SELECT COUNT(1) FROM nzb_imports WHERE id=?`, name)
	var c int
	if err := row.Scan(&c); err != nil {
		return nil, fuse.ENOENT
	}
	if c == 0 {
		return nil, fuse.ENOENT
	}
	return &rawImportDir{fs: n.fs, importID: name}, nil
}

type fileEntry struct {
	Idx     int
	Subject string
	Bytes   int64
	Name    string
}

type rawImportDir struct {
	fs       *RawFS
	importID string
}

func (n *rawImportDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0o555
	return nil
}

func (n *rawImportDir) listFiles(ctx context.Context) ([]fileEntry, error) {
	rows, err := n.fs.Jobs.DB().SQL.QueryContext(ctx, `SELECT idx,filename,subject,total_bytes FROM nzb_files WHERE import_id=? ORDER BY idx ASC`, n.importID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]fileEntry, 0)
	seen := map[string]int{}
	for rows.Next() {
		var e fileEntry
		var dbfn sql.NullString
		if err := rows.Scan(&e.Idx, &dbfn, &e.Subject, &e.Bytes); err != nil {
			continue
		}
		base := ""
		if dbfn.Valid {
			base = dbfn.String
		} else {
			f2, ok := subject.FilenameFromSubject(e.Subject)
			if ok {
				base = f2
			}
		}
		if base == "" {
			base = fmt.Sprintf("file_%04d.bin", e.Idx)
		}

		name := base
		seen[base]++
		if seen[base] > 1 {
			name = withSuffixBeforeExt(base, seen[base])
		}
		e.Name = name
		out = append(out, e)
	}
	return out, nil
}

func (n *rawImportDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	files, err := n.listFiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]fuse.Dirent, 0, len(files))
	for _, f := range files {
		out = append(out, fuse.Dirent{Name: f.Name, Type: fuse.DT_File})
	}
	return out, nil
}

func (n *rawImportDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	files, err := n.listFiles(ctx)
	if err != nil {
		return nil, fuse.ENOENT
	}
	for _, f := range files {
		if f.Name == name {
			return &rawFile{fs: n.fs, importID: n.importID, fileIdx: f.Idx, name: f.Name, size: f.Bytes}, nil
		}
	}
	return nil, fuse.ENOENT
}

type rawFile struct {
	fs       *RawFS
	importID string
	fileIdx  int
	name     string
	size     int64
}

func (n *rawFile) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = 0o444
	a.Size = uint64(max64(0, n.size))
	a.Mtime = time.Now()
	return nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (n *rawFile) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	if req.Offset < 0 {
		return fuse.EIO
	}
	if n.size <= 0 {
		return fuse.EIO
	}
	start := int64(req.Offset)
	end := start + int64(req.Size) - 1
	if start >= n.size {
		resp.Data = nil
		return nil
	}
	if end >= n.size {
		end = n.size - 1
	}

	st := streamer.New(n.fs.Cfg.Download, n.fs.Jobs, n.fs.Cfg.Paths.CacheDir, n.fs.Cfg.Paths.CacheMaxBytes)
	buf := &bytes.Buffer{}
	if err := st.StreamRange(ctx, n.importID, n.fileIdx, n.name, start, end, buf, n.fs.Cfg.Download.PrefetchSegments); err != nil {
		log.Printf("fuse read error import=%s fileIdx=%d: %v", n.importID, n.fileIdx, err)
		return fuse.EIO
	}
	resp.Data = buf.Bytes()
	return nil
}

// Ensure interfaces
var _ fs.FS = (*RawFS)(nil)
var _ fs.Node = (*rawRoot)(nil)
var _ fs.HandleReadDirAller = (*rawRoot)(nil)
var _ fs.NodeStringLookuper = (*rawRoot)(nil)

var _ fs.Node = (*rawImportsDir)(nil)
var _ fs.HandleReadDirAller = (*rawImportsDir)(nil)
var _ fs.NodeStringLookuper = (*rawImportsDir)(nil)

var _ fs.Node = (*rawImportDir)(nil)
var _ fs.HandleReadDirAller = (*rawImportDir)(nil)
var _ fs.NodeStringLookuper = (*rawImportDir)(nil)

var _ fs.Node = (*rawFile)(nil)
var _ fs.HandleReader = (*rawFile)(nil)

// Helpers for Windows-incompatible names (just in case)
func safeName(s string) string {
	s = filepath.Base(s)
	s = strings.ReplaceAll(s, "/", "_")
	return s
}

func withSuffixBeforeExt(name string, n int) string {
	if n <= 1 {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if ext == "" {
		return fmt.Sprintf("%s__%d", base, n)
	}
	return fmt.Sprintf("%s__%d%s", base, n, ext)
}

// avoid unused imports in case of future expansion
var _ = sql.ErrNoRows
var _ = safeName
