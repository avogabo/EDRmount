package streamer

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/nntp"
	"github.com/gaby/EDRmount/internal/yenc"
)

type SegmentLocator struct {
	ImportID  string
	FileIdx   int
	Number    int
	Bytes     int64
	MessageID string
}

type FileLayout struct {
	ImportID string
	FileIdx  int
	Total    int64
	Segs     []SegmentLocator // sorted by Number
	Offsets  []int64          // starting byte offset for each seg (same index as Segs)
}

func buildLayout(segs []segRow, importID string, fileIdx int) (*FileLayout, error) {
	sort.Slice(segs, func(i, j int) bool { return segs[i].Number < segs[j].Number })
	layout := &FileLayout{ImportID: importID, FileIdx: fileIdx}
	layout.Segs = make([]SegmentLocator, 0, len(segs))
	layout.Offsets = make([]int64, 0, len(segs))
	var off int64 = 0
	for _, s := range segs {
		layout.Offsets = append(layout.Offsets, off)
		layout.Segs = append(layout.Segs, SegmentLocator{ImportID: importID, FileIdx: fileIdx, Number: s.Number, Bytes: s.Bytes, MessageID: s.MessageID})
		off += s.Bytes
	}
	layout.Total = off
	return layout, nil
}

func (s *Streamer) segCachePath(importID string, fileIdx int, segNum int, messageID string) string {
	// include message-id hash to avoid collisions if same seg num changes across reimports
	h := sha1.Sum([]byte(messageID))
	name := fmt.Sprintf("%06d_%s.bin", segNum, hex.EncodeToString(h[:6]))
	return filepath.Join(s.cacheDir, "rawseg", importID, fmt.Sprintf("%d", fileIdx), name)
}

func (s *Streamer) ensureSegment(ctx context.Context, seg SegmentLocator) (string, error) {
	p := s.segCachePath(seg.ImportID, seg.FileIdx, seg.Number, seg.MessageID)
	if st, err := os.Stat(p); err == nil && st.Size() > 0 {
		return p, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}

	// Download + decode
	cl, err := nntp.Dial(ctx, nntp.Config{Host: s.cfg.Host, Port: s.cfg.Port, SSL: s.cfg.SSL, User: s.cfg.User, Pass: s.cfg.Pass, Timeout: 15 * time.Second})
	if err != nil {
		return "", err
	}
	defer cl.Close()
	if err := cl.Auth(); err != nil {
		return "", err
	}
	log.Printf("rawseg: import=%s fileIdx=%d seg=%d fetching", seg.ImportID, seg.FileIdx, seg.Number)
	lines, err := cl.BodyByMessageID(seg.MessageID)
	if err != nil {
		return "", err
	}
	data, _, _, _, err := yenc.DecodePart(lines)
	if err != nil {
		return "", err
	}
	log.Printf("rawseg: import=%s fileIdx=%d seg=%d decoded=%d bytes", seg.ImportID, seg.FileIdx, seg.Number, len(data))

	tmp := p + ".part"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		return "", err
	}
	return p, nil
}

// StreamRange writes exactly [start,end] inclusive from the logical file.
func (s *Streamer) StreamRange(ctx context.Context, importID string, fileIdx int, filename string, start, end int64, w io.Writer, prefetch int) error {
	// Load segments from DB
	qctx, qcancel := context.WithTimeout(ctx, 5*time.Second)
	defer qcancel()
	rows, err := s.jobs.DB().SQL.QueryContext(qctx, `SELECT number,bytes,message_id FROM nzb_segments WHERE import_id=? AND file_idx=? ORDER BY number ASC`, importID, fileIdx)
	if err != nil {
		return err
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
		return fmt.Errorf("no segments")
	}
	layout, _ := buildLayout(segs, importID, fileIdx)
	if start < 0 {
		start = 0
	}
	if end >= layout.Total {
		end = layout.Total - 1
	}
	if end < start {
		return fmt.Errorf("invalid range")
	}

	// find starting segment index
	idx := sort.Search(len(layout.Offsets), func(i int) bool {
		return layout.Offsets[i] > start
	}) - 1
	if idx < 0 {
		idx = 0
	}

	// prefetch next segments (best-effort) - fire and forget
	if prefetch > 0 {
		for j := 1; j <= prefetch; j++ {
			k := idx + j
			if k >= 0 && k < len(layout.Segs) {
				seg := layout.Segs[k]
				go func() {
					ctx2, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					_, _ = s.ensureSegment(ctx2, seg)
				}()
			}
		}
	}

	remainingStart := start
	remainingEnd := end
	for i := idx; i < len(layout.Segs); i++ {
		seg := layout.Segs[i]
		segStart := layout.Offsets[i]
		segEnd := segStart + seg.Bytes - 1
		if remainingStart > segEnd {
			continue
		}
		if remainingEnd < segStart {
			break
		}
		// ensure segment cached
		p, err := s.ensureSegment(ctx, seg)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		// compute slice within this segment
		s := remainingStart
		if s < segStart {
			s = segStart
		}
		e := remainingEnd
		if e > segEnd {
			e = segEnd
		}
		// seek relative
		if _, err := f.Seek(s-segStart, 0); err != nil {
			_ = f.Close()
			return err
		}
		if _, err := io.CopyN(w, f, (e-s)+1); err != nil {
			_ = f.Close()
			return err
		}
		_ = f.Close()
		if e == remainingEnd {
			break
		}
	}
	return nil
}
