package fusefs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

	streamMu sync.Mutex
	stream   *streamer.Streamer
}

func (m *ManualFS) Root() (fs.Node, error) { return &manualRawRoot{fs: m, rel: ""}, nil }

func (m *ManualFS) getStreamer() *streamer.Streamer {
	m.streamMu.Lock()
	defer m.streamMu.Unlock()
	if m.stream == nil {
		m.stream = streamer.New(m.Cfg.Download, m.Jobs, m.Cfg.Paths.CacheDir, m.Cfg.Paths.CacheMaxBytes)
	}
	return m.stream
}

// manualRawRoot exposes a RAW-like directory tree based on nzb_imports.path.
// For each NZB file, we expose a virtual folder named like the NZB (without .nzb)
// that contains the MKV payloads.
//
// Example:
//   (RAW)    /host/inbox/nzb/PELICULAS/1080/A/Movie (2020).nzb
//   (Manual) /library-manual/PELICULAS/1080/A/Movie (2020)/Movie (2020).mkv
//
// Manual filenames are kept as-is from the NZB (only filtering to .mkv).

type manualRawRoot struct {
	fs  *ManualFS
	rel string // relative path within the virtual manual tree
}

func (n *manualRawRoot) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0o555
	return nil
}

type manualImportPath struct {
	ID  string
	Rel string // relative path from NZB root to the NZB file
}

func (n *manualRawRoot) nzbRoot() string {
	root := strings.TrimSpace(n.fs.Cfg.NgPost.OutputDir)
	if root == "" {
		root = "/host/inbox/nzb"
	}
	return filepath.Clean(root)
}

