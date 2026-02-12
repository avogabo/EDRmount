package library

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/meta/tmdb"
)

type Resolver struct {
	cfg config.Config
	c   *tmdb.Client

	mu         sync.Mutex
	movieCache map[string]tmdb.MovieSearchResult
	tvCache    map[string]tmdb.TVDetails
	epCache    map[string]string // tvID|season|episode -> name
}

func NewResolver(cfg config.Config) *Resolver {
	r := &Resolver{cfg: cfg}
	r.movieCache = map[string]tmdb.MovieSearchResult{}
	r.tvCache = map[string]tmdb.TVDetails{}
	r.epCache = map[string]string{}

	if cfg.Metadata.TMDB.Enabled && strings.TrimSpace(cfg.Metadata.TMDB.APIKey) != "" {
		r.c = tmdb.New(cfg.Metadata.TMDB.APIKey)
		r.c.Language = cfg.Metadata.TMDB.Language
	}
	return r
}

func (r *Resolver) Enabled() bool { return r != nil && r.c != nil }

func (r *Resolver) ResolveMovie(ctx context.Context, title string, year int) (tmdb.MovieSearchResult, bool) {
	if !r.Enabled() {
		return tmdb.MovieSearchResult{}, false
	}
	baseTitle := strings.TrimSpace(title)
	key := fmt.Sprintf("m:%s:%d", strings.ToLower(baseTitle), year)
	r.mu.Lock()
	if v, ok := r.movieCache[key]; ok {
		r.mu.Unlock()
		return v, true
	}
	r.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	searchTitles := []string{baseTitle}
	if cleaned := sanitizeMovieQuery(baseTitle, year); cleaned != "" && !strings.EqualFold(cleaned, baseTitle) {
		searchTitles = append(searchTitles, cleaned)
	}

	var res []tmdb.MovieSearchResult
	for _, q := range searchTitles {
		out, err := r.c.SearchMovie(cctx, q, year)
		if err == nil && len(out) > 0 {
			res = out
			break
		}
	}
	if len(res) == 0 {
		return tmdb.MovieSearchResult{}, false
	}

	best := res[0]
	if year > 0 {
		for _, it := range res {
			if it.ReleaseYear() == year {
				best = it
				break
			}
		}
	}

	r.mu.Lock()
	r.movieCache[key] = best
	r.mu.Unlock()
	return best, true
}

func (r *Resolver) ResolveTV(ctx context.Context, title string, year int) (tmdb.TVDetails, bool) {
	if !r.Enabled() {
		return tmdb.TVDetails{}, false
	}
	baseTitle := strings.TrimSpace(title)
	key := fmt.Sprintf("t:%s:%d", strings.ToLower(baseTitle), year)
	r.mu.Lock()
	if v, ok := r.tvCache[key]; ok {
		r.mu.Unlock()
		return v, true
	}
	r.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	searchTitles := []string{baseTitle}
	if cleaned := sanitizeTVQuery(baseTitle, year); cleaned != "" && !strings.EqualFold(cleaned, baseTitle) {
		searchTitles = append(searchTitles, cleaned)
	}

	var res []tmdb.TVSearchResult
	for _, q := range searchTitles {
		out, err := r.c.SearchTV(cctx, q, year)
		if err == nil && len(out) > 0 {
			res = out
			break
		}
	}
	if len(res) == 0 {
		for _, q := range fallbackTVQueries(baseTitle) {
			out, err := r.c.SearchTV(cctx, q, year)
			if err == nil && len(out) > 0 {
				res = out
				break
			}
		}
	}
	if len(res) == 0 {
		return tmdb.TVDetails{}, false
	}

	best := res[0]
	if year > 0 {
		for _, it := range res {
			if it.FirstAirYear() == year {
				best = it
				break
			}
		}
	}

	details, err := r.c.GetTV(cctx, best.ID)
	if err != nil {
		return tmdb.TVDetails{}, false
	}

	r.mu.Lock()
	r.tvCache[key] = details
	r.mu.Unlock()
	return details, true
}

func (r *Resolver) ResolveEpisodeTitle(ctx context.Context, tvID, season, episode int) (string, bool) {
	if !r.Enabled() {
		return "", false
	}
	key := fmt.Sprintf("e:%d:%d:%d", tvID, season, episode)
	r.mu.Lock()
	if v, ok := r.epCache[key]; ok {
		r.mu.Unlock()
		return v, true
	}
	r.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	name, err := r.c.GetTVEpisodeName(cctx, tvID, season, episode)
	if err != nil || strings.TrimSpace(name) == "" {
		return "", false
	}
	r.mu.Lock()
	r.epCache[key] = name
	r.mu.Unlock()
	return name, true
}

var movieNoiseTokens = []string{
	"web dl", "web-dl", "web", "bluray", "bdrip", "brrip", "hdrip", "dvdrip",
	"1080p", "720p", "2160p", "4k", "x264", "x265", "hevc", "avc", "h264", "h265",
	"ddp", "dd+", "aac", "ac3", "dts", "truehd", "atmos", "remux", "proper", "repack",
	"hdz", "subs", "multi", "espa", "castellano", "dual", "copy",
}

func sanitizeMovieQuery(in string, year int) string {
	return sanitizeQuery(in, year)
}

func sanitizeTVQuery(in string, year int) string {
	s := sanitizeQuery(in, year)
	if s == "" {
		return s
	}
	// Remove trailing episode-like fragments that may survive generic cleaning.
	reEp := regexp.MustCompile(`(?i)\b\d{1,2}x\d{1,2}\b.*$`)
	s = strings.TrimSpace(reEp.ReplaceAllString(s, ""))
	if s == "" {
		return sanitizeQuery(in, year)
	}
	return s
}

func fallbackTVQueries(title string) []string {
	low := strings.ToLower(strings.TrimSpace(title))
	if low == "" {
		return nil
	}
	parts := strings.Fields(regexp.MustCompile(`[^a-z0-9 ]+`).ReplaceAllString(low, " "))
	stop := map[string]bool{
		"the": true, "and": true, "with": true, "from": true, "for": true,
		"el": true, "la": true, "los": true, "las": true, "de": true, "del": true, "en": true, "y": true,
		"desmintiendo": true, "confinamiento": true, "american": true, "lockdown": true,
	}
	seen := map[string]bool{}
	out := make([]string, 0, 3)
	for _, p := range parts {
		if len(p) < 5 || stop[p] {
			continue
		}
		q := strings.ToUpper(p[:1]) + p[1:]
		if !seen[q] {
			seen[q] = true
			out = append(out, q)
		}
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func sanitizeQuery(in string, year int) string {
	s := strings.TrimSpace(in)
	if s == "" {
		return ""
	}
	low := strings.ToLower(s)
	for _, t := range movieNoiseTokens {
		low = strings.ReplaceAll(low, t, " ")
	}
	if year > 0 {
		y := fmt.Sprintf("%d", year)
		low = strings.ReplaceAll(low, "("+y+")", " ")
		low = strings.ReplaceAll(low, y, " ")
	}
	// remove leftover symbols and collapse spaces
	re := regexp.MustCompile(`[^a-z0-9& ]+`)
	low = re.ReplaceAllString(low, " ")
	low = strings.TrimSpace(strings.Join(strings.Fields(low), " "))
	if low == "" {
		return strings.TrimSpace(in)
	}
	parts := strings.Fields(low)
	for i, p := range parts {
		if len(p) > 1 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		} else {
			parts[i] = strings.ToUpper(p)
		}
	}
	return strings.Join(parts, " ")
}
