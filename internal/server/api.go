package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/controller"
	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// Catalog is the slice of the index the API serves. The UI is an ordinary
// client of this and has no privileged path to the index, so anything the UI
// can show is something a script can fetch.
type Catalog interface {
	Search(ctx context.Context, gitRef string, filter index.SearchFilter) ([]index.SearchResult, int, error)
	Get(ctx context.Context, gitRef, entityRef string) (*duskv1alpha1.Entity, error)
	GetFrom(ctx context.Context, gitRef, entityRef, repository string) (*duskv1alpha1.Entity, error)
	ObservedAs(ctx context.Context, gitRef, entityRef, repository string) ([]string, error)
	Sources(ctx context.Context, gitRef, entityRef string) ([]index.EntitySource, error)
	Neighbors(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Relation, error)
	Dependents(ctx context.Context, gitRef, entityRef string, maxDepth int) ([]index.Dependent, error)
	List(ctx context.Context, gitRef, kind string) ([]*duskv1alpha1.Entity, error)
	NotesFor(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Note, error)
	RecentNotes(ctx context.Context, gitRef string, limit int) ([]*duskv1alpha1.Note, error)
	Notes(ctx context.Context, gitRef string, filter index.NoteFilter) ([]*duskv1alpha1.Note, error)
	CountNotes(ctx context.Context, gitRef string, filter index.NoteFilter) (int, error)
	Graph(ctx context.Context, gitRef string, v index.Visibility) (index.Graph, error)
	Kinds(ctx context.Context, gitRef string, v index.Visibility) ([]index.KindCount, error)

	// Minted is the overlay a mint writes: a role and aliases, and no counts,
	// so it needs no visibility of its own.
	Minted(ctx context.Context, gitRef string) ([]vocab.Kind, error)
	Scopes(ctx context.Context) ([]index.Scope, error)
	ScopeCounts(ctx context.Context) ([]index.ScopeCount, error)
	Integrity(ctx context.Context, gitRef string, v index.Visibility) ([]index.Problem, error)
	Drift(ctx context.Context, gitRef string, filter index.DriftFilter, v index.Visibility) ([]index.Drift, error)
	VisibleTo(ctx context.Context, gitRef string, v index.Visibility) ([]string, error)
	Diff(ctx context.Context, base, head string) ([]index.Change, error)
	ResolvePreview(ctx context.Context, ref string) (string, error)
	Orphans(ctx context.Context, live []string, v index.Visibility) ([]index.Problem, error)
	Forget(ctx context.Context, scope string) error
}

// Rotation is what is currently observing, which the index cannot know. Only
// together do they distinguish a scope left behind from one still in use.
type Rotation interface {
	Names() []string
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
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Body       string          `json:"body"`
	Refs       []string        `json:"refs,omitempty"`
	Pinned     bool            `json:"pinned,omitempty"`
	Provenance *provenanceJSON `json:"provenance,omitempty"`

	// Status closes a note that is work. Empty means open, which is what a note
	// written before there was a status is.
	Status string `json:"status,omitempty"`
}

type graphNodeJSON struct {
	Ref   string     `json:"ref"`
	Kind  string     `json:"kind"`
	Title string     `json:"title"`
	Notes []noteJSON `json:"notes"`
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
		entry := noteJSON{
			ID: n.GetId(), Kind: n.GetKind(), Body: n.GetBody(),
			Refs: n.GetRefs(), Pinned: n.GetPinned(), Status: n.GetStatus(),
		}
		if p := n.GetProvenance(); p != nil {
			entry.Provenance = &provenanceJSON{Source: p.GetSource(), Version: p.GetVersion()}
			if p.GetObservedAt() != nil {
				entry.Provenance.ObservedAt = p.GetObservedAt().AsTime().UTC().Format(timeFormat)
			}
		}
		out = append(out, entry)
	}
	return out
}

