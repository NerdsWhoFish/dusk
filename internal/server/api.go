package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/FetchHQ/dusk/internal/controller"
	"github.com/FetchHQ/dusk/internal/index"
)

// Catalog is the slice of the index the API serves. The UI is an ordinary
// client of this and has no privileged path to the index, so anything the UI
// can show is something a script can fetch.
type Catalog interface {
	Search(ctx context.Context, gitRef, query string, limit int) ([]index.SearchResult, error)
	Get(ctx context.Context, gitRef, entityRef string) (*duskv1alpha1.Entity, error)
	Neighbors(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Relation, error)
	Dependents(ctx context.Context, gitRef, entityRef string, maxDepth int) ([]index.Dependent, error)
	List(ctx context.Context, gitRef, kind string) ([]*duskv1alpha1.Entity, error)
	NotesFor(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Note, error)
	RecentNotes(ctx context.Context, gitRef string, limit int) ([]*duskv1alpha1.Note, error)
	Kinds(ctx context.Context, gitRef string) ([]index.KindCount, error)
	Scopes(ctx context.Context) ([]index.Scope, error)
	Integrity(ctx context.Context, gitRef string) ([]index.Problem, error)
}

// entityJSON is the wire shape, written by hand rather than through protojson,
// whose field naming is built for machines. This is read by a browser and by a
// person with curl.
type entityJSON struct {
	Ref         string          `json:"ref"`
	Kind        string          `json:"kind"`
	Namespace   string          `json:"namespace"`
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Attributes  map[string]any  `json:"attributes,omitempty"`
	Provenance  *provenanceJSON `json:"provenance,omitempty"`
}

type provenanceJSON struct {
	Source     string `json:"source,omitempty"`
	Version    string `json:"version,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
}

type relationJSON struct {
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
}

type noteJSON struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Body   string `json:"body"`
	Pinned bool   `json:"pinned,omitempty"`
}

func asEntity(e *duskv1alpha1.Entity) entityJSON {
	out := entityJSON{
		Ref: e.GetRef(), Kind: e.GetKind(), Namespace: e.GetNamespace(),
		Name: e.GetName(), Title: e.GetTitle(), Description: e.GetDescription(),
	}
	if fields := e.GetAttributes().GetFields(); len(fields) > 0 {
		out.Attributes = make(map[string]any, len(fields))
		for key, value := range fields {
			out.Attributes[key] = value.AsInterface()
		}
	}
	if p := e.GetProvenance(); p != nil {
		out.Provenance = &provenanceJSON{
			Source: p.GetSource(), Version: p.GetVersion(),
		}
		if p.GetObservedAt() != nil {
			out.Provenance.ObservedAt = p.GetObservedAt().AsTime().UTC().Format(timeFormat)
		}
	}
	return out
}

const timeFormat = "2006-01-02T15:04:05Z"

func asRelations(relations []*duskv1alpha1.Relation) []relationJSON {
	out := make([]relationJSON, 0, len(relations))
	for _, r := range relations {
		out = append(out, relationJSON{Type: r.GetType(), From: r.GetFrom(), To: r.GetTo()})
	}
	return out
}

func asNotes(notes []*duskv1alpha1.Note) []noteJSON {
	out := make([]noteJSON, 0, len(notes))
	for _, n := range notes {
		out = append(out, noteJSON{ID: n.GetId(), Kind: n.GetKind(), Body: n.GetBody(), Pinned: n.GetPinned()})
	}
	return out
}

// handleAPISearch answers GET /api/search?q=&kind=&limit=
func (s *Server) handleAPISearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}

	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = min(parsed, 200)
		}
	}

	results, err := s.catalog.Search(r.Context(), "", query, limit)
	if err != nil {
		writeError(w, err)
		return
	}

	kind := r.URL.Query().Get("kind")
	out := make([]index.SearchResult, 0, len(results))
	for _, result := range results {
		if kind == "" || strings.EqualFold(result.Kind, kind) {
			out = append(out, result)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out, "query": query})
}

// handleAPIEntity answers GET /api/entities/{ref} with everything about one
// entity, matching what `get` returns to an agent, because a person and an
// agent asking the same question should not get different answers.
func (s *Server) handleAPIEntity(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")

	entity, err := s.catalog.Get(r.Context(), "", ref)
	if errors.Is(err, index.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such entity", "ref": ref})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}

	relations, err := s.catalog.Neighbors(r.Context(), "", ref)
	if err != nil {
		writeError(w, err)
		return
	}
	notes, err := s.catalog.NotesFor(r.Context(), "", ref)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entity":    asEntity(entity),
		"relations": asRelations(relations),
		"notes":     asNotes(notes),
	})
}

// handleAPIDependents answers GET /api/entities/{ref}/dependents?depth=
func (s *Server) handleAPIDependents(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")

	depth := 3
	if raw := r.URL.Query().Get("depth"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			depth = min(parsed, 10)
		}
	}

	dependents, err := s.catalog.Dependents(r.Context(), "", ref, depth)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dependents": dependents})
}

// handleAPIEntities answers GET /api/entities?kind=
func (s *Server) handleAPIEntities(w http.ResponseWriter, r *http.Request) {
	entities, err := s.catalog.List(r.Context(), "", r.URL.Query().Get("kind"))
	if err != nil {
		writeError(w, err)
		return
	}

	out := make([]entityJSON, 0, len(entities))
	for _, entity := range entities {
		out = append(out, asEntity(entity))
	}
	writeJSON(w, http.StatusOK, map[string]any{"entities": out})
}

// handleAPIOverview answers GET /api/overview: everything the portal shows
// before anybody searches. One call rather than three, because a waterfall on
// the first page loaded is the slowest the product ever feels.
func (s *Server) handleAPIOverview(w http.ResponseWriter, r *http.Request) {
	kinds, err := s.catalog.Kinds(r.Context(), "")
	if err != nil {
		writeError(w, err)
		return
	}
	notes, err := s.catalog.RecentNotes(r.Context(), "", 6)
	if err != nil {
		writeError(w, err)
		return
	}

	var repositories []controller.Status
	if s.syncs != nil {
		repositories = s.syncs.Status()
	}

	// Counted from the index rather than from sync status, which is empty
	// until a sweep has run and would report zero for a catalog full of
	// entities loaded another way.
	scopes, err := s.catalog.Scopes(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	declaring := map[string]bool{}
	for _, scope := range scopes {
		declaring[scope.Repository] = true
	}

	total := 0
	for _, kind := range kinds {
		total += kind.Count
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"kinds":        kinds,
		"total":        total,
		"declaring":    len(declaring),
		"notes":        asNotes(notes),
		"repositories": repositories,
	})
}

// handleAPIIntegrity answers GET /api/integrity: what is wrong with the graph
// that no other read would mention.
func (s *Server) handleAPIIntegrity(w http.ResponseWriter, r *http.Request) {
	problems, err := s.catalog.Integrity(r.Context(), "")
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"problems": problems})
}

// handleAPIStatus answers GET /api/status with what Dusk last read, which is
// how the UI distinguishes "nobody wrote it down" from "Dusk has not looked".
func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	if s.syncs == nil {
		writeJSON(w, http.StatusOK, map[string]any{"repositories": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": s.syncs.Status()})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError keeps the message. The UI is read by the operator who runs the
// server, so hiding what broke helps nobody and costs a log dive.
func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
}
