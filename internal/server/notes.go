package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/NerdsWhoFish/dusk/internal/write"
)

// Notes is the slice of the writer this needs, declared here so the server does
// not depend on how a note is committed.
type Notes interface {
	Record(ctx context.Context, token string, note write.Note) (*write.Result, error)
	NoteDestination() string
}

// handleAPINoteStatus closes a note that is work: an idea somebody had and then
// either did or decided against. It changes only the status, because the body
// is prose somebody wrote and this surface has no business replacing it.
func (s *Server) handleAPINoteStatus(w http.ResponseWriter, r *http.Request) {
	if s.notes == nil || s.notes.NoteDestination() == "" {
		http.Error(w, `{"error":"there is nowhere to write notes"}`, http.StatusNotImplemented)
		return
	}

	// The id is a path inside the config repository, so it carries slashes and
	// travels in the body rather than in the route.
	var body struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Proof  string `json:"proof"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		http.Error(w, `{"error":"that could not be read"}`, http.StatusBadRequest)
		return
	}

	result, err := s.notes.Record(r.Context(), body.Proof, write.Note{
		Id:     body.ID,
		Status: body.Status,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	// A mode that may not commit answers with the change it would have made,
	// rather than a success the note did not have (ADR-0048). The browser shows
	// the diff; nothing here pretends the note was closed.
	if result.Proposed {
		writeJSON(w, http.StatusOK, map[string]any{
			"note": result.Ref, "status": body.Status, "proposed": true,
			"repository": result.Repository, "path": result.Path, "diff": result.Diff,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"note": result.Ref, "status": body.Status, "commit": result.Commit, "url": result.URL,
	})
}
