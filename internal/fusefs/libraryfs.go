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
	"github.com/gaby/EDRmount/internal/meta/tmdb"
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
}

func (r *LibraryFS) Root() (fs.Node, error) {
	if r.resolver == nil {
		r.resolver = library.NewResolver(r.Cfg)
	}
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

func (n *libDir) buildPath(ctx context.Context, row libRow) string {
	l := n.fs.Cfg.Library.Defaults()
	g := library.GuessFromFilename(row.Filename)
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

	if !g.IsSeries {
		movieTitle := g.Title
		tmdbID := 0
		if n.fs.resolver != nil {
			if m, ok := n.fs.resolver.ResolveMovie(ctxbg(ctx), movieTitle, year); ok {
				movieTitle = m.Title
				y := m.ReleaseYear()
				if y > 0 {
					year = y
					nums["year"] = y
				}
				tmdbID = m.ID
			}
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

	// Series
	seriesName := g.Title
	seriesTMDB := 0
	bucket := l.EmisionFolder
	if n.fs.resolver != nil {
		if tv, ok := n.fs.resolver.ResolveTV(ctxbg(ctx), seriesName, year); ok {
			seriesName = tv.Name
			y := tv.FirstAirYear()
			if y > 0 {
				year = y
				nums["year"] = y
			}
			seriesTMDB = tv.ID
			b := tmdb.MapTVStatusToBucket(tv.Status)
			if b == tmdb.SeriesBucketFinalizada {
				bucket = l.FinalizadasFolder
			} else if b == tmdb.SeriesBucketEmision {
				bucket = l.EmisionFolder
			}
			if g.Season > 0 && g.Episode > 0 {
				if epName, ok := n.fs.resolver.ResolveEpisodeTitle(ctxbg(ctx), tv.ID, g.Season, g.Episode); ok {
					vars["episode_title"] = epName
				}
			}
		}
	}
	if _, ok := vars["episode_title"]; !ok {
		vars["episode_title"] = "Episode"
	}
	vars["series"] = seriesName
	vars["tmdb_id"] = fmt.Sprintf("%d", seriesTMDB)
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
