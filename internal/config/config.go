package config

import (
	"encoding/json"
	"errors"
	"os"
)

type Paths struct {
	HostRoot   string `json:"host_root"`
	MountPoint string `json:"mount_point"`
	NzbInbox   string `json:"nzb_inbox"`
	MediaInbox string `json:"media_inbox"`
	CacheDir   string `json:"cache_dir"`

	// CacheMaxBytes is a best-effort size limit for /cache contents.
	CacheMaxBytes int64 `json:"cache_max_bytes"`
}

type Server struct {
	Addr string `json:"addr"`
}

type Runner struct {
	Mode string `json:"mode"` // "stub" or "exec" (dev)
}

type Config struct {
	Server Server `json:"server"`
	Paths  Paths  `json:"paths"`
	Runner Runner `json:"runner"`

	NgPost   NgPost           `json:"ngpost"`
	Download DownloadProvider `json:"download"`

	Library  Library  `json:"library"`
	Metadata Metadata `json:"metadata"`
}

func Default() Config {
	return Config{
		Server: Server{Addr: ":1516"},
		Paths: Paths{
			HostRoot:      "/host",
			MountPoint:    "/host/mount",
			NzbInbox:      "/host/inbox/nzb",
			MediaInbox:    "/host/inbox/media",
			CacheDir:      "/cache",
			CacheMaxBytes: 50 * 1024 * 1024 * 1024,
		},
		Runner:   Runner{Mode: "stub"},
		NgPost:   NgPost{Enabled: false, Port: 563, SSL: true, Connections: 20, Threads: 2, OutputDir: "/host/inbox/nzb", Obfuscate: true},
		Download: DownloadProvider{Enabled: false, Port: 563, SSL: true, Connections: 20, PrefetchSegments: 2},
		Library:  (Library{Enabled: true}).withDefaults(),
		Metadata: (Metadata{}).withDefaults(),
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	// Fill defaults for nested configs that may be missing
	cfg.Library = cfg.Library.withDefaults()
	cfg.Metadata = cfg.Metadata.withDefaults()
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Server.Addr == "" {
		return errors.New("server.addr required")
	}
	if c.Paths.MountPoint == "" {
		return errors.New("paths.mount_point required")
	}
	return nil
}