// handleAPIGraph answers the estate as a lean graph payload. Entity detail is
// deliberately absent: the map needs identity, edges, and attached knowledge;
// opening a node is the read that asks for everything else.
func (s *Server) handleAPIGraph(w http.ResponseWriter, r *http.Request) {
	graph, err := s.catalog.Graph(r.Context(), refOf(r), s.visibilityFor(r))
	if err != nil {
		writeError(w, err)
		return
	}

	nodes := make([]graphNodeJSON, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		title := node.Entity.GetTitle()
		if title == "" {
			title = node.Entity.GetName()
		}
		nodes = append(nodes, graphNodeJSON{
			Ref: node.Entity.GetRef(), Kind: node.Entity.GetKind(), Title: title,
			Notes: asNotes(node.Notes),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes, "relations": asRelations(graph.Relations),
	})
}

// handleAPINote answers GET /api/notes/{id}. Search covers entities and notes,
// so each hit needs a real destination of its own rather than pretending a
// note path is an entity ref.
func (s *Server) handleAPINote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	notes, err := s.catalog.Notes(r.Context(), refOf(r), index.NoteFilter{Id: id, Limit: 1})
	if err != nil {
		writeError(w, err)
		return
	}
	if len(notes) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such note", "id": id})
		return
	}

	answer := map[string]any{"note": asNotes(notes)[0]}
	if s.tokens != nil && refOf(r) == "" {
		answer["proof"] = s.tokens.Issue(proof.FromGet, map[string]string{
			id: notes[0].GetContentHash(),
		}).ID
	}
	writeJSON(w, http.StatusOK, answer)
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

	results, total, err := s.catalog.Search(r.Context(), refOf(r), index.SearchFilter{
		Query: query, Kind: r.URL.Query().Get("kind"), Limit: limit,
		Visibility: s.visibilityFor(r),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	visible, err := s.visible(r)
	if err != nil {
		writeError(w, err)
		return
	}

	out := narrow(results, visible)

	// What matched, not what survived the limit, so a caller can tell a short
	// answer from a complete one (ADR-0059).
	answer := map[string]any{"results": out, "query": query, "total": total}
	if refOf(r) == "" {
		if token := s.searched(out); token != "" {
			answer["proof"] = token
		}
	}
	writeJSON(w, http.StatusOK, answer)
}

// narrow drops what the viewer may not see. The kind a query asked to exclude
// is narrowed in the query, because a filter over an answer a limit already cut
// can report nothing while matches sit past it.
func narrow(results []index.SearchResult, visible func(string) bool) []index.SearchResult {
	out := make([]index.SearchResult, 0, len(results))
	for _, result := range results {
		// A note is visible with the entity it is about; an entity by itself.
		if result.Type == "entity" && !visible(result.Ref) {
			continue
		}
		out = append(out, result)
	}
	return out
}

// searched mints the token a search yields. A search is the read that witnesses
// an absence, which is what creating something needs proof of (ADR-0009), and
// without it the browser could not record something the catalog has never seen.
func (s *Server) searched(results []index.SearchResult) string {
	if s.tokens == nil {
		return ""
	}

	seen := make(map[string]string, len(results))
	for _, result := range results {
		seen[result.Ref] = result.Version
	}
	return s.tokens.Issue(proof.FromSearch, seen).ID
}

// handleAPIEntity answers GET /api/entities/{ref} with everything about one
// entity, matching what `get` returns to an agent, because a person and an
// agent asking the same question should not get different answers.
func (s *Server) handleAPIEntity(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	repository := strings.TrimSpace(r.URL.Query().Get("repository"))

	visible, err := s.visible(r)
	if err != nil {
		writeError(w, err)
		return
	}

	entity, observedAs, err := s.entityDeclaration(r.Context(), refOf(r), ref, repository)
	// Not-visible answers exactly as not-found. Telling somebody an entity
	// exists but is none of their business leaks the thing being protected.
	if errors.Is(err, index.ErrNotFound) || (err == nil && !visible(ref)) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such entity", "ref": ref})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}

	relations, err := s.catalog.Neighbors(r.Context(), refOf(r), ref)
	if err != nil {
		writeError(w, err)
		return
	}
	notes, err := s.catalog.NotesFor(r.Context(), refOf(r), ref)
	if err != nil {
		writeError(w, err)
		return
	}
	operational, err := s.entityOperational(r.Context(), refOf(r), ref, visible)
	if err != nil {
		writeError(w, err)
		return
	}

	// Views and actions ride along with the entity rather than costing a second
	// request, for the same reason `get` is fat: this is one question.
	views, actions := s.entityContributions(r, entity.GetKind())

	answer := map[string]any{
		"entity":      asEntity(entity),
		"relations":   asRelations(relations),
		"notes":       asNotes(notes),
		"views":       views,
		"actions":     actions,
		"sources":     operational.sources,
		"dependents":  operational.dependents,
		"events":      operational.events,
		"observed_as": observedAs,
	}

	// The browser meets the same read-before-write contract an agent does, so
	// an entity that changed while the page was open refuses the action rather
	// than acting on what is no longer true (ADR-0009).
	if s.tokens != nil && refOf(r) == "" {
		// The same read authorizes edits to both the entity and its shown notes.
		seen := map[string]string{ref: entity.GetProvenance().GetVersion()}
		for _, note := range notes {
			seen[note.GetId()] = note.GetContentHash()
		}
		answer["proof"] = s.tokens.Issue(proof.FromGet, seen).ID
	}
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) entityContributions(r *http.Request, kind string) ([]plugin.View, []plugin.Action) {
	if s.plugins == nil || refOf(r) != "" {
		return nil, nil
	}
	return s.plugins.Views(kind), s.plugins.Actions(kind)
}

func (s *Server) entityDeclaration(ctx context.Context, gitRef, ref, repository string) (*duskv1alpha1.Entity, []string, error) {
	if repository == "" {
		entity, err := s.catalog.Get(ctx, gitRef, ref)
		return entity, nil, err
	}
	entity, err := s.catalog.GetFrom(ctx, gitRef, ref, repository)
	if err != nil {
		return nil, nil, err
	}
	observedAs, err := s.catalog.ObservedAs(ctx, gitRef, ref, repository)
	return entity, observedAs, err
}

type operationalEntity struct {
	sources    []index.EntitySource
	dependents []index.Dependent
	events     []eventJSON
}

func (s *Server) entityOperational(
	ctx context.Context,
	gitRef string,
	ref string,
	visible func(string) bool,
) (operationalEntity, error) {
	sources, err := s.catalog.Sources(ctx, gitRef, ref)
	if err != nil {
		return operationalEntity{}, err
	}
	dependents, err := s.catalog.Dependents(ctx, gitRef, ref, 4)
	if err != nil {
		return operationalEntity{}, err
	}
	dependents = slices.DeleteFunc(dependents, func(dependent index.Dependent) bool {
		return !visible(dependent.Ref)
	})
	return operationalEntity{
		sources:    sources,
		dependents: dependents,
		events:     asEvents(s.events.RecentFor(ref, 12)),
	}, nil
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

	dependents, err := s.catalog.Dependents(r.Context(), refOf(r), ref, depth)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dependents": dependents})
}

// handleAPIEntities answers GET /api/entities?kind=
func (s *Server) handleAPIEntities(w http.ResponseWriter, r *http.Request) {
	entities, err := s.catalog.List(r.Context(), refOf(r), r.URL.Query().Get("kind"))
	if err != nil {
		writeError(w, err)
		return
	}

	visible, err := s.visible(r)
	if err != nil {
		writeError(w, err)
		return
	}

	out := make([]entityJSON, 0, len(entities))
	for _, entity := range entities {
		if visible(entity.GetRef()) {
			out = append(out, asEntity(entity))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entities": out})
}

// refOf reads which ref to show, which is how a preview is browsed. Only a
// preview ref is accepted: letting a query string name any ref would let a
// reader wander into whatever happened to be indexed.
func refOf(r *http.Request) string {
	ref := r.URL.Query().Get("ref")
	if index.IsPreviewRef(ref) {
		return ref
	}
	return ""
}

// visible returns a predicate for what this request may see. Filtering after
// the query rather than inside it keeps one code path: an unrestricted viewer,
// which is every single-operator deployment, pays nothing.
func (s *Server) visible(r *http.Request) (func(ref string) bool, error) {
	visibility := s.visibilityFor(r)
	if !visibility.Restricted() {
		return func(string) bool { return true }, nil
	}

	refs, err := s.catalog.VisibleTo(r.Context(), refOf(r), visibility)
	if err != nil {
		return nil, err
	}

	allowed := make(map[string]bool, len(refs))
	for _, ref := range refs {
		allowed[ref] = true
	}
	return func(ref string) bool { return allowed[ref] }, nil
}

// handleAPIOverview answers GET /api/overview: everything the portal shows
// before anybody searches. One call rather than three, because a waterfall on
// the first page loaded is the slowest the product ever feels.
func (s *Server) handleAPIOverview(w http.ResponseWriter, r *http.Request) {
	kinds, err := s.catalog.Kinds(r.Context(), refOf(r), s.visibilityFor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	notes, err := s.catalog.RecentNotes(r.Context(), refOf(r), 6)
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
	scopes, err := s.catalog.ScopeCounts(r.Context())
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

// handleAPIDiff compares a repository's preview with the complete default view.
func (s *Server) handleAPIDiff(w http.ResponseWriter, r *http.Request) {
	ref := refOf(r)
	if ref == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "pass ref=refs/pull/<owner>/<repository>/<number>/head to compare a pull request",
		})
		return
	}

	changes, err := s.catalog.Diff(r.Context(), "", ref)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ref": ref, "changes": changes})
}

// handleAPIViewer answers who this browser is.
func (s *Server) handleAPIViewer(w http.ResponseWriter, r *http.Request) {
	identity, ok := s.signedInAs(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"signed_in":   false,
			"restricted":  false,
			"github":      s.oauth.Configured(),
			"cache_scope": "public",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"signed_in":   true,
		"login":       identity.Login,
		"restricted":  false,
		"github":      true,
		"cache_scope": "operator",
	})
}

// handleAPIIntegrity answers GET /api/integrity: what is wrong with the graph
// that no other read would mention.
func (s *Server) handleAPIIntegrity(w http.ResponseWriter, r *http.Request) {
	visibility := s.visibilityFor(r)

	problems, err := s.catalog.Integrity(r.Context(), refOf(r), visibility)
	if err != nil {
		writeError(w, err)
		return
	}

	// Orphans need both halves: the index knows which scopes exist, and only
	// the rotation knows which of them anything still refreshes.
	if s.rotation != nil {
		orphans, err := s.catalog.Orphans(r.Context(), s.rotation.Names(), visibility)
		if err != nil {
			writeError(w, err)
			return
		}
		problems = append(problems, orphans...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"problems": problems})
}

// handleAPIForget answers POST /api/observations/forget, which is how the
// scope an old ingester left behind is cleared. Never automatic: a temporarily
// unconfigured ingester would lose history it is about to re-observe.
func (s *Server) handleAPIForget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope string `json:"scope"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		http.Error(w, `{"error":"that request could not be read"}`, http.StatusBadRequest)
		return
	}

	if s.rotation != nil && slices.Contains(s.rotation.Names(), strings.TrimPrefix(body.Scope, "ingester:")) {
		http.Error(w, `{"error":"that ingester is running, so forgetting it would only lose what it is about to observe again"}`, http.StatusConflict)
		return
	}

	if err := s.catalog.Forget(r.Context(), body.Scope); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"forgot": body.Scope})
}

// handleAPIDrift answers GET /api/drift: what the catalog claims and reality
// does not support. `?undeclared=true` adds the other direction.
func (s *Server) handleAPIDrift(w http.ResponseWriter, r *http.Request) {
	filter := index.DriftFilter{Undeclared: r.URL.Query().Get("undeclared") == "true"}
	drifts, err := s.catalog.Drift(r.Context(), refOf(r), filter, s.visibilityFor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"drift": drifts})
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
