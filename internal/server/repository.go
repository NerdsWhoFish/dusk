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

// Declarations is the shared entity write path used by the browser and MCP.
type Declarations interface {
	Declare(ctx context.Context, token string, declaration write.Declaration) (*write.Result, error)
}

func (s *Server) handleAPISetEntity(w http.ResponseWriter, r *http.Request) {
	if !s.operatorWrite(w, r) {
		return
	}
	if s.declarations == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "entity declarations are unavailable"})
		return
	}

	var body struct {
		Proof          string            `json:"proof"`
		Repository     string            `json:"repository"`
		Title          string            `json:"title"`
		Description    string            `json:"description"`
		Attributes     map[string]string `json:"attributes"`
		ObservedAs     []string          `json:"observed_as"`
		Unset          []string          `json:"unset"`
		Decommissioned *bool             `json:"decommissioned"`
		Remove         bool              `json:"remove"`
		Confirm        bool              `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "that entity change could not be read"})
		return
	}

	result, err := s.declarations.Declare(r.Context(), body.Proof, write.Declaration{
		Ref: r.PathValue("ref"), Repository: body.Repository,
		Title: body.Title, Description: body.Description,
		Attributes: body.Attributes, ObservedAs: body.ObservedAs, Unset: body.Unset,
		Decommissioned: body.Decommissioned, Remove: body.Remove, Confirm: body.Confirm,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, writeResult(result))
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
