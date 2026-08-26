package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// RepositoryFiles is the shared repository opt-in path used by the browser
// and MCP. GitHub installation access remains the authority boundary.
type RepositoryFiles interface {
	RepositoryRoot(ctx context.Context, repository string) (*write.RepositoryFile, error)
	SetRepositoryRoot(ctx context.Context, token, repository string, body []byte) (*write.Result, error)
}

func (s *Server) handleAPIRepository(w http.ResponseWriter, r *http.Request) {
	if !s.operatorWrite(w, r) {
		return
	}
	if s.repositories == nil || s.tokens == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "repository files are unavailable"})
		return
	}
	file, err := s.repositories.RepositoryRoot(r.Context(), r.URL.Query().Get("repository"))
	if err != nil {
		writeError(w, err)
		return
	}
	seen := map[string]string{}
	if file.Exists {
		seen[file.Repository] = file.Version
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"repository": file.Repository,
		"path":       file.Path,
		"body":       string(file.Body),
		"template":   string(file.Template),
		"declared":   file.Exists,
		"proof":      s.tokens.Issue(proof.FromRepository, seen).ID,
	})
}

func (s *Server) handleAPISetRepository(w http.ResponseWriter, r *http.Request) {
	if !s.operatorWrite(w, r) {
		return
	}
	if s.repositories == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "repository files are unavailable"})
		return
	}
	var body struct {
		Repository string `json:"repository"`
		Body       string `json:"body"`
		Proof      string `json:"proof"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "that repository file could not be read"})
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "dusk.md cannot be empty"})
		return
	}
	result, err := s.repositories.SetRepositoryRoot(r.Context(), body.Proof, body.Repository, []byte(body.Body))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, writeResult(result))
}
