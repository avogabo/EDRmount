package config

type HealthScanConfig struct {
	Enabled bool `json:"enabled"`

	// IntervalHours is how long to wait AFTER a complete scan run finishes before starting a new full run.
	IntervalHours int `json:"interval_hours"`

	// ChunkEveryHours controls how long to wait after a chunk finishes (due to budget) before resuming.
	// Default: 24h.
	ChunkEveryHours int `json:"chunk_every_hours"`

	// MaxDurationMinutes is the time budget per scan chunk (e.g. 120-180 minutes).
	MaxDurationMinutes int `json:"max_duration_minutes"`

	// AutoRepair enqueues a health_repair_nzb job for each BROKEN NZB found.
	AutoRepair bool `json:"auto_repair"`
}

type HealthLockConfig struct {
	// LockTTLHours is the time after which a lock file is considered stale and can be taken over.
	LockTTLHours int `json:"lock_ttl_hours"`
}

type HealthConfig struct {
	Enabled bool `json:"enabled"`

	// BackupDir is where original NZBs are moved before replacing.
	// IMPORTANT: This should be local-only (not in the shared RAW NZB tree).
	// If empty, defaults to "/cache/health-bak".
	BackupDir string `json:"backup_dir"`

	Scan HealthScanConfig `json:"scan"`
	Lock HealthLockConfig `json:"lock"`
}
