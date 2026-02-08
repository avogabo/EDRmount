package nntp

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"
)

type Config struct {
	Host    string
	Port    int
	SSL     bool
	User    string
	Pass    string
	Timeout time.Duration
}

type Client struct {
	cfg  Config
	conn net.Conn
	r    *bufio.Reader
}

func (c *Client) setDeadline() {
	_ = c.conn.SetDeadline(time.Now().Add(c.cfg.Timeout))
}

func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Port == 0 {
		cfg.Port = 119
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	d := &net.Dialer{Timeout: cfg.Timeout}
	var c net.Conn
	var err error
	if cfg.SSL {
		tlsCfg := &tls.Config{ServerName: cfg.Host}
		td := &tls.Dialer{NetDialer: d, Config: tlsCfg}
		c, err = td.DialContext(ctx, "tcp", addr)
	} else {
		c, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	cl := &Client{cfg: cfg, conn: c, r: bufio.NewReaderSize(c, 1024*1024)}
	// read greeting
	cl.setDeadline()
	line, err := cl.readLine()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	if !strings.HasPrefix(line, "200") && !strings.HasPrefix(line, "201") {
		_ = c.Close()
		return nil, fmt.Errorf("unexpected greeting: %s", line)
	}
	return cl, nil
}

func (c *Client) Close() error {
	_ = c.send("QUIT")
	return c.conn.Close()
}

func (c *Client) readLine() (string, error) {
	c.setDeadline()
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	return line, nil
}

func (c *Client) send(cmd string) error {
	c.setDeadline()
	_, err := c.conn.Write([]byte(cmd + "\r\n"))
	return err
}

func (c *Client) Auth() error {
	if c.cfg.User == "" {
		return nil
	}
	if err := c.send("AUTHINFO USER " + c.cfg.User); err != nil {
		return err
	}
	line, err := c.readLine()
	if err != nil {
		return err
	}
	// 381 = password required, 281 = ok (no password needed)
	if strings.HasPrefix(line, "281") {
		return nil
	}
	if !strings.HasPrefix(line, "381") {
		return fmt.Errorf("auth user failed: %s", line)
	}
	if err := c.send("AUTHINFO PASS " + c.cfg.Pass); err != nil {
		return err
	}
	line, err = c.readLine()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "281") {
		return fmt.Errorf("auth pass failed: %s", line)
	}
	return nil
}

func (c *Client) Noop() error {
	if err := c.send("STAT"); err != nil {
		return err
	}
	line, err := c.readLine()
	if err != nil {
		return err
	}
	// 223 is "article exists" but needs a message-id; some servers reply 500.
	// We mainly care about detecting dead sockets; accept any response line.
	_ = line
	return nil
}

// BodyByMessageID fetches the body lines (dot-terminated) for a message-id.
// Returns raw lines (without CRLF), with dot-stuffing already unescaped.
func (c *Client) normalizeMessageID(messageID string) string {
	messageID = strings.TrimSpace(messageID)
	if !strings.HasPrefix(messageID, "<") {
		messageID = "<" + messageID
	}
	if !strings.HasSuffix(messageID, ">") {
		messageID = messageID + ">"
	}
	return messageID
}

// StatByMessageID checks if an article exists without downloading its body.
// Returns nil if the server reports it exists.
func (c *Client) StatByMessageID(messageID string) error {
	c.setDeadline()
	messageID = c.normalizeMessageID(messageID)
	if err := c.send("STAT " + messageID); err != nil {
		return err
	}
	line, err := c.readLine()
	if err != nil {
		return err
	}
	// 223 = article exists
	if strings.HasPrefix(line, "223") {
		return nil
	}
	return fmt.Errorf("STAT failed: %s", line)
}

func (c *Client) BodyByMessageID(messageID string) ([]string, error) {
	c.setDeadline()
	messageID = c.normalizeMessageID(messageID)
	if err := c.send("BODY " + messageID); err != nil {
		return nil, err
	}
	line, err := c.readLine()
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(line, "222") {
		return nil, fmt.Errorf("BODY failed: %s", line)
	}
	out := make([]string, 0, 1024)
	for {
		l, err := c.readLine()
		if err != nil {
			return nil, err
		}
		if l == "." {
			break
		}
		if strings.HasPrefix(l, "..") {
			l = l[1:]
		}
		out = append(out, l)
	}
	return out, nil
}
