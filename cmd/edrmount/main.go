package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/gaby/EDRmount/internal/api"
	"github.com/gaby/EDRmount/internal/backup"
	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/fusefs"
	"github.com/gaby/EDRmount/internal/health"
	"github.com/gaby/EDRmount/internal/runner"
	"github.com/gaby/EDRmount/internal/watch"
)

func main() {
	var cfgPath string
	var enableFuse bool
	flag.StringVar(&cfgPath, "config", "/config/config.json", "path to config file (json)")
	flag.BoolVar(&enableFuse, "fuse", true, "enable FUSE mounts at <mount_point>/*")
	flag.Parse()

	// First-run UX: if config.json is missing, create a safe default so the service can boot.
	if err := config.EnsureConfigFile(cfgPath); err != nil {
		log.Fatalf("config bootstrap: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config validate: %v", err)
	}

	autoActivateFileBotLicense(cfg)

	dbPath := "/config/edrmount.db"
	// One-shot DB reset marker (created by API/UI): delete ONLY the DB files, keep config.json.
	resetMarker := "/config/.reset-db"
	if _, err := os.Stat(resetMarker); err == nil {
		_ = os.Remove(dbPath)
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
		_ = os.Remove(resetMarker)
	}

	srv, closeFn, err := api.New(cfg, api.Options{ConfigPath: cfgPath, DBPath: dbPath})
	if err != nil {
		log.Fatalf("api init: %v", err)
	}
	defer func() {
		if closeFn != nil {
			_ = closeFn()
		}
	}()

	// Start background watcher + runner.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if srvJobs := srv.Jobs(); srvJobs != nil {
		// Start watchers (NZB/media) and runner (job executor) independently.
		if cfg.Watch.NZB.Enabled || cfg.Watch.Media.Enabled {
			w := watch.New(srvJobs, cfg.Watch.NZB, cfg.Watch.Media)
			go w.Run(ctx)
		}

		if cfg.Runner.Enabled {
			r := runner.New(srvJobs)
			r.Mode = cfg.Runner.Mode
			r.GetConfig = srv.Config
			go r.Run(ctx)
		}

		// Backup scheduler (reads latest config from the API server)
		sched := &backup.Scheduler{
			DBPath: dbPath,
			Cfg: func() backup.Config {
				c := srv.Config().Backups
				return backup.Config{
					Enabled:    c.Enabled,
					Dir:        c.Dir,
					EveryMins:  c.EveryMins,
					Keep:       c.Keep,
					CompressGZ: c.CompressGZ,
				}
			},
		}
		go sched.Run(ctx)

		// Health scan scheduler (enqueues health_scan_nzb according to config)
		hs := &health.Scheduler{
			Jobs: srvJobs,
			Cfg: func() config.HealthConfig {
				return srv.Config().Health
			},
		}
		go hs.Run(ctx)

		if enableFuse {
			if cfg.Library.Enabled {
				if _, err := fusefs.MountLibraryAuto(ctx, cfg, srvJobs); err != nil {
					log.Printf("FUSE library-auto mount failed: %v", err)
				} else {
					log.Printf("FUSE library-auto mounted at %s/library-auto", cfg.Paths.MountPoint)
				}
				if _, err := fusefs.MountLibraryManual(ctx, cfg, srvJobs); err != nil {
					log.Printf("FUSE library-manual mount failed: %v", err)
				} else {
					log.Printf("FUSE library-manual mounted at %s/library-manual", cfg.Paths.MountPoint)
				}
			}
		}
	}

	log.Printf("EDRmount listening on %s", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, srv.Handler()); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func autoActivateFileBotLicense(cfg config.Config) {
	rn := cfg.Rename
	if strings.ToLower(strings.TrimSpace(rn.Provider)) != "filebot" || !rn.FileBot.Enabled {
		return
	}
	bin := strings.TrimSpace(rn.FileBot.Binary)
	if bin == "" {
		bin = "/usr/local/bin/filebot"
	}
	licensePath := strings.TrimSpace(rn.FileBot.LicensePath)
	if licensePath == "" {
		licensePath = "/config/filebot/license.psm"
	}
	if _, err := os.Stat(bin); err != nil {
		return
	}
	if _, err := os.Stat(licensePath); err != nil {
		return
	}

	out, err := exec.Command(bin, "-script", "fn:sysinfo").CombinedOutput()
	if err == nil {
		s := strings.ToUpper(string(out))
		if strings.Contains(s, "LICENSE:") && !strings.Contains(s, "UNREGISTERED") {
			return
		}
	}

	cmd := exec.Command(bin, "--license", licensePath)
	licOut, licErr := cmd.CombinedOutput()
	if licErr != nil {
		log.Printf("filebot license auto-activate WARN: %v", licErr)
		if len(licOut) > 0 {
			log.Printf("filebot license auto-activate output: %s", strings.TrimSpace(string(licOut)))
		}
		return
	}
	log.Printf("filebot license auto-activated from %s", licensePath)
}
