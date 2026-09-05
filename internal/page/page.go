// Package page turns a portal page into resolved blocks.
//
// A page is a markdown file whose frontmatter declares an ordered list of
// typed queries. An agent curating a layout is therefore an agent writing
// queries, which is something agents do well (ADR-0013).
package page

import (
	"context"
	"fmt"
	"slices"
	"strings"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/insights"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
)

// Type is a block's kind. Adding one is adding a query, not a widget: the
// renderer decides how a result looks, and the page decides what it asks.
type Type string

const (
	// TypeEntities lists entities matching a query.
	TypeEntities Type = "entities"

	// TypeNotes lists recent notes.
	TypeNotes Type = "recent-notes"

	// TypeDrift lists where the catalog and reality disagree.
	TypeDrift Type = "drift"

	// TypeIntegrity lists what is wrong with the catalog itself.
	TypeIntegrity Type = "integrity"

	// TypeKinds counts entities by kind, which is the estate's shape.
	TypeKinds Type = "kinds"

	// TypeReads reports what Dusk last read, per repository.
	TypeReads Type = "reads"

	// TypeAnalytics summarizes the current estate and retained action history.
	TypeAnalytics Type = "analytics"

	// TypeGraph mounts the estate explorer. Its graph-shaped query is deferred
	// to /api/graph so the homepage can become useful before the full estate
	// payload arrives.
	TypeGraph Type = "graph"

	// TypeView mounts a plugin's own custom element, the same one an entity
	// page gets, so a plugin's view is not confined to the thing it is about.
	TypeView Type = "view"
)

// Types are the block types a page may declare.
var Types = []Type{TypeEntities, TypeNotes, TypeDrift, TypeIntegrity, TypeKinds, TypeReads, TypeAnalytics, TypeGraph, TypeView}

// Block is one declared query.
type Block struct {
	Type  Type   `yaml:"type" json:"type"`
	Title string `yaml:"title,omitempty" json:"title,omitempty"`

	// Query filters a block: on entities, bare words plus `kind:` and
	// `related:`; on notes, `kind:`, `status:` and `ref:`, which is what makes
	// "my open ideas" a block rather than a page.
	Query string `yaml:"query,omitempty" json:"query,omitempty"`

	// Sort orders an entities block: `name`, or any attribute key. A leading
	// `-` reverses it, which is how "the latest three" is written.
	Sort string `yaml:"sort,omitempty" json:"sort,omitempty"`

	Limit int `yaml:"limit,omitempty" json:"limit,omitempty"`

	// Plugin and Element name a view block's custom element. Element may be
	// left out when the plugin contributes exactly one.
	Plugin  string `yaml:"plugin,omitempty" json:"plugin,omitempty"`
	Element string `yaml:"element,omitempty" json:"element,omitempty"`

	// Ref is what a view block is about, since a homepage has no entity of its
	// own to pass to the element.
	Ref string `yaml:"ref,omitempty" json:"ref,omitempty"`

	// Wide asks the renderer for the full width. It is a hint, not a layout:
	// ADR-0013 keeps blocks queries rather than placed widgets.
	Wide bool `yaml:"wide,omitempty" json:"wide,omitempty"`
}

// Page is a portal page as declared.
type Page struct {
	Title  string  `yaml:"title" json:"title"`
	Blocks []Block `yaml:"blocks" json:"blocks"`

	// Search shows the search box. A pointer because absent means yes: search
	// is the primary action and a page that forgets to mention it should still
	// have it. Setting it false is how somebody removes it deliberately.
	Search *bool `yaml:"search,omitempty" json:"search,omitempty"`
}

// Searchable reports whether to render the search box.
func (p Page) Searchable() bool { return p.Search == nil || *p.Search }

