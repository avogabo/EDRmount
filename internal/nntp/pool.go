package nntp

import (
	"context"
	"sync"
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
	return &Pool{cfg: cfg, max: max, idle: make(chan *Client, max)}
}

func (p *Pool) Acquire(ctx context.Context) (*Client, error) {
	select {
	case c := <-p.idle:
		return c, nil
	default:
	}

	p.mu.Lock()
	if p.created < p.max {
		p.created++
		p.mu.Unlock()
		c, err := Dial(ctx, p.cfg)
		if err != nil {
			p.mu.Lock()
			p.created--
			p.mu.Unlock()
			return nil, err
		}
		if err := c.Auth(); err != nil {
			_ = c.Close()
			p.mu.Lock()
			p.created--
			p.mu.Unlock()
			return nil, err
		}
		return c, nil
	}
	p.mu.Unlock()

	// wait for an idle client
	select {
	case c := <-p.idle:
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *Pool) Release(c *Client) {
	if c == nil {
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
