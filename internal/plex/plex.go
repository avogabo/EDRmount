package plex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Token   string

	HTTP *http.Client
}

func New(baseURL, token string) *Client {
	c := &Client{BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), Token: strings.TrimSpace(token)}
	c.HTTP = &http.Client{Timeout: 12 * time.Second}
	return c
}

func (c *Client) Enabled() bool {
	return c != nil && c.BaseURL != "" && c.Token != ""
}

// RefreshPath asks Plex to refresh a specific path.
// It first tries the directory path (recommended), and optionally the exact file path.
//
// Uses /library/sections/all/refresh?path=... which works across libraries.
func (c *Client) RefreshPath(ctx context.Context, plexPath string, fallbackFile bool) error {
	if !c.Enabled() {
		return fmt.Errorf("plex not configured")
	}
	plexPath = filepath.Clean(strings.TrimSpace(plexPath))
	if plexPath == "." || plexPath == "/" || plexPath == "" {
		return fmt.Errorf("invalid plex path")
	}

	try := []string{plexPath}
	if fallbackFile {
		// include original too (some callers pass dir already; ensure no duplicates)
		if filepath.Ext(plexPath) == "" {
			// nothing
		}
	}

	// always try directory first if it looks like a file
	if ext := filepath.Ext(plexPath); ext != "" {
		dir := filepath.Dir(plexPath)
		if dir != "." && dir != "/" {
			try = append([]string{dir}, try...)
		}
	}

	seen := map[string]bool{}
	for _, p := range try {
		p = filepath.Clean(p)
		if seen[p] {
			continue
		}
		seen[p] = true
		if err := c.refreshOnce(ctx, p); err == nil {
			return nil
		} else if !fallbackFile {
			return err
		}
		// if fallback allowed, keep trying
	}
	return fmt.Errorf("plex refresh failed")
}

func (c *Client) refreshOnce(ctx context.Context, plexPath string) error {
	u, err := url.Parse(c.BaseURL + "/library/sections/all/refresh")
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("path", plexPath)
	q.Set("X-Plex-Token", c.Token)
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("plex refresh status=%d", resp.StatusCode)
	}
	return nil
}
