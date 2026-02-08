package backup

import (
	"context"
	"time"
)

type ConfigProvider func() Config

type Scheduler struct {
	DBPath string
	Cfg    ConfigProvider

	lastKey string
	lastRun time.Time
}

func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cfg := s.Cfg()
			if !cfg.Enabled || cfg.EveryMins <= 0 {
				continue
			}
			key := cfg.Dir + ":" + itoa(cfg.EveryMins) + ":" + itoa(cfg.Keep) + ":" + boolKey(cfg.CompressGZ)
			if s.lastKey != key {
				s.lastKey = key
				s.lastRun = time.Time{}
			}

			if !s.lastRun.IsZero() && time.Since(s.lastRun) < time.Duration(cfg.EveryMins)*time.Minute {
				continue
			}

			_, err := RunOnce(ctx, s.DBPath, cfg.Dir, cfg.CompressGZ)
			if err == nil {
				s.lastRun = time.Now()
				Rotate(cfg.Dir, cfg.Keep)
			}
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	buf := make([]byte, 0, 12)
	for i > 0 {
		buf = append(buf, byte('0'+(i%10)))
		i /= 10
	}
	if neg {
		buf = append(buf, '-')
	}
	// reverse
	for l, r := 0, len(buf)-1; l < r; l, r = l+1, r-1 {
		buf[l], buf[r] = buf[r], buf[l]
	}
	return string(buf)
}

func boolKey(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