// Resolved is a block with its query run.
type Resolved struct {
	Block

	Entities  []*duskv1alpha1.Entity `json:"entities,omitempty"`
	Notes     []*duskv1alpha1.Note   `json:"notes,omitempty"`
	Drift     []index.Drift          `json:"drift,omitempty"`
	Problems  []index.Problem        `json:"problems,omitempty"`
	Kinds     []index.KindCount      `json:"kinds,omitempty"`
	Reads     []Read                 `json:"reads,omitempty"`
	Analytics *insights.Snapshot     `json:"analytics,omitempty"`

	// Source is where a view block's JavaScript is served from, and Spec is the
	// declared view to draw instead. One or the other, filled in by whatever
	// knows which plugins are running (ADR-0020).
	Source    string           `json:"source,omitempty"`
	Spec      *plugin.ViewSpec `json:"spec,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`

	// Err is why a block is empty. A block that fails renders empty rather
	// than breaking the page (ADR-0013), so the reason has to travel with it.
	Err string `json:"error,omitempty"`
}

// Read is one repository's last reconcile, as a block renders it.
type Read struct {
	Repository string `json:"repository"`
	Entities   int    `json:"entities"`
	Error      string `json:"error,omitempty"`
	Observed   bool   `json:"observed,omitempty"`
}

// Catalog is what resolving a page needs.
type Catalog interface {
	List(ctx context.Context, gitRef, kind string) ([]*duskv1alpha1.Entity, error)
	Search(ctx context.Context, gitRef string, filter index.SearchFilter) ([]index.SearchResult, int, error)
	Get(ctx context.Context, gitRef, entityRef string) (*duskv1alpha1.Entity, error)
	Neighbors(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Relation, error)
	Notes(ctx context.Context, gitRef string, filter index.NoteFilter) ([]*duskv1alpha1.Note, error)
	Drift(ctx context.Context, gitRef string, filter index.DriftFilter, v index.Visibility) ([]index.Drift, error)
	Integrity(ctx context.Context, gitRef string, v index.Visibility) ([]index.Problem, error)
	Kinds(ctx context.Context, gitRef string, v index.Visibility) ([]index.KindCount, error)
	Scopes(ctx context.Context) ([]index.Scope, error)
	ScopeCounts(ctx context.Context) ([]index.ScopeCount, error)
}

// Default is the page shown when the config repository declares none. It has
// to be good enough that declaring is optional (ADR-0013): a catalog that only
// looks presentable once everyone does homework is the failure Dusk avoids.
func Default() Page {
	// The order pairs a tall block with a short one. Two columns with one
	// non-wide block in a row leaves half the row empty, which reads as a
	// missing panel rather than a deliberate layout.
	return Page{
		Title: "Home",
		Blocks: []Block{
			{Type: TypeKinds},
			{Type: TypeAnalytics, Title: "Estate pulse", Wide: true},
			{Type: TypeGraph, Title: "Estate map", Wide: true},
			{Type: TypeDrift, Title: "Drifted", Limit: 6},
			{Type: TypeReads, Title: "What Dusk has read"},
			{Type: TypeNotes, Title: "Recent notes", Limit: 5, Wide: true},
			{Type: TypeIntegrity, Title: "Problems", Wide: true},
		},
	}
}

// Resolve runs every block's query as v may see it. A failing block carries
// its reason and renders empty; it never takes the page with it.
func Resolve(ctx context.Context, catalog Catalog, p Page, v index.Visibility) []Resolved {
	return ResolveAt(ctx, catalog, p, v, "")
}

// ResolveAt runs every catalog query against the same default or preview view.
func ResolveAt(ctx context.Context, catalog Catalog, p Page, v index.Visibility, gitRef string) []Resolved {
	resolved := make([]Resolved, 0, len(p.Blocks))
	for _, block := range p.Blocks {
		resolved = append(resolved, resolveOne(ctx, catalog, block, v, gitRef))
	}
	return resolved
}

