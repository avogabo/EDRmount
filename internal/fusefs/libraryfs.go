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

	resolver *library.Resolver
	streamMu sync.Mutex
	stream   *streamer.Streamer
}

func (r *LibraryFS) Root() (fs.Node, error) {
	if r.resolver == nil {
		r.resolver = library.NewResolver(r.Cfg)
	}
	return &libDir{fs: r, rel: ""}, nil
}

func (r *LibraryFS) getStreamer() *streamer.Streamer {
	r.streamMu.Lock()
	defer r.streamMu.Unlock()
	if r.stream == nil {
		r.stream = streamer.New(r.Cfg.Download, r.Jobs, r.Cfg.Paths.CacheDir, r.Cfg.Paths.CacheMaxBytes)
	}
	return r.stream
}

type libFile struct {
	fs       *LibraryFS
	importID string
	fileIdx  int
	name     string
	size     int64

	mu         sync.Mutex
	cacheStart int64
	cacheData  []byte
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
	if start >= n.size {
		resp.Data = nil
		return nil
	}
	want := int64(req.Size)
	if want <= 0 {
		resp.Data = nil
		return nil
	}
	end := start + want - 1
	if end >= n.size {
		end = n.size - 1
	}

	// Hot read cache per open file handle: helps media players that read sequentially in small chunks.
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

	// Conservative read-ahead to avoid bursty segment storms on some clients.
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
	if err := st.StreamRange(ctx, n.importID, n.fileIdx, n.name, start, fetchEnd, buf, prefetch); err != nil {
		if errors.Is(err, io.EOF) {
			resp.Data = nil
			return nil
		}
		log.Printf("fuse library read error import=%s fileIdx=%d: %v", n.importID, n.fileIdx, err)
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
		// Auto library: only expose MKV payloads.
		if strings.ToLower(filepath.Ext(r.Filename)) != ".mkv" {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (n *libDir) buildPath(ctx context.Context, row libRow) string {
	l := n.fs.Cfg.Library.Defaults()
	g := library.GuessFromFilename(row.Filename)

	// Overrides: allow manual correction while still exposing it in library-auto.
	// (Plex can continue to point at library-auto.)
	{
		var kind, title, quality string
		var year, tmdbID int
		err := n.fs.Jobs.DB().SQL.QueryRowContext(ctx, `SELECT kind,title,year,quality,tmdb_id FROM library_overrides WHERE import_id=? AND file_idx=?`, row.ImportID, row.Idx).Scan(&kind, &title, &year, &quality, &tmdbID)
		if err == nil {
			kind = strings.TrimSpace(kind)
			if kind == "" {
				kind = "movie"
			}
			// For now, implement movie overrides (tv reserved).
			if kind == "movie" {
				if strings.TrimSpace(title) != "" {
					g.Title = strings.TrimSpace(title)
				}
				if year > 0 {
					g.Year = year
				}
				if strings.TrimSpace(quality) != "" {
					g.Quality = strings.TrimSpace(quality)
				}
				// store tmdb id in a local var via vars below
				// (we still try to resolve if tmdbID==0 to enrich titles, but it's optional)
				varsTMDBOverride := tmdbID
				_ = varsTMDBOverride
			}
		}
	}

	initial := library.InitialFolder(g.Title)
	quality := g.Quality
	ext := g.Ext
	if ext == "" {
		ext = filepath.Ext(row.Filename)
	}

	year := g.Year
	if year < 0 {
		year = 0
	}

	vars := map[string]string{
		"movies_root":        l.MoviesRoot,
		"series_root":        l.SeriesRoot,
		"emision_folder":     l.EmisionFolder,
		"finalizadas_folder": l.FinalizadasFolder,
		"quality":            quality,
		"initial":            initial,
		"ext":                ext,
	}
	nums := map[string]int{
		"year":    year,
		"season":  g.Season,
		"episode": g.Episode,
	}
	// Prefer resolved metadata produced at import-time.
	{
		var kind, title, q, status, epTitle, virtualPath string
		var y, tmdbID, season, episode int
		err := n.fs.Jobs.DB().SQL.QueryRowContext(ctx, `SELECT kind,title,year,quality,tmdb_id,series_status,season,episode,episode_title,virtual_path FROM library_resolved WHERE import_id=? AND file_idx=?`, row.ImportID, row.Idx).Scan(&kind, &title, &y, &q, &tmdbID, &status, &season, &episode, &epTitle, &virtualPath)
		if err == nil {
			if strings.TrimSpace(virtualPath) != "" {
				vp := library.CleanPath(virtualPath)
				if n.fs.Cfg.Library.Defaults().UppercaseFolders {
					vp = library.ApplyUppercaseFolders(vp)
				}
				return vp
			}
			if strings.TrimSpace(title) != "" {
				g.Title = title
			}
			if y > 0 {
				nums["year"] = y
			}
			if strings.TrimSpace(q) != "" {
				vars["quality"] = q
			}
			if season > 0 {
				nums["season"] = season
			}
			if episode > 0 {
				nums["episode"] = episode
			}
			if strings.TrimSpace(epTitle) != "" {
				vars["episode_title"] = epTitle
			}
			if strings.TrimSpace(status) != "" {
				vars["series_status"] = status
			}
			vars["tmdb_id"] = fmt.Sprintf("%d", tmdbID)
			if strings.EqualFold(kind, "series") {
				g.IsSeries = true
			}
		}
	}

	if !g.IsSeries {
		// Fast path for FUSE listing: avoid external resolvers (TMDB/FileBot) on each directory read.
		movieTitle := g.Title
		tmdbID := 0
		// Respect explicit override tmdb_id/title/year if present.
		_ = n.fs.Jobs.DB().SQL.QueryRowContext(ctx, `SELECT tmdb_id,title,year FROM library_overrides WHERE import_id=? AND file_idx=?`, row.ImportID, row.Idx).Scan(&tmdbID, &movieTitle, &year)
		if strings.TrimSpace(movieTitle) == "" {
			movieTitle = g.Title
		}
		if year < 0 {
			year = 0
		}
		nums["year"] = year
		if tmdbID < 0 {
			tmdbID = 0
		}
		vars["title"] = movieTitle
		vars["tmdb_id"] = fmt.Sprintf("%d", tmdbID)

		dir := library.CleanPath(library.Render(l.MovieDirTemplate, vars, nums))
		file := library.CleanPath(library.Render(l.MovieFileTemplate, vars, nums))
		p := filepath.Join(dir, file)
		if l.UppercaseFolders {
			p = library.ApplyUppercaseFolders(p)
		}
		return p
	}

	// Series (fast path): avoid external resolvers on each directory listing.
	seriesName := g.Title
	seriesTMDB := 0
	bucket := vars["series_status"]
	if strings.TrimSpace(bucket) == "" {
		bucket = l.EmisionFolder
	}
	if _, ok := vars["episode_title"]; !ok {
		vars["episode_title"] = "Episode"
	}
	vars["series"] = seriesName
	if vars["tmdb_id"] == "" {
		vars["tmdb_id"] = fmt.Sprintf("%d", seriesTMDB)
	}
	vars["series_status"] = bucket

	baseDir := library.CleanPath(library.Render(l.SeriesDirTemplate, vars, nums))
	seasonDirName := library.CleanPath(library.Render(l.SeasonFolderTemplate, vars, nums))
	file := library.CleanPath(library.Render(l.SeriesFileTemplate, vars, nums))
	p := filepath.Join(baseDir, seasonDirName, file)
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

func ctxbg(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
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
		p := filepath.Clean(n.buildPath(ctx, r))
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
