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
	"sync"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"golang.org/x/sync/singleflight"

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

	// Si estamos por encima del límite, limpiar entradas antiguas
	if c.size+int64(len(data)) > c.maxSize && len(c.chunks) > 0 {
		// Eliminar la mitad de las entradas (simple LRU aproximado)
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

// Global chunk cache (100MB por defecto)
var globalChunkCache = newChunkCache(100 * 1024 * 1024)

// singleflight group para deduplicar descargas concurrentes
var fetchGroup singleflight.Group

// RawFS exposes a read-only filesystem:
//
//	/raw/<importId>/<filename>
//
// where <filename> comes from NZB subject parsing (best-effort).
type RawFS struct {
	Cfg  config.Config
	Jobs *jobs.Store

	streamMu sync.Mutex
	stream   *streamer.Streamer
}

func (r *RawFS) Root() (fs.Node, error) {
	return &rawRoot{fs: r}, nil
}

func (r *RawFS) getStreamer() *streamer.Streamer {
	r.streamMu.Lock()
	defer r.streamMu.Unlock()
	if r.stream == nil {
		r.stream = streamer.New(r.Cfg.Download, r.Jobs, r.Cfg.Paths.CacheDir, r.Cfg.Paths.CacheMaxBytes)
	}
	return r.stream
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

// chunkSize define el tamaño de cada chunk en memoria (1MB)
const chunkSize = 1024 * 1024

// minReadSize define el tamaño mínimo de lectura (4MB para mejor throughput)
const minReadSize = 4 * 1024 * 1024

func (n *rawFile) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	if req.Offset < 0 {
		return fuse.EIO
	}
	if n.size <= 0 {
		return fuse.EIO
	}

	start := int64(req.Offset)
	// Aumentar el tamaño de lectura para mejor throughput
	requestedSize := int64(req.Size)
	if requestedSize < minReadSize {
		requestedSize = minReadSize
	}

	end := start + requestedSize - 1
	if start >= n.size {
		resp.Data = nil
		return nil
	}
	if end >= n.size {
		end = n.size - 1
	}

	// Intentar leer desde caché primero
	cacheKey := globalChunkCache.key(n.importID, n.fileIdx, start)
	if cachedData, ok := globalChunkCache.get(n.importID, n.fileIdx, start, int(end-start+1)); ok {
		// Ajustar al tamaño real solicitado
		if len(cachedData) > req.Size {
			resp.Data = cachedData[:req.Size]
		} else {
			resp.Data = cachedData
		}
		return nil
	}

	st := n.fs.getStreamer()

	// Usar singleflight para deduplicar descargas concurrentes del mismo rango
	result, err, _ := fetchGroup.Do(cacheKey, func() (interface{}, error) {
		buf := &bytes.Buffer{}
		// Aumentar prefetch a 30 segmentos para mejor rendimiento
		if err := st.StreamRange(ctx, n.importID, n.fileIdx, n.name, start, end, buf, 30); err != nil {
			return nil, err
		}
		data := buf.Bytes()

		// Guardar en caché
		if len(data) > 0 {
			globalChunkCache.set(n.importID, n.fileIdx, start, data)
		}
		return data, nil
	})

	if err != nil {
		log.Printf("fuse read error import=%s fileIdx=%d: %v", n.importID, n.fileIdx, err)
		return fuse.EIO
	}

	data := result.([]byte)
	if len(data) == 0 {
		resp.Data = nil
		return nil
	}

	// Ajustar al tamaño real solicitado
	if len(data) > req.Size {
		resp.Data = data[:req.Size]
	} else {
		resp.Data = data
	}
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
