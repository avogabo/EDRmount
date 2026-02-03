package api

import (
	"embed"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/db"
	"github.com/gaby/EDRmount/internal/jobs"
	"github.com/gaby/EDRmount/internal/version"
)

//go:embed webui/*
var uiFS embed.FS

type Server struct {
	cfg     config.Config
	cfgPath string
	mux     *http.ServeMux
	jobs    *jobs.Store
}

type Options struct {
	ConfigPath string
	DBPath     string
}

func New(cfg config.Config, opts Options) (*Server, func() error, error) {
	s := &Server{cfg: cfg, cfgPath: opts.ConfigPath, mux: http.NewServeMux()}

	closers := []func() error{}
	if opts.DBPath != "" {
		d, err := db.Open(opts.DBPath)
		if err != nil {
			return nil, nil, err
		}
		closers = append(closers, d.Close)
		s.jobs = jobs.NewStore(d)
	}

	closeFn := func() error {
		var first error
		for i := len(closers) - 1; i >= 0; i-- {
			if err := closers[i](); err != nil && first == nil {
				first = err
			}
		}
		return first
	}

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
		case http.MethodPut:
			b, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			var next config.Config
			if err := json.Unmarshal(b, &next); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			if err := next.Validate(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			// Persist to disk and apply in-memory
			if err := config.Save(s.cfgPath, next); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			s.cfg = next
			_ = json.NewEncoder(w).Encode(s.cfg)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Jobs API
	s.mux.HandleFunc("/api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "jobs db not configured"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, err := s.jobs.List(r.Context(), 100)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(items)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	s.mux.HandleFunc("/api/v1/jobs/enqueue/import", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "jobs db not configured"})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var payload struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		job, err := s.jobs.Enqueue(r.Context(), jobs.TypeImport, payload)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(job)
	})

	s.mux.HandleFunc("/api/v1/jobs/enqueue/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "jobs db not configured"})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var payload struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		job, err := s.jobs.Enqueue(r.Context(), jobs.TypeUpload, payload)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(job)
	})

	// Extra routes
	s.registerJobLogRoutes()

	// UI static (placeholder for now)
	ui := http.FS(uiFS)
	s.mux.Handle("/", http.FileServer(ui))

	return s, closeFn, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) Jobs() *jobs.Store { return s.jobs }
