package library

import (
	"context"
	"fmt"
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
	key := fmt.Sprintf("m:%s:%d", strings.ToLower(strings.TrimSpace(title)), year)
	r.mu.Lock()
	if v, ok := r.movieCache[key]; ok {
		r.mu.Unlock()
		return v, true
	}
	r.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	res, err := r.c.SearchMovie(cctx, title, year)
	if err != nil || len(res) == 0 {
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
	key := fmt.Sprintf("t:%s:%d", strings.ToLower(strings.TrimSpace(title)), year)
	r.mu.Lock()
	if v, ok := r.tvCache[key]; ok {
		r.mu.Unlock()
		return v, true
	}
	r.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	res, err := r.c.SearchTV(cctx, title, year)
	if err != nil || len(res) == 0 {
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