func resolveOne(ctx context.Context, catalog Catalog, block Block, v index.Visibility, gitRef string) Resolved {
	out := Resolved{Block: block}
	if out.Title == "" {
		out.Title = defaultTitle(block.Type)
	}

	var err error
	switch block.Type {
	case TypeEntities:
		out.Entities, out.Truncated, err = entitiesFor(ctx, catalog, block, gitRef)
	case TypeNotes:
		out.Notes, err = catalog.Notes(ctx, gitRef, notesFilter(block))
	case TypeDrift:
		out.Drift, out.Truncated, err = driftFor(ctx, catalog, block, v, gitRef)
	case TypeIntegrity:
		out.Problems, err = catalog.Integrity(ctx, gitRef, v)
	case TypeKinds:
		out.Kinds, err = catalog.Kinds(ctx, gitRef, v)
	case TypeReads:
		out.Reads, err = readsFor(ctx, catalog)
	case TypeAnalytics, TypeGraph:
		// The server joins analytics to action history, while the browser loads
		// the graph from its dedicated endpoint after the page has painted.
	case TypeView:
		out.Entities, out.Truncated, err = viewFor(ctx, catalog, block, gitRef)
	default:
		err = fmt.Errorf("no such block type %q, expected one of %s", block.Type, joinTypes())
	}

	if err != nil {
		out.Err = err.Error()
	}
	return out
}

// viewFor resolves a view block's query, if it has one. A view may render a
// result set rather than one ref, so the page asks the question and the plugin
// decides only how the answer looks. The element itself is filled in by
// whatever knows which plugins are running.
func viewFor(ctx context.Context, catalog Catalog, block Block, gitRef string) ([]*duskv1alpha1.Entity, bool, error) {
	if block.Plugin == "" {
		return nil, false, fmt.Errorf("a view block has to name a plugin")
	}
	if block.Query == "" {
		return nil, false, nil
	}
	return entitiesFor(ctx, catalog, block, gitRef)
}

func entitiesFor(ctx context.Context, catalog Catalog, block Block, gitRef string) ([]*duskv1alpha1.Entity, bool, error) {
	parsed := splitQuery(block.Query)
	limit := limitOr(block.Limit, 0)

	entities, err := candidates(ctx, catalog, parsed, block.Limit, gitRef)
	if err != nil {
		return nil, false, err
	}

	if parsed.related != "" {
		entities, err = onlyRelated(ctx, catalog, entities, parsed.related, gitRef)
		if err != nil {
			return nil, false, err
		}
	}

	// Sorted before cutting, or "the latest three" would be three arbitrary
	// entities put in order rather than the three latest.
	sortEntities(entities, block.Sort)
	return cut(entities, limit)
}

// candidates is everything the query matches before relations narrow it.
func candidates(ctx context.Context, catalog Catalog, parsed filter, limit int, gitRef string) ([]*duskv1alpha1.Entity, error) {
	// A bare or kind-only query lists, which is ordered and complete. Words go
	// through search, which is ranked.
	if parsed.words == "" {
		return catalog.List(ctx, gitRef, parsed.kind)
	}

	results, _, err := catalog.Search(ctx, gitRef, index.SearchFilter{
		Query: parsed.words, Kind: parsed.kind, Limit: limitOr(limit, 50),
	})
	if err != nil {
		return nil, err
	}

	var entities []*duskv1alpha1.Entity
	for _, result := range results {
		if result.Type != "entity" {
			continue
		}
		entity, err := catalog.Get(ctx, gitRef, result.Ref)
		if err != nil {
			continue
		}
		entities = append(entities, entity)
	}
	return entities, nil
}

// onlyRelated keeps entities with a relation to a ref, in either direction.
// Direction is ignored on purpose: asking what you have flown through an
// airport should not mean writing one block for departures and one for
// arrivals.
func onlyRelated(ctx context.Context, catalog Catalog, entities []*duskv1alpha1.Entity, ref, gitRef string) ([]*duskv1alpha1.Entity, error) {
	relations, err := catalog.Neighbors(ctx, gitRef, ref)
	if err != nil {
		return nil, err
	}

	touching := map[string]bool{}
	for _, relation := range relations {
		touching[relation.GetFrom()] = true
		touching[relation.GetTo()] = true
	}

	kept := make([]*duskv1alpha1.Entity, 0, len(entities))
	for _, entity := range entities {
		if touching[entity.GetRef()] {
			kept = append(kept, entity)
		}
	}
	return kept, nil
}

