package streamer

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
	"github.com/gaby/EDRmount/internal/nntp"
	"github.com/gaby/EDRmount/internal/yenc"
)

type Streamer struct {
	cfg      config.DownloadProvider
	jobs     *jobs.Store
	cacheDir string
}

func New(cfg config.DownloadProvider, j *jobs.Store, cacheDir string) *Streamer {
	return &Streamer{cfg: cfg, jobs: j, cacheDir: cacheDir}
}

type segRow struct {
	Number    int
	Bytes     int64
	MessageID string
}

func (s *Streamer) EnsureFile(ctx context.Context, importID string, fileIdx int, filename string) (string, error) {
	// cache path
	base := filepath.Join(s.cacheDir, "raw", importID)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	outPath := filepath.Join(base, filename)
	if st, err := os.Stat(outPath); err == nil && st.Size() > 0 {
		return outPath, nil
	}

	if !s.cfg.Enabled {
		return "", fmt.Errorf("download provider disabled")
	}
	if s.cfg.Host == "" || s.cfg.User == "" || s.cfg.Pass == "" {
		return "", fmt.Errorf("download provider not configured")
	}

	// Load segments
	rows, err := s.jobs.DB().SQL.QueryContext(ctx, `SELECT number,bytes,message_id FROM nzb_segments WHERE import_id=? AND file_idx=? ORDER BY number ASC`, importID, fileIdx)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	segs := make([]segRow, 0)
	for rows.Next() {
		var r segRow
		if err := rows.Scan(&r.Number, &r.Bytes, &r.MessageID); err != nil {
			continue
		}
		r.MessageID = strings.TrimSpace(r.MessageID)
		segs = append(segs, r)
	}
	if len(segs) == 0 {
		return "", fmt.Errorf("no segments for file")
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].Number < segs[j].Number })

	cl, err := nntp.Dial(ctx, nntp.Config{Host: s.cfg.Host, Port: s.cfg.Port, SSL: s.cfg.SSL, User: s.cfg.User, Pass: s.cfg.Pass, Timeout: 15 * time.Second})
	if err != nil {
		return "", err
	}
	defer cl.Close()
	if err := cl.Auth(); err != nil {
		return "", err
	}

	// Write temp then rename
	tmp := outPath + ".part"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()

	for _, seg := range segs {
		log.Printf("raw: import=%s fileIdx=%d seg=%d fetching", importID, fileIdx, seg.Number)
		lines, err := cl.BodyByMessageID(seg.MessageID)
		if err != nil {
			return "", err
		}
		data, _, _, _, err := yenc.DecodePart(lines)
		log.Printf("raw: import=%s fileIdx=%d seg=%d decoded=%d bytes", importID, fileIdx, seg.Number, len(data))
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			return "", err
		}
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, outPath); err != nil {
		return "", err
	}
	return outPath, nil
}
