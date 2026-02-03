package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/gaby/EDRmount/internal/api"
	"github.com/gaby/EDRmount/internal/config"
)

func main() {
	var cfgPath string
	flag.StringVar(&cfgPath, "config", "/config/config.json", "path to config file (json)")
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

	log.Printf("EDRmount listening on %s", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, srv.Handler()); err != nil {
		log.Fatalf("server: %v", err)
	}
}
