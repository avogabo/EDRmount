package fusefs

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
	"github.com/gaby/EDRmount/internal/streamer"
)

// ManualFS exposes a read-only filesystem that is editable via UI (DB-backed).
//
// Layout:
//   /Imports/<importId>/<filename>  (auto, mirrors imports)
//   /Folders/<user-folders...>      (custom virtual folders)

type ManualFS struct {
	Cfg  config.Config
	Jobs *jobs.Store
}

func (m *ManualFS) Root() (fs.Node, error) { return &manualRoot{fs: m}, nil }

type manualRoot struct{ fs *ManualFS }

func (n *manualRoot) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0o555
	return nil
}

func (n *manualRoot) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	return []fuse.Dirent{
		{Name: "Imports", Type: fuse.DT_Dir},
		{Name: "Folders", Type: fuse.DT_Dir},
	}, nil
}

func (n *manualRoot) Lookup(ctx context.Context, name string) (fs.Node, error) {
	switch name {
	case "Imports":
		return &manualImportsDir{fs: n.fs}, nil
	case "Folders":
		return &manualFoldersDir{fs: n.fs, dirID: "root"}, nil
	default:
		return nil, fuse.ENOENT
	}
}

// ---------------- Imports tree ----------------

type manualImportsDir struct{ fs *ManualFS }

func (n *manualImportsDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0o555
	return nil
}

func (n *manualImportsDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
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

func (n *manualImportsDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	row := n.fs.Jobs.DB().SQL.QueryRowContext(ctx, `SELECT COUNT(1) FROM nzb_imports WHERE id=?`, name)
	var c int
	if err := row.Scan(&c); err != nil || c == 0 {
		return nil, fuse.ENOENT
	}
	return &manualImportDir{fs: n.fs, importID: name}, nil
}

type manualImportDir struct {
	fs       *ManualFS
	importID string
}

func (n *manualImportDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0o555
	return nil
}

type impFileRow struct {
	Idx      int
	Filename string
	Bytes    int64
}

func (n *manualImportDir) list(ctx context.Context) ([]impFileRow, error) {
	rows, err := n.fs.Jobs.DB().SQL.QueryContext(ctx, `SELECT idx, filename, subject, total_bytes FROM nzb_files WHERE import_id=? ORDER BY idx`, n.importID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]impFileRow, 0)
	seen := map[string]int{}
	for rows.Next() {
		var r impFileRow
		var fn sql.NullString
		var subj string
		if err := rows.Scan(&r.Idx, &fn, &subj, &r.Bytes); err != nil {
			continue
		}
		name := ""
		if fn.Valid {
			name = fn.String
		}
		if name == "" {
			name = filepath.Base(subj)
		}
		if name == "" {
			name = fmt.Sprintf("file_%04d.bin", r.Idx)
		}
		seen[name]++
		if seen[name] > 1 {
			name = withSuffixBeforeExt(name, seen[name])
		}
		r.Filename = name
		out = append(out, r)
	}
	return out, nil
}

func (n *manualImportDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	items, err := n.list(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]fuse.Dirent, 0, len(items))
	for _, it := range items {
		out = append(out, fuse.Dirent{Name: it.Filename, Type: fuse.DT_File})
	}
	return out, nil
}

func (n *manualImportDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	items, err := n.list(ctx)
	if err != nil {
		return nil, fuse.ENOENT
	}
	for _, it := range items {
		if it.Filename == name {
			// We need underlying real filename for streamer. Use DB filename if present, else this name.
			real := name
			row := n.fs.Jobs.DB().SQL.QueryRowContext(ctx, `SELECT filename FROM nzb_files WHERE import_id=? AND idx=?`, n.importID, it.Idx)
			var fn sql.NullString
			_ = row.Scan(&fn)
			if fn.Valid && fn.String != "" {
				real = fn.String
			}
			return &manualFile{fs: n.fs, importID: n.importID, fileIdx: it.Idx, displayName: name, realName: real, size: it.Bytes}, nil
		}
	}
	return nil, fuse.ENOENT
}

// ---------------- Folders tree (DB-backed) ----------------

type manualFoldersDir struct {
	fs    *ManualFS
	dirID string
}

func (n *manualFoldersDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0o555
	return nil
}

type folderRow struct {
	ID   string
	Name string
}

type itemRow struct {
	ID       string
	Label    string
	ImportID string
	FileIdx  int
	Bytes    int64
	RealName string
	DispName string
}