func (n *manualRawRoot) importPaths(ctx context.Context) ([]manualImportPath, error) {
	if n.fs == nil || n.fs.Jobs == nil {
		return nil, nil
	}
	rows, err := n.fs.Jobs.DB().SQL.QueryContext(ctx, `SELECT id, path FROM nzb_imports ORDER BY imported_at DESC LIMIT 5000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	root := n.nzbRoot()
	out := make([]manualImportPath, 0)
	for rows.Next() {
		var id, p string
		if err := rows.Scan(&id, &p); err != nil {
			continue
		}
		p = filepath.Clean(p)
		rel := ""
		if p == root {
			rel = ""
		} else if strings.HasPrefix(p, root+string(filepath.Separator)) {
			rel = strings.TrimPrefix(p, root+string(filepath.Separator))
		} else {
			// Not under the configured root; skip from the RAW-like view.
			continue
		}
		if strings.TrimSpace(rel) == "" {
			continue
		}
		out = append(out, manualImportPath{ID: id, Rel: rel})
	}
	return out, nil
}

type childEntry struct {
	Name     string
	IsNZBDir bool
	ImportID string
}

func (n *manualRawRoot) children(ctx context.Context) ([]childEntry, error) {
	imports, err := n.importPaths(ctx)
	if err != nil {
		return nil, err
	}

	curParts := []string{}
	if strings.TrimSpace(n.rel) != "" {
		curParts = strings.Split(n.rel, string(filepath.Separator))
	}

	// Collect dirs and nzb-leaf dirs.
	dirs := map[string]bool{}
	leaves := map[string][]string{} // name -> importIDs

	for _, it := range imports {
		parts := strings.Split(it.Rel, string(filepath.Separator))
		if len(parts) == 0 {
			continue
		}
		// Ensure this import is under current rel.
		if len(parts) < len(curParts) {
			continue
		}
		match := true
		for i := range curParts {
			if parts[i] != curParts[i] {
				match = false
				break
			}
		}
		if !match {
			continue
		}

		if len(parts) == len(curParts)+1 {
			// leaf at this level: should be an NZB file
			fn := parts[len(parts)-1]
			if strings.ToLower(filepath.Ext(fn)) != ".nzb" {
				continue
			}
			stem := strings.TrimSuffix(fn, filepath.Ext(fn))
			stem = strings.TrimSpace(stem)
			if stem == "" {
				stem = it.ID
			}
			leaves[stem] = append(leaves[stem], it.ID)
			continue
		}

		// intermediate dir
		child := parts[len(curParts)]
		if strings.TrimSpace(child) != "" {
			dirs[child] = true
		}
	}

	out := make([]childEntry, 0, len(dirs)+len(leaves))
	for d := range dirs {
		out = append(out, childEntry{Name: d, IsNZBDir: false})
	}

	// Resolve leaf name collisions by appending [id8]
	for stem, ids := range leaves {
		if len(ids) == 1 {
			out = append(out, childEntry{Name: stem, IsNZBDir: true, ImportID: ids[0]})
			continue
		}
		for _, id := range ids {
			sfx := id
			if len(sfx) > 8 {
				sfx = sfx[:8]
			}
			out = append(out, childEntry{Name: fmt.Sprintf("%s [%s]", stem, sfx), IsNZBDir: true, ImportID: id})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		// dirs first, then leaves
		if out[i].IsNZBDir != out[j].IsNZBDir {
			return !out[i].IsNZBDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (n *manualRawRoot) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	kids, err := n.children(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]fuse.Dirent, 0, len(kids))
	for _, k := range kids {
		out = append(out, fuse.Dirent{Name: k.Name, Type: fuse.DT_Dir})
	}
	return out, nil
}

func (n *manualRawRoot) Lookup(ctx context.Context, name string) (fs.Node, error) {
	kids, err := n.children(ctx)
	if err != nil {
		return nil, fuse.ENOENT
	}
	for _, k := range kids {
		if k.Name != name {
			continue
		}
		if k.IsNZBDir {
			return &manualImportDir{fs: n.fs, importID: k.ImportID}, nil
		}
		next := name
		if strings.TrimSpace(n.rel) != "" {
			next = filepath.Join(n.rel, name)
		}
		return &manualRawRoot{fs: n.fs, rel: next}, nil
	}
	return nil, fuse.ENOENT
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

		// Manual library: only expose MKV payloads.
		if strings.ToLower(filepath.Ext(name)) != ".mkv" {
			continue
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

	// items (only MKVs)
	q := `
		SELECT i.id, i.label, i.import_id, i.file_idx, f.total_bytes, f.filename
		FROM manual_items i
		JOIN nzb_files f ON f.import_id=i.import_id AND f.idx=i.file_idx
		WHERE i.dir_id=?
		  AND LOWER(COALESCE(f.filename, '')) LIKE '%.mkv'
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

	mu         sync.Mutex
	cacheStart int64
	cacheData  []byte
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
	want := int64(req.Size)
	end := start + want - 1
	if start >= n.size {
		resp.Data = nil
		return nil
	}
	if end >= n.size {
		end = n.size - 1
	}

	// Hot read cache per open handle (same strategy as libraryfs)
	n.mu.Lock()
	if len(n.cacheData) > 0 {
		cs := n.cacheStart
		ce := cs + int64(len(n.cacheData)) - 1
		if start >= cs && end <= ce {
			off := start - cs
			resp.Data = append([]byte(nil), n.cacheData[off:off+(end-start)+1]...)
			n.mu.Unlock()
			return nil
		}
	}
	n.mu.Unlock()

	// Conservative read-ahead to avoid tiny random-read storms from players.
	window := int64(1 * 1024 * 1024) // 1MiB
	if want > window {
		window = want
	}
	fetchEnd := start + window - 1
	if fetchEnd >= n.size {
		fetchEnd = n.size - 1
	}

	st := n.fs.getStreamer()
	buf := &bytes.Buffer{}
	prefetch := n.fs.Cfg.Download.PrefetchSegments
	if prefetch > 2 {
		prefetch = 2
	}
	if prefetch < 0 {
		prefetch = 0
	}
	if err := st.StreamRange(ctx, n.importID, n.fileIdx, n.realName, start, fetchEnd, buf, prefetch); err != nil {
		if errors.Is(err, io.EOF) {
			resp.Data = nil
			return nil
		}
		log.Printf("fuse manual read error import=%s fileIdx=%d: %v", n.importID, n.fileIdx, err)
		return fuse.EIO
	}
	all := buf.Bytes()
	n.mu.Lock()
	n.cacheStart = start
	n.cacheData = append(n.cacheData[:0], all...)
	n.mu.Unlock()

	need := (end - start) + 1
	if int64(len(all)) < need {
		resp.Data = append([]byte(nil), all...)
		return nil
	}
	resp.Data = append([]byte(nil), all[:need]...)
	return nil
}

var _ fs.FS = (*ManualFS)(nil)
var _ fs.Node = (*manualRawRoot)(nil)
var _ fs.NodeStringLookuper = (*manualRawRoot)(nil)
var _ fs.HandleReadDirAller = (*manualRawRoot)(nil)

var _ fs.Node = (*manualImportsDir)(nil)
var _ fs.NodeStringLookuper = (*manualImportsDir)(nil)
var _ fs.HandleReadDirAller = (*manualImportsDir)(nil)

var _ fs.Node = (*manualImportDir)(nil)
var _ fs.NodeStringLookuper = (*manualImportDir)(nil)
var _ fs.HandleReadDirAller = (*manualImportDir)(nil)

var _ fs.Node = (*manualFile)(nil)
var _ fs.HandleReader = (*manualFile)(nil)

var _ = sort.Strings
