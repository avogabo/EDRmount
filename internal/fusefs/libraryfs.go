package fusefs

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
	"github.com/gaby/EDRmount/internal/library"
	"github.com/gaby/EDRmount/internal/streamer"
)

type LibraryFS struct {
	Cfg  config.Config
	Jobs *jobs.Store

	streamMu sync.Mutex
	stream   *streamer.Streamer
}

func (m *LibraryFS) getStreamer() *streamer.Streamer {
	m.streamMu.Lock()
	defer m.streamMu.Unlock()
	if m.stream == nil {
		m.stream = streamer.New(m.Cfg.Download, m.Jobs, m.Cfg.Paths.CacheDir, m.Cfg.Paths.CacheMaxBytes)
	}
	return m.stream
}

type libDir struct {
	fs.Inode
	fs  *LibraryFS
	rel string
}

func (n *libDir) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) unix.Errno {
	out.Mode = fuse.S_IFDIR | 0555
	return 0
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
			r.Filename = filepath.Base(subj)
			if r.Filename == "" {
				r.Filename = fmt.Sprintf("file_%04d.bin", r.Idx)
			}
		}
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

	var kind, title, quality string
	var year, tmdbID int
	err := n.fs.Jobs.DB().SQL.QueryRowContext(ctx, `SELECT kind,title,year,quality,tmdb_id FROM library_overrides WHERE import_id=? AND file_idx=?`, row.ImportID, row.Idx).Scan(&kind, &title, &year, &quality, &tmdbID)
	if err == nil {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			kind = "movie"
		}
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
		}
	}

	initial := library.InitialFolder(g.Title)
	q := g.Quality
	ext := g.Ext
	if ext == "" {
		ext = filepath.Ext(row.Filename)
	}

	y := g.Year
	if y < 0 {
		y = 0
	}

	vars := map[string]string{
		"movies_root":        l.MoviesRoot,
		"series_root":        l.SeriesRoot,
		"emision_folder":     l.EmisionFolder,
		"finalizadas_folder": l.FinalizadasFolder,
		"quality":            q,
		"initial":            initial,
		"ext":                ext,
	}
	nums := map[string]int{
		"year":    y,
		"season":  g.Season,
		"episode": g.Episode,
	}
	
	var rkind, rtitle, rq, status, epTitle, virtualPath string
	var ry, rtmdbID, rseason, repisode int
	err = n.fs.Jobs.DB().SQL.QueryRowContext(ctx, `SELECT kind,title,year,quality,tmdb_id,series_status,season,episode,episode_title,virtual_path FROM library_resolved WHERE import_id=? AND file_idx=?`, row.ImportID, row.Idx).Scan(&rkind, &rtitle, &ry, &rq, &rtmdbID, &status, &rseason, &repisode, &epTitle, &virtualPath)
	if err == nil {
		if strings.TrimSpace(virtualPath) != "" {
			vp := library.CleanPath(virtualPath)
			if n.fs.Cfg.Library.Defaults().UppercaseFolders {
				vp = library.ApplyUppercaseFolders(vp)
			}
			return vp
		}
		if strings.TrimSpace(rtitle) != "" {
			g.Title = rtitle
		}
		if ry > 0 {
			nums["year"] = ry
		}
		if strings.TrimSpace(rq) != "" {
			vars["quality"] = rq
		}
		if rseason > 0 {
			nums["season"] = rseason
		}
		if repisode > 0 {
			nums["episode"] = repisode
		}
		if strings.TrimSpace(epTitle) != "" {
			vars["episode_title"] = epTitle
		}
		if strings.TrimSpace(status) != "" {
			vars["series_status"] = status
		}
		vars["tmdb_id"] = fmt.Sprintf("%d", rtmdbID)
		if strings.EqualFold(rkind, "series") {
			g.IsSeries = true
		}
	}

	if !g.IsSeries {
		movieTitle := g.Title
		mtmdbID := 0
		_ = n.fs.Jobs.DB().SQL.QueryRowContext(ctx, `SELECT tmdb_id,title,year FROM library_overrides WHERE import_id=? AND file_idx=?`, row.ImportID, row.Idx).Scan(&mtmdbID, &movieTitle, &year)
		if strings.TrimSpace(movieTitle) == "" {
			movieTitle = g.Title
		}
		if year < 0 {
			year = 0
		}
		nums["year"] = year
		if mtmdbID < 0 {
			mtmdbID = 0
		}
		vars["title"] = movieTitle
		vars["tmdb_id"] = fmt.Sprintf("%d", mtmdbID)

		dir := library.CleanPath(library.Render(l.MovieDirTemplate, vars, nums))
		file := library.CleanPath(library.Render(l.MovieFileTemplate, vars, nums))
		p := filepath.Join(dir, file)
		if l.UppercaseFolders {
			p = library.ApplyUppercaseFolders(p)
		}
		return p
	}

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

