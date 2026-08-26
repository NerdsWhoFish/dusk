package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// Notes is the slice of the writer this needs, declared here so the server does
// not depend on how a note is committed.
type Notes interface {
	Record(ctx context.Context, token string, note write.Note) (*write.Result, error)
	NoteDestination() string
}

func (s *Server) handleAPINotes(w http.ResponseWriter, r *http.Request) {
	if !s.operatorWrite(w, r) {
		return
	}

	limit := queryInt(r, "limit", 100, 200)
	filter := index.NoteFilter{
		Kind: r.URL.Query().Get("kind"), Status: r.URL.Query().Get("status"),
		Ref: r.URL.Query().Get("ref"), AboutRepository: r.URL.Query().Get("repository"), Limit: limit,
		Offset: queryInt(r, "offset", 0, 100000),
	}
	if raw := r.URL.Query().Get("pinned"); raw != "" {
		pinned, err := strconv.ParseBool(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "pinned must be true or false"})
			return
		}
		filter.Pinned = &pinned
	}

	notes, err := s.catalog.Notes(r.Context(), refOf(r), filter)
	if err != nil {
		writeError(w, err)
		return
	}
	total, err := s.catalog.CountNotes(r.Context(), refOf(r), filter)
	if err != nil {
		writeError(w, err)
		return
	}

	answer := map[string]any{"notes": asNotes(notes), "total": total, "offset": filter.Offset}
	if s.tokens != nil {
		seen := make(map[string]string, len(notes))
		for _, note := range notes {
			seen[note.GetId()] = note.GetContentHash()
		}
		answer["proof"] = s.tokens.Issue(proof.FromNote, seen).ID
	}
	writeJSON(w, http.StatusOK, answer)
}

func queryInt(r *http.Request, name string, fallback, ceiling int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return min(value, ceiling)
}

func (s *Server) handleAPIWriteNote(w http.ResponseWriter, r *http.Request) {
	if !s.operatorWrite(w, r) {
		return
	}
	if s.notes == nil || s.notes.NoteDestination() == "" {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "there is nowhere to write notes"})
		return
	}

	var body struct {
		ID     string    `json:"id"`
		Kind   string    `json:"kind"`
		Refs   *[]string `json:"refs"`
		Body   string    `json:"body"`
		Pinned *bool     `json:"pinned"`
		Status string    `json:"status"`
		Proof  string    `json:"proof"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "that note could not be read"})
		return
	}

	var refs []string
	if body.Refs != nil {
		refs = *body.Refs
	}
	result, err := s.notes.Record(r.Context(), body.Proof, write.Note{
		Id: body.ID, Kind: body.Kind, Refs: refs, Body: body.Body,
		Pinned: body.Pinned, Status: body.Status,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, writeResult(result))
}

func (s *Server) handleAPIDeleteNote(w http.ResponseWriter, r *http.Request) {
	if !s.operatorWrite(w, r) {
		return
	}
	if s.notes == nil || s.notes.NoteDestination() == "" {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "there is nowhere to write notes"})
		return
	}

	var body struct {
		ID      string `json:"id"`
		Proof   string `json:"proof"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "that note removal could not be read"})
		return
	}
	result, err := s.notes.Record(r.Context(), body.Proof, write.Note{
		Id: body.ID, Remove: true, Confirm: body.Confirm,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, writeResult(result))
}

func (s *Server) operatorWrite(w http.ResponseWriter, r *http.Request) bool {
	if s.visibilityFor(r).Restricted() {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "only the trusted operator view may change shared catalog knowledge"})
		return false
	}
	return true
}

// handleAPINoteStatus closes a note that is work: an idea somebody had and then
// either did or decided against. It changes only the status, because the body
// is prose somebody wrote and this surface has no business replacing it.
func (s *Server) handleAPINoteStatus(w http.ResponseWriter, r *http.Request) {
	if !s.operatorWrite(w, r) {
		return
	}
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
	// rather than a success the note did not have (ADR-0052). The browser shows
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
