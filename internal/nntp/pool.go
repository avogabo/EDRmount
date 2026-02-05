package nntp

import (
	"context"
	"sync"
	"time"
)

type Pool struct {
	cfg Config
	max int

	mu      sync.Mutex
	created int
	idle    chan *Client
}

func NewPool(cfg Config, max int) *Pool {
	if max <= 0 {
		max = 1
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &Pool{cfg: cfg, max: max, idle: make(chan *Client, max)}
}

func (p *Pool) dialAuthed(ctx context.Context) (*Client, error) {
	c, err := Dial(ctx, p.cfg)
	if err != nil {
		return nil, err
	}
	if err := c.Auth(); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (p *Pool) Acquire(ctx context.Context) (*Client, error) {
	// Try an idle client first
	select {
	case c := <-p.idle:
		// Validate quickly; if dead, drop and retry
		if err := c.Noop(); err != nil {
			_ = c.Close()
			p.mu.Lock()
			p.created--
			p.mu.Unlock()
			break
		}
		return c, nil
	default:
	}

	p.mu.Lock()
	if p.created < p.max {
		p.created++
		p.mu.Unlock()
		c, err := p.dialAuthed(ctx)
		if err != nil {
			p.mu.Lock()
			p.created--
			p.mu.Unlock()
			return nil, err
		}
		return c, nil
	}
	p.mu.Unlock()

	// Wait for an idle client
	select {
	case c := <-p.idle:
		if err := c.Noop(); err != nil {
			_ = c.Close()
			p.mu.Lock()
			p.created--
			p.mu.Unlock()
			// last attempt: dial new if possible
			p.mu.Lock()
			if p.created < p.max {
				p.created++
				p.mu.Unlock()
				return p.dialAuthed(ctx)
			}
			p.mu.Unlock()
			return nil, err
		}
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *Pool) Release(c *Client) {
	if c == nil {
		return
	}
	// If connection is dead, drop it
	if err := c.Noop(); err != nil {
		_ = c.Close()
		p.mu.Lock()
		p.created--
		p.mu.Unlock()
		return
	}
	select {
	case p.idle <- c:
	default:
		_ = c.Close()
		p.mu.Lock()
		p.created--
		p.mu.Unlock()
	}
}
