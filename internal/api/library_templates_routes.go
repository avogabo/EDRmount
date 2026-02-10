package api

import "net/http"

func (s *Server) registerLibraryTemplatesRoutes() {
	// GET /api/v1/library/templates/preview
	s.mux.HandleFunc("/api/v1/library/templates/preview", func(w http.ResponseWriter, r *http.Request) {
		s.handleTemplatesPreview(w, r)
	})
}
