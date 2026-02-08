package api

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gaby/EDRmount/internal/config"
)

type providerTestRequest struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	SSL  bool   `json:"ssl"`
	// We intentionally do not require user/pass for connectivity tests.
}

type providerTestResponse struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	LatencyMs int64  `json:"latency_ms"`
}

func (s *Server) registerProviderRoutes() {
	s.mux.HandleFunc("/api/v1/provider/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req providerTestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if req.Host == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "host required"})
			return
		}
		if req.Port == 0 {
			req.Port = 563
		}

		addr := fmt.Sprintf("%s:%d", req.Host, req.Port)
		start := time.Now()
		var err error
		if req.SSL {
			d := &net.Dialer{Timeout: 5 * time.Second}
			conn, e := tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: req.Host})
			err = e
			if conn != nil {
				_ = conn.Close()
			}
		} else {
			conn, e := net.DialTimeout("tcp", addr, 5*time.Second)
			err = e
			if conn != nil {
				_ = conn.Close()
			}
		}

		lat := time.Since(start).Milliseconds()
		if err != nil {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(providerTestResponse{OK: false, Message: err.Error(), LatencyMs: lat})
			return
		}
		_ = json.NewEncoder(w).Encode(providerTestResponse{OK: true, Message: "connect ok", LatencyMs: lat})
	})

	// Convenience endpoint: returns current ngpost + download config (masked)
	s.mux.HandleFunc("/api/v1/providers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			cfg := s.Config()
			ng := cfg.NgPost
			dl := cfg.Download
			if ng.Pass != "" {
				ng.Pass = "***"
			}
			if dl.Pass != "" {
				dl.Pass = "***"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ngpost": ng, "download": dl})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func validateNgPostCfg(n config.NgPost) error {
	if !n.Enabled {
		return nil
	}
	if n.Host == "" {
		return fmt.Errorf("ngpost.host required")
	}
	if n.User == "" {
		return fmt.Errorf("ngpost.user required")
	}
	if n.Pass == "" {
		return fmt.Errorf("ngpost.pass required")
	}
	if n.Groups == "" {
		return fmt.Errorf("ngpost.groups required")
	}
	return nil
}
