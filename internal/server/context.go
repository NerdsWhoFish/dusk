package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"

	"github.com/NerdsWhoFish/dusk/internal/contextconfig"
	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// AgentContext is the exact renderer behind dusk_context.
type AgentContext interface {
	PreviewContext(ctx context.Context, root string) (mcp.ContextPreview, error)
}

// ContextFile is the committed profile controlling agent orientation.
type ContextFile interface {
	Context(ctx context.Context) ([]byte, error)
	SetContext(ctx context.Context, token string, body []byte) (*write.Result, error)
}

func (s *Server) handleAPIContext(w http.ResponseWriter, r *http.Request) {
	if s.visibilityFor(r).Restricted() {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "the agent context contains the whole catalog and is available only to the trusted operator view",
		})
		return
	}
	if s.agentContext == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "agent context is not configured"})
		return
	}

	preview, err := s.agentContext.PreviewContext(r.Context(), r.URL.Query().Get("root"))
	if err != nil {
		writeError(w, err)
		return
	}

	answer := map[string]any{
		"context": preview.Context, "repository": preview.Repository,
		"declared": preview.Declared, "entity_count": preview.EntityCount,
		"budget": preview.Budget, "bytes": len(preview.Context),
	}
	profile, err := s.readContextFile(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	profile.NoteKinds = preview.NoteKinds
	profile.FullNoteKinds = preview.FullNoteKinds
	answer["profile"] = profile
	writeJSON(w, http.StatusOK, answer)
}

type contextFileJSON struct {
	Body          string   `json:"body"`
	Declared      bool     `json:"declared"`
	Path          string   `json:"path"`
	Proof         string   `json:"proof,omitempty"`
	NoteKinds     []string `json:"note_kinds"`
	FullNoteKinds []string `json:"full_note_kinds"`
}

func (s *Server) readContextFile(ctx context.Context) (contextFileJSON, error) {
	profile := contextFileJSON{Path: contextconfig.Path}
	var seen map[string]string

	if s.contextFile == nil {
		body, err := contextconfig.Format(contextconfig.Default())
		profile.Body = string(body)
		return profile, err
	}

	body, err := s.contextFile.Context(ctx)
	switch {
	case err == nil:
		profile.Body, profile.Declared = string(body), true
		seen = map[string]string{contextconfig.Path: duskmd.ContentHash(string(body))}
	case errors.Is(err, fs.ErrNotExist):
		body, formatErr := contextconfig.Format(contextconfig.Default())
		if formatErr != nil {
			return contextFileJSON{}, formatErr
		}
		profile.Body = string(body)
	default:
		return contextFileJSON{}, err
	}

	if s.tokens != nil {
		profile.Proof = s.tokens.Issue(proof.FromContext, seen).ID
	}
	return profile, nil
}

func (s *Server) handleAPISetContext(w http.ResponseWriter, r *http.Request) {
	if s.visibilityFor(r).Restricted() {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "only the trusted operator view may change agent context"})
		return
	}
	if s.contextFile == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "there is nowhere to write the context profile"})
		return
	}

	var body struct {
		Body          string    `json:"body"`
		Proof         string    `json:"proof"`
		FullNoteKinds *[]string `json:"full_note_kinds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, contextconfig.MaxBudget+8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "that context profile could not be read"})
		return
	}

	contents := []byte(body.Body)
	if body.FullNoteKinds != nil {
		profile, err := contextconfig.Parse(contents)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		profile.FullNoteKinds = *body.FullNoteKinds
		contents, err = contextconfig.Format(profile)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}

	result, err := s.contextFile.SetContext(r.Context(), body.Proof, contents)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	answer := writeResult(result)
	answer["body"] = string(contents)
	writeJSON(w, http.StatusOK, answer)
}

func writeResult(result *write.Result) map[string]any {
	return map[string]any{
		"ref": result.Ref, "repository": result.Repository, "path": result.Path,
		"commit": result.Commit, "url": result.URL, "created": result.Created,
		"removed": result.Removed, "proposed": result.Proposed, "diff": result.Diff,
	}
}
