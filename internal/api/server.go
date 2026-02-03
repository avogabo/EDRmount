package api

import (
	"embed"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/version"
)

//go:embed webui/*
var uiFS embed.FS

type Server struct {
	cfg config.Config
	mux *http.ServeMux
}

func New(cfg config.Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}

	// Health
	s.mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"time":    time.Now().Format(time.RFC3339),
			"version": version.Version,
			"commit":  version.Commit,
		})
	})

	// Basic API (UI consumes this)
	s.mux.HandleFunc("/api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(s.cfg)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// UI static (placeholder for now)
	ui := http.FS(uiFS)
	s.mux.Handle("/", http.FileServer(ui))

	return s
}

func (s *Server) Handler() http.Handler { return s.mux }
