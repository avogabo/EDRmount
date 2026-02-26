package fusefs

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sync/singleflight"
	"golang.org/x/sys/unix"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
	"github.com/gaby/EDRmount/internal/streamer"
	"github.com/gaby/EDRmount/internal/subject"
)

// chunkCache almacena chunks de datos en memoria para evitar re-descargas
type chunkCache struct {
	mu      sync.RWMutex
	chunks  map[string][]byte // key: "importID:fileIdx:offset"
	size    int64
	maxSize int64
}

func newChunkCache(maxSize int64) *chunkCache {
	return &chunkCache{
		chunks:  make(map[string][]byte),
		maxSize: maxSize,
	}
}

func (c *chunkCache) key(importID string, fileIdx int, offset int64) string {
	return fmt.Sprintf("%s:%d:%d", importID, fileIdx, offset)
}

func (c *chunkCache) get(importID string, fileIdx int, offset int64, size int) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := c.key(importID, fileIdx, offset)
	data, ok := c.chunks[key]
	if !ok || len(data) < size {
		return nil, false
	}
	return data[:size], true
}

func (c *chunkCache) set(importID string, fileIdx int, offset int64, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.size+int64(len(data)) > c.maxSize && len(c.chunks) > 0 {
		for k, v := range c.chunks {
			delete(c.chunks, k)
			c.size -= int64(len(v))
			if c.size < c.maxSize/2 {
				break
			}
		}
	}

	key := c.key(importID, fileIdx, offset)
	if old, exists := c.chunks[key]; exists {
		c.size -= int64(len(old))
	}
	c.chunks[key] = data
	c.size += int64(len(data))
}

var globalChunkCache = newChunkCache(100 * 1024 * 1024)
var fetchGroup singleflight.Group

type rawRoot struct {
	fs.Inode
	Cfg  config.Config
	Jobs *jobs.Store

	streamMu sync.Mutex
	stream   *streamer.Streamer
}

var _ = (fs.NodeOnAdder)((*rawRoot)(nil))
var _ = (fs.NodeLookuper)((*rawRoot)(nil))

func (r *rawRoot) getStreamer() *streamer.Streamer {
	r.streamMu.Lock()
	defer r.streamMu.Unlock()
	if r.stream == nil {
		r.stream = streamer.New(r.Cfg.Download, r.Jobs, r.Cfg.Paths.CacheDir, r.Cfg.Paths.CacheMaxBytes)
	}
	return r.stream
}

func (n *rawRoot) OnAdd(ctx context.Context) {
	ch := n.NewPersistentInode(ctx, &rawImportsDir{root: n}, fs.StableAttr{Mode: fuse.S_IFDIR | 0555})
	n.AddChild("raw", ch, false)
}

func (n *rawRoot) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, unix.Errno) {
	return nil, unix.ENOENT
}

type rawImportsDir struct {
	fs.Inode
	root *rawRoot
}

var _ = (fs.NodeReaddirer)((*rawImportsDir)(nil))
var _ = (fs.NodeLookuper)((*rawImportsDir)(nil))

func (n *rawImportsDir) Readdir(ctx context.Context) (fs.DirStream, unix.Errno) {
	rows, err := n.root.Jobs.DB().SQL.QueryContext(ctx, `SELECT id FROM nzb_imports ORDER BY imported_at DESC LIMIT 500`)
	if err != nil {
		return nil, unix.EIO
	}
	defer rows.Close()
	var entries []fuse.DirEntry
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		entries = append(entries, fuse.DirEntry{Name: id, Mode: fuse.S_IFDIR})
	}
	return fs.NewListDirStream(entries), 0
}

func (n *rawImportsDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, unix.Errno) {
	row := n.root.Jobs.DB().SQL.QueryRowContext(ctx, `SELECT COUNT(1) FROM nzb_imports WHERE id=?`, name)
	var c int
	if err := row.Scan(&c); err != nil || c == 0 {
		return nil, unix.ENOENT
	}
	ch := n.NewInode(ctx, &rawImportDir{root: n.root, importID: name}, fs.StableAttr{Mode: fuse.S_IFDIR | 0555})
	return ch, 0
}

type fileEntry struct {
	Idx     int
	Subject string
	Bytes   int64
	Name    string
}

type rawImportDir struct {
	fs.Inode
	root     *rawRoot
	importID string
}

func (n *rawImportDir) listFiles(ctx context.Context) ([]fileEntry, error) {
	rows, err := n.root.Jobs.DB().SQL.QueryContext(ctx, `SELECT idx,filename,subject,total_bytes FROM nzb_files WHERE import_id=? ORDER BY idx ASC`, n.importID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fileEntry
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
			if f2, ok := subject.FilenameFromSubject(e.Subject); ok {
				base = f2
			}
		}
		if base == "" {
			base = fmt.Sprintf("file_%04d.bin", e.Idx)
		}

		seen[base]++
		if seen[base] > 1 {
			base = withSuffixBeforeExt(base, seen[base])
		}
		e.Name = base
		out = append(out, e)
	}
	return out, nil
}

var _ = (fs.NodeReaddirer)((*rawImportDir)(nil))
var _ = (fs.NodeLookuper)((*rawImportDir)(nil))

