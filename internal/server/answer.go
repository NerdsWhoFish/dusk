package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/NerdsWhoFish/dusk/internal/answer"
)

// handleAPIAnswerConfig publishes only what the search UI needs. Provider
// credentials and the full endpoint never cross the server boundary.
func (s *Server) handleAPIAnswerConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.answers.Config())
}

// handleAPIAsk answers one question from a bounded, viewer-visible catalog
// slice. It is deliberately separate from ordinary search, which remains a
// local SQLite read and never calls an external provider.
func (s *Server) handleAPIAsk(w http.ResponseWriter, r *http.Request) {
	if s.answers == nil || !s.answers.Config().Enabled {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "AI search is not configured"})
		return
	}

	var request struct {
		Question string `json:"question"`
		Model    string `json:"model"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid AI question: " + err.Error()})
		return
	}

	result, err := s.answers.Ask(r.Context(), refOf(r), s.visibilityFor(r),
		strings.TrimSpace(request.Question), strings.TrimSpace(request.Model))
	if errors.Is(err, answer.ErrInvalid) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		s.log.Warn("AI search failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "the AI provider could not answer that question"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
