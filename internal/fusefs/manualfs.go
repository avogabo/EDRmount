package fusefs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
	"github.com/gaby/EDRmount/internal/streamer"
	"golang.org/x/sys/unix"
)

var manChunkCaches sync.Map

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

type manualRoot struct {
	fs.Inode
	Cfg  config.Config
	Jobs *jobs.Store
	mfs  *ManualFS
}

func (n *manualRoot) OnAdd(ctx context.Context) {
	n.mfs = &ManualFS{Cfg: n.Cfg, Jobs: n.Jobs}
	ch := n.NewPersistentInode(ctx, &manualRawRoot{fs: n.mfs, rel: ""}, fs.StableAttr{Mode: fuse.S_IFDIR | 0555})
	n.AddChild("raw", ch, false)
}

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
type manualRawRoot struct {
	fs.Inode
	fs  *ManualFS
	rel string // relative path within the virtual manual tree
}

func (n *manualRawRoot) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) unix.Errno {
	out.Mode = fuse.S_IFDIR | 0555
	return 0
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

func (n *manualRawRoot) Readdir(ctx context.Context) (fs.DirStream, unix.Errno) {
	kids, err := n.children(ctx)
	if err != nil {
		return nil, unix.EIO
	}
	var entries []fuse.DirEntry
	for _, k := range kids {
		entries = append(entries, fuse.DirEntry{Name: k.Name, Mode: fuse.S_IFDIR})
	}
	return fs.NewListDirStream(entries), 0
}

func (n *manualRawRoot) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, unix.Errno) {
	kids, err := n.children(ctx)
	if err != nil {
		return nil, unix.ENOENT
	}
	for _, k := range kids {
		if k.Name != name {
			continue
		}
		if k.IsNZBDir {
			ch := n.NewInode(ctx, &manualImportDir{fs: n.fs, importID: k.ImportID}, fs.StableAttr{Mode: fuse.S_IFDIR | 0555})
			return ch, 0
		}
		next := name
		if strings.TrimSpace(n.rel) != "" {
			next = filepath.Join(n.rel, name)
		}
		ch := n.NewInode(ctx, &manualRawRoot{fs: n.fs, rel: next}, fs.StableAttr{Mode: fuse.S_IFDIR | 0555})
		return ch, 0
	}
	return nil, unix.ENOENT
}

type manualImportDir struct {
	fs.Inode
	fs       *ManualFS
	importID string
}

func (n *manualImportDir) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) unix.Errno {
	out.Mode = fuse.S_IFDIR | 0555
	return 0
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
	for rows.Next() {
		var r impFileRow
		var dbfn sql.NullString
		var subj string
		if err := rows.Scan(&r.Idx, &dbfn, &subj, &r.Bytes); err != nil {
			continue
		}
		// logic from listFiles
		out = append(out, r)
	}
	return out, nil
}

func (n *manualImportDir) Readdir(ctx context.Context) (fs.DirStream, unix.Errno) {
	files, err := n.list(ctx)
	if err != nil {
		return nil, unix.EIO
	}
	var entries []fuse.DirEntry
	for _, f := range files {
		entries = append(entries, fuse.DirEntry{Name: f.Filename, Mode: fuse.S_IFREG})
	}
	return fs.NewListDirStream(entries), 0
}

func (n *manualImportDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, unix.Errno) {
	files, err := n.list(ctx)
	if err != nil {
		return nil, unix.ENOENT
	}
	for _, f := range files {
		if f.Filename == name {
			ch := n.NewInode(ctx, &manualFile{fs: n.fs, importID: n.importID, fileIdx: f.Idx, displayName: f.Filename, realName: f.Filename, size: f.Bytes}, fs.StableAttr{Mode: fuse.S_IFREG | 0444})
			return ch, 0
		}
	}
	return nil, unix.ENOENT
}

type manualFile struct {
	fs.Inode
	fs          *ManualFS
	importID    string
	fileIdx     int
	displayName string
	realName    string
	size        int64
}

func (n *manualFile) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) unix.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = uint64(max64(0, n.size))
	return 0
}

func (n *manualFile) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, unix.Errno) {
	return &manualFileHandle{file: n}, fuse.FOPEN_KEEP_CACHE, 0
}

type manualFileHandle struct {
	file *manualFile
}

func (h *manualFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, unix.Errno) {
	n := h.file
	if off < 0 || n.size <= 0 {
		return fuse.ReadResultData(nil), unix.EIO
	}
	if off >= n.size {
		return fuse.ReadResultData(nil), 0
	}
	
	want := len(dest)
	if int64(want) < minReadSize {
		want = minReadSize
	}

	start := off
	chunkStart := (start / chunkSize) * chunkSize
	chunkEnd := chunkStart + int64(want) - 1
	if chunkEnd >= n.size {
		chunkEnd = n.size - 1
	}
	need := int(chunkEnd-chunkStart) + 1
	cacheKey := globalChunkCache.key(n.importID, n.fileIdx, chunkStart)

	if cachedData, ok := globalChunkCache.get(n.importID, n.fileIdx, chunkStart, need); ok {
		rel := int(start - chunkStart)
		if rel < 0 || rel >= len(cachedData) {
			return fuse.ReadResultData(nil), 0
		}
		out := cachedData[rel:]
		if len(out) > len(dest) {
			out = out[:len(dest)]
		}
		return fuse.ReadResultData(out), 0
	}

	st := n.fs.getStreamer()
	result, err, _ := fetchGroup.Do(cacheKey, func() (interface{}, error) {
		buf := &bytes.Buffer{}
		if err := st.StreamRange(ctx, n.importID, n.fileIdx, n.realName, chunkStart, chunkEnd, buf, 50); err != nil {
			return nil, err
		}
		data := buf.Bytes()
		if len(data) > 0 {
			globalChunkCache.set(n.importID, n.fileIdx, chunkStart, data)
		}
		return data, nil
	})

	if err != nil {
		log.Printf("fuse manual read error import=%s fileIdx=%d: %v", n.importID, n.fileIdx, err)
		return fuse.ReadResultData(nil), unix.EIO
	}

	data := result.([]byte)
	if len(data) == 0 {
		return fuse.ReadResultData(nil), 0
	}

	rel := int(start - chunkStart)
	if rel < 0 || rel >= len(data) {
		return fuse.ReadResultData(nil), 0
	}
	out := data[rel:]
	if len(out) > len(dest) {
		out = out[:len(dest)]
	}
	return fuse.ReadResultData(out), 0
}

var _ = sql.ErrNoRows
var _ = errors.New("")