// driftFor reads `undeclared` out of the block query rather than taking a
// field of its own, because a block is a query (ADR-0013).
func driftFor(ctx context.Context, catalog Catalog, block Block, v index.Visibility, gitRef string) ([]index.Drift, bool, error) {
	filter := index.DriftFilter{Undeclared: strings.Contains(block.Query, "undeclared")}
	drifts, err := catalog.Drift(ctx, gitRef, filter, v)
	if err != nil {
		return nil, false, err
	}
	return cut(drifts, limitOr(block.Limit, 0))
}

func readsFor(ctx context.Context, catalog Catalog) ([]Read, error) {
	scopes, err := catalog.ScopeCounts(ctx)
	if err != nil {
		return nil, err
	}

	reads := make([]Read, 0, len(scopes))
	for _, scope := range scopes {
		reads = append(reads, Read{
			Repository: scope.Repository,
			Entities:   scope.Entities,
			Observed:   index.IsObserved(scope.Repository),
		})
	}
	return reads, nil
}

// filter is a parsed query. It stays deliberately small: a query language is a
// thing to design, not to grow.
type filter struct {
	kind    string
	related string
	words   string
}

// splitQuery pulls the predicates out of a query, leaving the words. `related:`
// is what makes a block a graph question rather than a list, which is where a
// catalog earns its keep over a search box.
func splitQuery(query string) filter {
	var parsed filter
	var remaining []string

	for field := range strings.FieldsSeq(query) {
		switch {
		case strings.HasPrefix(field, "kind:"):
			parsed.kind = strings.TrimPrefix(field, "kind:")
		case strings.HasPrefix(field, "related:"):
			parsed.related = strings.TrimPrefix(field, "related:")
		default:
			remaining = append(remaining, field)
		}
	}

	parsed.words = strings.Join(remaining, " ")
	return parsed
}

// sortEntities orders a block's results. Unknown keys are attributes, so a
// plugin can make its entities sortable by declaring one.
func sortEntities(entities []*duskv1alpha1.Entity, by string) {
	if by == "" {
		return
	}

	key, descending := strings.CutPrefix(by, "-")
	value := func(entity *duskv1alpha1.Entity) string {
		if key == "name" {
			return entity.GetName()
		}
		return entity.GetAttributes().GetFields()[key].GetStringValue()
	}

	slices.SortStableFunc(entities, func(left, right *duskv1alpha1.Entity) int {
		order := strings.Compare(value(left), value(right))
		if descending {
			return -order
		}
		return order
	})
}

func cut[T any](items []T, limit int) ([]T, bool, error) {
	if limit > 0 && len(items) > limit {
		return items[:limit], true, nil
	}
	return items, false, nil
}

func limitOr(limit, fallback int) int {
	if limit > 0 {
		return limit
	}
	return fallback
}

func defaultTitle(t Type) string {
	switch t {
	case TypeEntities:
		return "Entities"
	case TypeNotes:
		return "Recent notes"
	case TypeDrift:
		return "Drift"
	case TypeIntegrity:
		return "Problems"
	case TypeKinds:
		return "Kinds"
	case TypeReads:
		return "What Dusk has read"
	case TypeGraph:
		return "Estate map"
	}
	return string(t)
}

func joinTypes() string {
	names := make([]string, 0, len(Types))
	for _, t := range Types {
		names = append(names, string(t))
	}
	return strings.Join(names, ", ")
}

// notesFilter reads a notes block's query. The grammar is the same shape as an
// entities block's: `field:value` terms, so one page speaks one language.
func notesFilter(block Block) index.NoteFilter {
	filter := index.NoteFilter{Limit: limitOr(block.Limit, 6)}

	for term := range strings.FieldsSeq(block.Query) {
		field, value, ok := strings.Cut(term, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(field) {
		case "kind":
			filter.Kind = value
		case "status":
			filter.Status = value
		case "ref":
			// A ref is itself `kind:namespace/name`, so the value is whatever
			// followed the first colon rather than the next word.
			filter.Ref = value
		}
	}
	return filter
}