func (n *libDir) Readdir(ctx context.Context) (fs.DirStream, unix.Errno) {
	dirs, files, err := n.children(ctx)
	if err != nil {
		return nil, unix.EIO
	}
	var entries []fuse.DirEntry
	for _, d := range dirs {
		entries = append(entries, fuse.DirEntry{Name: d, Mode: fuse.S_IFDIR})
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		entries = append(entries, fuse.DirEntry{Name: k, Mode: fuse.S_IFREG})
	}
	return fs.NewListDirStream(entries), 0
}

func (n *libDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, unix.Errno) {
	dirs, files, err := n.children(ctx)
	if err != nil {
		return nil, unix.ENOENT
	}
	for _, d := range dirs {
		if d == name {
			rel := name
			if n.rel != "" {
				rel = filepath.Join(n.rel, name)
			}
			ch := n.NewInode(ctx, &libDir{fs: n.fs, rel: rel}, fs.StableAttr{Mode: fuse.S_IFDIR | 0555})
			return ch, 0
		}
	}
	if r, ok := files[name]; ok {
		ch := n.NewInode(ctx, &libFile{fs: n.fs, importID: r.ImportID, fileIdx: r.Idx, name: r.Filename, size: r.Bytes}, fs.StableAttr{Mode: fuse.S_IFREG | 0444})
		return ch, 0
	}
	return nil, unix.ENOENT
}

type libFile struct {
	fs.Inode
	fs       *LibraryFS
	importID string
	fileIdx  int
	name     string
	size     int64
}

func (n *libFile) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) unix.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = uint64(max64(0, n.size))
	return 0
}

func (n *libFile) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, unix.Errno) {
	return &libFileHandle{file: n}, fuse.FOPEN_KEEP_CACHE, 0
}

type libFileHandle struct {
	file *libFile
}

func (h *libFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, unix.Errno) {
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

	// This assumes streamer is injected globally or exposed via Jobs. Let's create a temp method or refactor getStreamer
	// Right now we can just use the global chunk cache with singleflight but we need a streamer instance.
	// For simplicity in the refactor, we'll initialize a streamer here temporarily if needed, or get it from root.
	
	// Temporary streamer instantiation (should be shared later)
	st := streamer.New(n.fs.Cfg.Download, n.fs.Jobs, n.fs.Cfg.Paths.CacheDir, n.fs.Cfg.Paths.CacheMaxBytes)

	result, err, _ := fetchGroup.Do(cacheKey, func() (interface{}, error) {
		buf := &bytes.Buffer{}
		if err := st.StreamRange(ctx, n.importID, n.fileIdx, n.name, chunkStart, chunkEnd, buf, 50); err != nil {
			return nil, err
		}
		data := buf.Bytes()
		if len(data) > 0 {
			globalChunkCache.set(n.importID, n.fileIdx, chunkStart, data)
		}
		return data, nil
	})

	if err != nil {
		log.Printf("fuse library read error import=%s fileIdx=%d: %v", n.importID, n.fileIdx, err)
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
