package server

import "net/http"

func (s *Server) handleTelemetryConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{
		"url":         s.cfg.FaroURL,
		"environment": s.cfg.Environment,
	})
}
