package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/gaby/EDRmount/internal/api"
	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/fusefs"
	"github.com/gaby/EDRmount/internal/runner"
	"github.com/gaby/EDRmount/internal/watch"
)

func main() {
	var cfgPath string
	var enableFuse bool
	flag.StringVar(&cfgPath, "config", "/config/config.json", "path to config file (json)")
	flag.BoolVar(&enableFuse, "fuse", false, "enable FUSE raw mount at <mount_point>/raw")
	flag.Parse()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config validate: %v", err)
	}

	dbPath := "/config/edrmount.db"
	srv, closeFn, err := api.New(cfg, api.Options{ConfigPath: cfgPath, DBPath: dbPath})
	if err != nil {
		log.Fatalf("api init: %v", err)
	}
	defer func() {
		if closeFn != nil {
			_ = closeFn()
		}
	}()

	// Start background watcher + runner (stubs for now).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if srvJobs := srv.Jobs(); srvJobs != nil {
		w := watch.New(srvJobs, cfg.Paths.NzbInbox, cfg.Paths.MediaInbox)
		go w.Run(ctx)

		r := runner.New(srvJobs)
		r.Mode = cfg.Runner.Mode
		r.NgPost = cfg.NgPost
		go r.Run(ctx)

		if enableFuse {
			if _, err := fusefs.MountRaw(ctx, cfg, srvJobs); err != nil {
				log.Printf("FUSE raw mount failed: %v", err)
			} else {
				log.Printf("FUSE raw mounted at %s/raw", cfg.Paths.MountPoint)
			}

			if cfg.Library.Enabled {
				if _, err := fusefs.MountLibraryAuto(ctx, cfg, srvJobs); err != nil {
					log.Printf("FUSE library-auto mount failed: %v", err)
				} else {
					log.Printf("FUSE library-auto mounted at %s/library-auto", cfg.Paths.MountPoint)
				}
				// library-manual mount will be added next
			}
		}
	}

	log.Printf("EDRmount listening on %s", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, srv.Handler()); err != nil {
		log.Fatalf("server: %v", err)
	}
}
