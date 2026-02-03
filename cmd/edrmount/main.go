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

	srv := api.New(cfg)
	log.Printf("EDRmount listening on %s", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, srv.Handler()); err != nil {
		log.Fatalf("server: %v", err)
	}
}
