package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// EnsureConfigFile makes sure the config file exists.
//
// If the file does not exist, it writes a safe default config that allows EDRmount
// to boot so the user can finish configuration via the Web UI.
//
// It never overwrites an existing file.
func EnsureConfigFile(path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	// Make parent dir.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	// Safe defaults for first boot: no runner/watch, no creds.
	cfg := Default()
	cfg.Runner.Enabled = false
	cfg.Watch.NZB.Enabled = false
	cfg.Watch.Media.Enabled = false
	cfg.NgPost.Enabled = false
	cfg.Download.Enabled = false
	cfg.Plex.Enabled = false
	// Health can stay enabled but scanning is off by default.
	cfg.Health.Scan.Enabled = false

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	// Write with restrictive perms; user can loosen on host side if desired.
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write default config: %w", err)
	}
	return nil
}
