package runner

import (
	"errors"
	"os"
	"time"
)

// acquireHealthLock creates a lock file atomically (O_EXCL). If it exists and is stale, it is removed and retried.
func acquireHealthLock(lockPath string, ttl time.Duration) error {
	if lockPath == "" {
		return errors.New("lock path required")
	}
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}

	// If lock exists and is stale, remove it.
	if st, err := os.Stat(lockPath); err == nil {
		age := time.Since(st.ModTime())
		if age > ttl {
			_ = os.Remove(lockPath)
		} else {
			return errors.New("locked (another repair in progress)")
		}
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		// someone else won the race
		return errors.New("locked (another repair in progress)")
	}
	_ = f.Close()
	return nil
}
