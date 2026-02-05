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
	"strings"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
	"github.com/gaby/EDRmount/internal/library"
	"github.com/gaby/EDRmount/internal/streamer"
)

// LibraryFS exposes a read-only organized view under /library.
// It is best-effort: until metadata matching is implemented, IDs may be placeholders.
//
// Example default target paths:
//   Peliculas/4K/T/Titanic (1999) tmdb-597/Titanic (1999) tmdb-597.mkv
//   SERIES/Emision/D/Dark (2017) tmdb-123/Temporada 01/01x01 - Secrets.mkv

type LibraryFS struct {
	Cfg  config.Config
	Jobs *jobs.Store
}

func (r *LibraryFS) Root() (fs.Node, error) {
	return &libDir{fs: r, rel: ""}, nil
}

type libFile struct {
	fs       *LibraryFS
	importID string
	fileIdx  int
	name     string
	size     int64
}

func (n *libFile) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = 0o444
	a.Size = uint64(max64(0, n.size))
	a.Mtime = time.Now()
	return nil
}

func (n *libFile) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
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
		log.Printf("fuse library read error import=%s fileIdx=%d: %v", n.importID, n.fileIdx, err)
		return fuse.EIO
	}
	resp.Data = buf.Bytes()
	return nil
}

var _ fs.Node = (*libFile)(nil)
var _ fs.HandleReader = (*libFile)(nil)

// libDir is a generic directory node for any prefix in the virtual library tree.

type libDir struct {
	fs  *LibraryFS
	rel string // relative path within /library
}

func (n *libDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0o555
	return nil
}

type libRow struct {
	ImportID string
	Idx      int
	Filename string
	Bytes    int64
}

func (n *libDir) rows(ctx context.Context) ([]libRow, error) {
	rows, err := n.fs.Jobs.DB().SQL.QueryContext(ctx, `SELECT import_id, idx, filename, subject, total_bytes FROM nzb_files ORDER BY import_id, idx LIMIT 5000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]libRow, 0)
	for rows.Next() {
		var r libRow
		var subj string
		var fn sql.NullString
		if err := rows.Scan(&r.ImportID, &r.Idx, &fn, &subj, &r.Bytes); err != nil {
			continue
		}
		if fn.Valid && fn.String != "" {
			r.Filename = fn.String
		} else {
			// fallback
			r.Filename = filepath.Base(subj)
			if r.Filename == "" {
				r.Filename = fmt.Sprintf("file_%04d.bin", r.Idx)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func (n *libDir) buildPath(row libRow) string {
	l := n.fs.Cfg.Library.Defaults()
	g := library.GuessFromFilename(row.Filename)
	initial := library.InitialFolder(g.Title)
	quality := g.Quality
	ext := g.Ext
	if ext == "" {
		ext = filepath.Ext(row.Filename)
	}

	// placeholder ids until TMDB matching is wired.
	id := "unknown"
	year := g.Year
	if year <= 0 {
		year = 0
	}

	if !g.IsSeries {
		dirName := g.Title
		if year > 0 {
			dirName = fmt.Sprintf("%s (%d)", g.Title, year)
		}
		dirName = fmt.Sprintf("%s tmdb-%s", dirName, id)
		dir := filepath.Join(l.MoviesRoot, quality, initial, dirName)
		fileName := g.Title
		if year > 0 {
			fileName = fmt.Sprintf("%s (%d)", g.Title, year)
		}
		fileName = fmt.Sprintf("%s tmdb-%s%s", fileName, id, ext)
		p := filepath.Join(dir, fileName)
		if l.UppercaseFolders {
			p = library.ApplyUppercaseFolders(p)
		}
		return p
	}

	// series: simple placeholder title/year as well
	seriesStatus := l.EmisionFolder
	seriesName := g.Title
	if year > 0 {
		seriesName = fmt.Sprintf("%s (%d)", g.Title, year)
	}
	seriesName = fmt.Sprintf("%s tmdb-%s", seriesName, id)
	seasonFolder := fmt.Sprintf("Temporada %02d", maxInt(1, g.Season))
	epTitle := "Episode"
	fileName := fmt.Sprintf("%02dx%02d - %s%s", maxInt(1, g.Season), maxInt(1, g.Episode), epTitle, ext)

	dir := filepath.Join(l.SeriesRoot, seriesStatus, initial, seriesName, seasonFolder)
	p := filepath.Join(dir, fileName)
	if l.UppercaseFolders {
		p = library.ApplyUppercaseFolders(p)
	}
	return p
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (n *libDir) children(ctx context.Context) (dirs []string, files map[string]libRow, err error) {
	rows, err := n.rows(ctx)
	if err != nil {
		return nil, nil, err
	}
	prefix := strings.Trim(n.rel, string(filepath.Separator))
	files = map[string]libRow{}
	seenDir := map[string]bool{}

	for _, r := range rows {
		p := filepath.Clean(n.buildPath(r))
		p = strings.TrimPrefix(p, string(filepath.Separator))

		// match prefix
		if prefix != "" {
			if p == prefix {
				continue
			}
			if !strings.HasPrefix(p, prefix+string(filepath.Separator)) {
				continue
			}
			p = strings.TrimPrefix(p, prefix+string(filepath.Separator))
		}

		parts := strings.Split(p, string(filepath.Separator))
		if len(parts) == 0 {
			continue
		}
		name := parts[0]
		if len(parts) == 1 {
			// file at this level
			files[name] = r
			continue
		}
		if !seenDir[name] {
			seenDir[name] = true
			dirs = append(dirs, name)
		}
	}
	sort.Strings(dirs)
	return dirs, files, nil
}

func (n *libDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	dirs, files, err := n.children(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]fuse.Dirent, 0, len(dirs)+len(files))
	for _, d := range dirs {
		out = append(out, fuse.Dirent{Name: d, Type: fuse.DT_Dir})
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, fuse.Dirent{Name: k, Type: fuse.DT_File})
	}
	return out, nil
}

func (n *libDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	dirs, files, err := n.children(ctx)
	if err != nil {
		return nil, fuse.ENOENT
	}
	for _, d := range dirs {
		if d == name {
			rel := name
			if n.rel != "" {
				rel = filepath.Join(n.rel, name)
			}
			return &libDir{fs: n.fs, rel: rel}, nil
		}
	}
	if r, ok := files[name]; ok {
		return &libFile{fs: n.fs, importID: r.ImportID, fileIdx: r.Idx, name: r.Filename, size: r.Bytes}, nil
	}
	return nil, fuse.ENOENT
}

var _ fs.FS = (*LibraryFS)(nil)
var _ fs.Node = (*libDir)(nil)
var _ fs.HandleReadDirAller = (*libDir)(nil)
var _ fs.NodeStringLookuper = (*libDir)(nil)

// Ensure deterministic ordering (helps Plex scans).
var _ = sort.Strings
