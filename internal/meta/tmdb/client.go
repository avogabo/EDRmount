package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.themoviedb.org/3"

type Client struct {
	// APIKey is the TMDB v3 API key. Keep it secret.
	APIKey string

	// BaseURL defaults to https://api.themoviedb.org/3
	BaseURL string

	// Language is an optional TMDB language code (e.g. "es-ES", "en-US").
	Language string

	HTTP *http.Client
}

func New(apiKey string) *Client {
	return &Client{
		APIKey:  apiKey,
		BaseURL: defaultBaseURL,
		HTTP: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) validate() error {
	if c == nil {
		return errors.New("tmdb client is nil")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return errors.New("tmdb api key missing")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		c.BaseURL = defaultBaseURL
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	return nil
}

func (c *Client) SearchMovie(ctx context.Context, query string, year int) ([]MovieSearchResult, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("query", query)
	if year > 0 {
		q.Set("year", strconv.Itoa(year))
	}
	var out SearchMovieResponse
	if err := c.getJSON(ctx, "/search/movie", q, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

func (c *Client) GetMovie(ctx context.Context, id int) (MovieDetails, error) {
	if err := c.validate(); err != nil {
		return MovieDetails{}, err
	}
	var out MovieDetails
	if err := c.getJSON(ctx, fmt.Sprintf("/movie/%d", id), nil, &out); err != nil {
		return MovieDetails{}, err
	}
	return out, nil
}

func (c *Client) SearchTV(ctx context.Context, query string, firstAirYear int) ([]TVSearchResult, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("query", query)
	if firstAirYear > 0 {
		q.Set("first_air_date_year", strconv.Itoa(firstAirYear))
	}
	var out SearchTVResponse
	if err := c.getJSON(ctx, "/search/tv", q, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

func (c *Client) GetTV(ctx context.Context, id int) (TVDetails, error) {
	if err := c.validate(); err != nil {
		return TVDetails{}, err
	}
	var out TVDetails
	if err := c.getJSON(ctx, fmt.Sprintf("/tv/%d", id), nil, &out); err != nil {
		return TVDetails{}, err
	}
	return out, nil
}

func (c *Client) GetTVSeason(ctx context.Context, tvID int, seasonNumber int) (TVSeasonDetails, error) {
	if err := c.validate(); err != nil {
		return TVSeasonDetails{}, err
	}
	var out TVSeasonDetails
	path := fmt.Sprintf("/tv/%d/season/%d", tvID, seasonNumber)
	if err := c.getJSON(ctx, path, nil, &out); err != nil {
		return TVSeasonDetails{}, err
	}
	return out, nil
}

// GetTVEpisodeName resolves an episode name by requesting the season payload.
// This avoids needing extra /episode endpoints.
func (c *Client) GetTVEpisodeName(ctx context.Context, tvID int, seasonNumber int, episodeNumber int) (string, error) {
	season, err := c.GetTVSeason(ctx, tvID, seasonNumber)
	if err != nil {
		return "", err
	}
	for _, ep := range season.Episodes {
		if ep.EpisodeNumber == episodeNumber {
			return ep.Name, nil
		}
	}
	return "", fmt.Errorf("episode not found: tv=%d season=%d episode=%d", tvID, seasonNumber, episodeNumber)
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, dst any) error {
	base := strings.TrimRight(c.BaseURL, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	u, err := url.Parse(base + path)
	if err != nil {
		return err
	}

	values := u.Query()
	values.Set("api_key", c.APIKey)
	if c.Language != "" {
		values.Set("language", c.Language)
	}
	for k, vv := range q {
		for _, v := range vv {
			values.Add(k, v)
		}
	}
	u.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	// Avoid adding headers that might be logged elsewhere; keep minimal.
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2MB max
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Do not include full URL (contains api_key). Keep a safe error.
		return fmt.Errorf("tmdb http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return err
	}
	return nil
}