func (n *manualFoldersDir) children(ctx context.Context) ([]folderRow, []itemRow, error) {
	// subdirs
	drows, err := n.fs.Jobs.DB().SQL.QueryContext(ctx, `SELECT id, name FROM manual_dirs WHERE parent_id=? ORDER BY name`, n.dirID)
	if err != nil {
		return nil, nil, err
	}
	defer drows.Close()
	dirs := make([]folderRow, 0)
	for drows.Next() {
		var fr folderRow
		if err := drows.Scan(&fr.ID, &fr.Name); err != nil {
			continue
		}
		dirs = append(dirs, fr)
	}

	// items
	q := `
		SELECT i.id, i.label, i.import_id, i.file_idx, f.total_bytes, f.filename
		FROM manual_items i
		JOIN nzb_files f ON f.import_id=i.import_id AND f.idx=i.file_idx
		WHERE i.dir_id=?
		ORDER BY i.label
	`
	irows, err := n.fs.Jobs.DB().SQL.QueryContext(ctx, q, n.dirID)
	if err != nil {
		return dirs, nil, err
	}
	defer irows.Close()
	items := make([]itemRow, 0)
	seen := map[string]int{}
	for irows.Next() {
		var it itemRow
		var fn sql.NullString
		if err := irows.Scan(&it.ID, &it.Label, &it.ImportID, &it.FileIdx, &it.Bytes, &fn); err != nil {
			continue
		}
		it.RealName = ""
		if fn.Valid {
			it.RealName = fn.String
		}
		it.DispName = it.Label
		if it.DispName == "" {
			it.DispName = it.RealName
		}
		if it.DispName == "" {
			it.DispName = fmt.Sprintf("file_%04d.bin", it.FileIdx)
		}
		seen[it.DispName]++
		if seen[it.DispName] > 1 {
			it.DispName = withSuffixBeforeExt(it.DispName, seen[it.DispName])
		}
		items = append(items, it)
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(items, func(i, j int) bool { return items[i].DispName < items[j].DispName })
	return dirs, items, nil
}

func (n *manualFoldersDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	dirs, items, err := n.children(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]fuse.Dirent, 0, len(dirs)+len(items))
	for _, d := range dirs {
		out = append(out, fuse.Dirent{Name: d.Name, Type: fuse.DT_Dir})
	}
	for _, it := range items {
		out = append(out, fuse.Dirent{Name: it.DispName, Type: fuse.DT_File})
	}
	return out, nil
}

func (n *manualFoldersDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	dirs, items, err := n.children(ctx)
	if err != nil {
		return nil, fuse.ENOENT
	}
	for _, d := range dirs {
		if d.Name == name {
			return &manualFoldersDir{fs: n.fs, dirID: d.ID}, nil
		}
	}
	for _, it := range items {
		if it.DispName == name {
			real := it.RealName
			if real == "" {
				real = it.DispName
			}
			return &manualFile{fs: n.fs, importID: it.ImportID, fileIdx: it.FileIdx, displayName: it.DispName, realName: real, size: it.Bytes}, nil
		}
	}
	return nil, fuse.ENOENT
}

// ---------------- File node ----------------

type manualFile struct {
	fs          *ManualFS
	importID    string
	fileIdx     int
	displayName string
	realName    string
	size        int64
}

func (n *manualFile) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = 0o444
	a.Size = uint64(max64(0, n.size))
	a.Mtime = time.Now()
	return nil
}

func (n *manualFile) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
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
	if err := st.StreamRange(ctx, n.importID, n.fileIdx, n.realName, start, end, buf, n.fs.Cfg.Download.PrefetchSegments); err != nil {
		log.Printf("fuse manual read error import=%s fileIdx=%d: %v", n.importID, n.fileIdx, err)
		return fuse.EIO
	}
	resp.Data = buf.Bytes()
	return nil
}

var _ fs.FS = (*ManualFS)(nil)
var _ fs.Node = (*manualRoot)(nil)
var _ fs.NodeStringLookuper = (*manualRoot)(nil)
var _ fs.HandleReadDirAller = (*manualRoot)(nil)

var _ fs.Node = (*manualImportsDir)(nil)
var _ fs.NodeStringLookuper = (*manualImportsDir)(nil)
var _ fs.HandleReadDirAller = (*manualImportsDir)(nil)

var _ fs.Node = (*manualImportDir)(nil)
var _ fs.NodeStringLookuper = (*manualImportDir)(nil)
var _ fs.HandleReadDirAller = (*manualImportDir)(nil)

var _ fs.Node = (*manualFoldersDir)(nil)
var _ fs.NodeStringLookuper = (*manualFoldersDir)(nil)
var _ fs.HandleReadDirAller = (*manualFoldersDir)(nil)

var _ fs.Node = (*manualFile)(nil)
var _ fs.HandleReader = (*manualFile)(nil)

var _ = sort.Strings