func (n *rawImportDir) Readdir(ctx context.Context) (fs.DirStream, unix.Errno) {
	files, err := n.listFiles(ctx)
	if err != nil {
		return nil, unix.EIO
	}
	var entries []fuse.DirEntry
	for _, f := range files {
		entries = append(entries, fuse.DirEntry{Name: f.Name, Mode: fuse.S_IFREG})
	}
	return fs.NewListDirStream(entries), 0
}

func (n *rawImportDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, unix.Errno) {
	files, err := n.listFiles(ctx)
	if err != nil {
		return nil, unix.EIO
	}
	for _, f := range files {
		if f.Name == name {
			child := n.NewInode(ctx, &rawFile{
				root:     n.root,
				importID: n.importID,
				fileIdx:  f.Idx,
				name:     f.Name,
				size:     f.Bytes,
			}, fs.StableAttr{Mode: fuse.S_IFREG | 0444})
			return child, 0
		}
	}
	return nil, unix.ENOENT
}

type rawFile struct {
	fs.Inode
	root     *rawRoot
	importID string
	fileIdx  int
	name     string
	size     int64
}

var _ = (fs.NodeOpener)((*rawFile)(nil))
var _ = (fs.NodeGetattrer)((*rawFile)(nil))

func (n *rawFile) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) unix.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = uint64(max64(0, n.size))
	out.Mtime = uint64(time.Now().Unix())
	return 0
}

func (n *rawFile) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, unix.Errno) {
	return &rawFileHandle{file: n}, fuse.FOPEN_KEEP_CACHE, 0
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

type rawFileHandle struct {
	file *rawFile
}

var _ = (fs.FileReader)((*rawFileHandle)(nil))

const chunkSize = 1 * 1024 * 1024
const minReadSize = 1 * 1024 * 1024
const readPrefetchChunks = 4
const streamPrefetch = 200

func (h *rawFileHandle) readChunk(ctx context.Context, st *streamer.Streamer, chunkStart int64) ([]byte, error) {
	n := h.file
	if chunkStart < 0 || chunkStart >= n.size {
		return nil, nil
	}
	chunkEnd := chunkStart + int64(chunkSize) - 1
	if chunkEnd >= n.size {
		chunkEnd = n.size - 1
	}
	size := int(chunkEnd-chunkStart) + 1
	cacheKey := globalChunkCache.key(n.importID, n.fileIdx, chunkStart)
	if cachedData, ok := globalChunkCache.get(n.importID, n.fileIdx, chunkStart, size); ok {
		return cachedData, nil
	}

	result, err, _ := fetchGroup.Do(cacheKey, func() (interface{}, error) {
		if cachedData, ok := globalChunkCache.get(n.importID, n.fileIdx, chunkStart, size); ok {
			return cachedData, nil
		}
		buf := &bytes.Buffer{}
		if err := st.StreamRange(ctx, n.importID, n.fileIdx, n.name, chunkStart, chunkEnd, buf, streamPrefetch); err != nil {
			return nil, err
		}
		data := buf.Bytes()
		if len(data) > 0 {
			globalChunkCache.set(n.importID, n.fileIdx, chunkStart, data)
		}
		return data, nil
	})
	if err != nil {
		return nil, err
	}
	data, _ := result.([]byte)
	return data, nil
}

func (h *rawFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, unix.Errno) {
	n := h.file
	if off < 0 || n.size <= 0 || off >= n.size {
		return fuse.ReadResultData(nil), 0
	}

	readLen := int64(len(dest))
	if readLen <= 0 {
		return fuse.ReadResultData(nil), 0
	}
	if readLen < minReadSize {
		readLen = minReadSize
	}

	start := off
	end := start + readLen - 1
	if end >= n.size {
		end = n.size - 1
	}

	chunkStart := (start / chunkSize) * chunkSize
	chunkEnd := (end / int64(chunkSize)) * int64(chunkSize)
	prefetchEnd := chunkEnd + int64(readPrefetchChunks*chunkSize)
	if prefetchEnd >= n.size {
		prefetchEnd = ((n.size - 1) / int64(chunkSize)) * int64(chunkSize)
	}

	st := n.root.getStreamer()
	window := make([]byte, 0, int(end-chunkStart)+1)
	for cur := chunkStart; cur <= prefetchEnd; cur += int64(chunkSize) {
		data, err := h.readChunk(ctx, st, cur)
		if err != nil {
			log.Printf("fuse read error import=%s fileIdx=%d chunk=%d: %v", n.importID, n.fileIdx, cur, err)
			return fuse.ReadResultData(nil), unix.EIO
		}
		if cur <= chunkEnd && len(data) > 0 {
			window = append(window, data...)
		}
	}
	if len(window) == 0 {
		return fuse.ReadResultData(nil), 0
	}

	rel := int(start - chunkStart)
	if rel < 0 || rel >= len(window) {
		return fuse.ReadResultData(nil), 0
	}
	out := window[rel:]
	if len(out) > len(dest) {
		out = out[:len(dest)]
	}
	return fuse.ReadResultData(out), 0
}

func safeName(s string) string {
	s = filepath.Base(s)
	s = strings.ReplaceAll(s, "/", "_")
	return s
}

func withSuffixBeforeExt(name string, m int) string {
	if m <= 1 {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if ext == "" {
		return fmt.Sprintf("%s__%d", base, m)
	}
	return fmt.Sprintf("%s__%d%s", base, m, ext)
}

var _ = sql.ErrNoRows
var _ = safeName
