package index

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
)

// SearchResult is one full-text hit, carrying enough to render a list without
// a second read.
type SearchResult struct {
	// Type is "entity" or "note". One search covers both, because "how do I
	// reach the zwave pi" is a note and "the zwave pi" is an entity.
	Type string

	// Ref identifies the hit: an entity ref, or a note's path.
	Ref     string
	Kind    string
	Title   string
	Snippet string

	// Version is what a proof token records, so a search authorizes writing
	// everything it returned rather than only naming it. Empty for a note.
	Version string
}

// Dependent is an entity reached by walking relations inbound, with the length
// of the shortest path that reached it.
type Dependent struct {
	Ref   string
	Depth int
}

// Get returns one entity at gitRef, across every repository contributing to it.
// Ordering keeps the answer stable when more than one source has the ref, and
// Integrity reports the cases where that ordering is the only tiebreak.
func (db *DB) Get(ctx context.Context, gitRef, entityRef string) (*duskv1alpha1.Entity, error) {
	var row entityRow
	err := scoped(db.gorm.WithContext(ctx), gitRef).
		Where("ref = ?", entityRef).
		// A human who wrote it down beats an ingester that inferred it
		// (ADR-0034). Repository is the tiebreak among equals.
		Order("observed, repository").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("index: get %q at %q: %w", entityRef, gitRef, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("index: get %q at %q: %w", entityRef, gitRef, err)
	}
	return row.entity()
}

// NotesFor returns the notes attached to an entity, pinned first then by id.
// It is what makes a note's refs worth writing, since knowledge about a service
// is only useful if it arrives when somebody asks about that service.
func (db *DB) NotesFor(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Note, error) {
	clause, args := scopeClause("notes", gitRef)

	var rows []noteRow
	err := db.gorm.WithContext(ctx).
		Model(&noteRow{}).
		Joins("JOIN note_refs ON note_refs.repository = notes.repository"+
			" AND note_refs.git_ref = notes.git_ref AND note_refs.note_id = notes.note_id").
		Where("note_refs.ref = ?", entityRef).
		Where(clause, args...).
		Order("notes.pinned DESC, notes.note_id").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: notes for %q at %q: %w", entityRef, gitRef, err)
	}

	notes := make([]*duskv1alpha1.Note, 0, len(rows))
	for _, row := range rows {
		notes = append(notes, row.note())
	}
	return notes, nil
}

// RecentNotes returns the most recently observed notes, pinned first. It backs
// the portal's recent-notes block from ADR-0013: the accumulated knowledge is
// the half of the catalog worth showing before anybody searches.
func (db *DB) RecentNotes(ctx context.Context, gitRef string, limit int) ([]*duskv1alpha1.Note, error) {
	return db.Notes(ctx, gitRef, NoteFilter{Limit: limit})
}

// NoteFilter narrows what Notes answers with. An empty filter is every note,
// newest first.
type NoteFilter struct {
	// Kind is a note kind such as idea or gotcha.
	Kind string

	// Status is open, done or dropped. Open also matches a note written before
	// there was a status, because empty means open rather than unknown.
	Status string

	// Ref limits to notes attached to one entity.
	Ref string

	Limit int
}

// Notes answers what has been written down, narrowed. It is one query rather
// than several so "my open ideas about this repository" is one question.
func (db *DB) Notes(ctx context.Context, gitRef string, filter NoteFilter) ([]*duskv1alpha1.Note, error) {
	if filter.Limit <= 0 {
		filter.Limit = 10
	}

	clause, args := scopeClause("notes", gitRef)
	query := db.gorm.WithContext(ctx).Model(&noteRow{}).Where(clause, args...)

	if filter.Kind != "" {
		query = query.Where("notes.kind = ?", filter.Kind)
	}
	switch filter.Status {
	case "":
	case duskmd.StatusOpen:
		query = query.Where("notes.status = ? OR notes.status = ?", duskmd.StatusOpen, "")
	default:
		query = query.Where("notes.status = ?", filter.Status)
	}
	if filter.Ref != "" {
		query = query.Joins("JOIN note_refs ON note_refs.repository = notes.repository"+
			" AND note_refs.git_ref = notes.git_ref AND note_refs.note_id = notes.note_id").
			Where("note_refs.ref = ?", filter.Ref)
	}

	var rows []noteRow
	err := query.
		Order("notes.pinned DESC, notes.observed_at DESC, notes.note_id").
		Limit(filter.Limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: notes at %q: %w", gitRef, err)
	}

	notes := make([]*duskv1alpha1.Note, 0, len(rows))
	for _, row := range rows {
		notes = append(notes, row.note())
	}
	return notes, nil
}

// KindCount is how many entities share one kind.
type KindCount struct {
	Kind  string
	Count int
}

// Kinds counts entities by kind, the cheapest useful thing to show somebody
// who has not searched yet. It counts what v may see, never what exists,
// because the difference between the two is the leak (ADR-0048).
func (db *DB) Kinds(ctx context.Context, gitRef string, v Visibility) ([]KindCount, error) {
	var counts []KindCount
	err := visible(scoped(db.gorm.WithContext(ctx), gitRef), v, "").
		Model(&entityRow{}).
		Select("kind, count(*) as count").
		Group("kind").
		Order("count DESC, kind").
		Find(&counts).Error
	if err != nil {
		return nil, fmt.Errorf("index: count kinds at %q: %w", gitRef, err)
	}
	return counts, nil
}

// Location is where an entity is declared: the repository, the file, and the
// version a write must still match to prove it read the current one.
type Location struct {
	Repository string
	GitRef     string
	Path       string
	Version    string
}

// Locate finds the file that declares an entity, which is how a write routes to
// the repository that owns it rather than needing a routing table.
func (db *DB) Locate(ctx context.Context, gitRef, entityRef string) (*Location, error) {
	var row entityRow
	err := scoped(db.gorm.WithContext(ctx), gitRef).
		Where("ref = ?", entityRef).
		Order("repository").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("index: locate %q: %w", entityRef, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("index: locate %q: %w", entityRef, err)
	}
	return &Location{
		Repository: row.Repository,
		GitRef:     row.GitRef,
		Path:       row.Path,
		Version:    row.Version,
	}, nil
}

// ObservedBy names the ingester scopes that saw this entity. Get returns one
// winning row and prefers the declared one, so it cannot answer this: an action
// has to reach the plugin that actually knows the thing.
func (db *DB) ObservedBy(ctx context.Context, gitRef, entityRef string) ([]string, error) {
	var repositories []string
	err := scoped(db.gorm.WithContext(ctx), gitRef).
		Model(&entityRow{}).
		Where("ref = ? AND observed = ?", entityRef, true).
		Distinct().
		Order("repository").
		Pluck("repository", &repositories).Error
	if err != nil {
		return nil, fmt.Errorf("index: observers of %q: %w", entityRef, err)
	}

	scopes := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		scopes = append(scopes, strings.TrimPrefix(repository, observedPrefix))
	}
	return scopes, nil
}

// List returns every entity at gitRef, optionally narrowed to one kind.
func (db *DB) List(ctx context.Context, gitRef, kind string) ([]*duskv1alpha1.Entity, error) {
	query := scoped(db.gorm.WithContext(ctx), gitRef)
	if kind != "" {
		query = query.Where("kind = ?", kind)
	}

	var rows []entityRow
	if err := query.Order("repository, ref").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("index: list at %q: %w", gitRef, err)
	}
	return entities(rows)
}

// Neighbors returns every relation with entityRef at either end.
func (db *DB) Neighbors(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Relation, error) {
	var rows []relationRow
	err := scoped(db.gorm.WithContext(ctx), gitRef).
		Where("from_ref = ? OR to_ref = ?", entityRef, entityRef).
		Order("from_ref, to_ref, type").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: neighbors of %q at %q: %w", entityRef, gitRef, err)
	}

	relations := make([]*duskv1alpha1.Relation, 0, len(rows))
	for _, row := range rows {
		relation, err := row.relation()
		if err != nil {
			return nil, err
		}
		relations = append(relations, relation)
	}
	return relations, nil
}

// Search runs a full-text query against entity text at gitRef.
func (db *DB) Search(ctx context.Context, gitRef, query string, limit int) ([]SearchResult, error) {
	match := matchExpression(query)
	if match == "" {
		return nil, errors.New("index: search: query is required")
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	// Qualified, because the join makes a bare git_ref ambiguous.
	scope, scopeArgs := scopeClause("f", gitRef)
	args := append([]any{match}, scopeArgs...)
	args = append(args, limit)

	var results []SearchResult
	err := db.gorm.WithContext(ctx).Raw(`
		SELECT f.kind_of AS type, f.id AS ref, f.kind, f.title,
		       -- Column 7 is body. snippet() takes a positional index, so
		       -- adding a column to catalog_fts silently moves this.
		       snippet(catalog_fts, 7, '', '', '...', 12) AS snippet,
		       COALESCE(e.version, '') AS version
		  FROM catalog_fts f
		  LEFT JOIN entities e
		    ON e.repository = f.repository AND e.git_ref = f.git_ref AND e.ref = f.id
		 WHERE catalog_fts MATCH ? AND `+scope+`
		 ORDER BY rank
		 LIMIT ?`, args...,
	).Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("index: search %q at %q: %w", query, gitRef, err)
	}
	return results, nil
}

const defaultSearchLimit = 25

// Dependents walks relations inbound from entityRef, answering "what breaks if
// this goes away". Depth is bounded because a dependency graph may be cyclic.
func (db *DB) Dependents(ctx context.Context, gitRef, entityRef string, maxDepth int) ([]Dependent, error) {
	if maxDepth < 1 {
		return nil, fmt.Errorf("index: dependents: max depth must be at least 1, got %d", maxDepth)
	}

	seed, seedArgs := scopeClause("", gitRef)
	walk, walkArgs := scopeClause("r", gitRef)

	args := append([]any{}, seedArgs...)
	args = append(args, entityRef)
	args = append(args, walkArgs...)
	args = append(args, maxDepth)

	var dependents []Dependent
	err := db.gorm.WithContext(ctx).Raw(`
		WITH RECURSIVE reached(ref, depth) AS (
			SELECT from_ref, 1
			  FROM relations
			 WHERE `+seed+` AND to_ref = ?
			UNION
			SELECT r.from_ref, reached.depth + 1
			  FROM relations r
			  JOIN reached ON r.to_ref = reached.ref
			 WHERE `+walk+` AND reached.depth < ?
		)
		SELECT ref, MIN(depth) AS depth
		  FROM reached
		 GROUP BY ref
		 ORDER BY depth, ref`, args...,
	).Scan(&dependents).Error
	if err != nil {
		return nil, fmt.Errorf("index: dependents of %q at %q: %w", entityRef, gitRef, err)
	}
	return dependents, nil
}

// matchExpression turns free text into an FTS5 query that cannot be a syntax
// error, quoting each token and making the last a prefix so results narrow as
// a query is typed.
func matchExpression(query string) string {
	fields := strings.Fields(query)
	terms := make([]string, 0, len(fields))
	for i, field := range fields {
		term := `"` + strings.ReplaceAll(field, `"`, `""`) + `"`
		if i == len(fields)-1 {
			term += "*"
		}
		terms = append(terms, term)
	}
	return strings.Join(terms, " ")
}

func entities(rows []entityRow) ([]*duskv1alpha1.Entity, error) {
	out := make([]*duskv1alpha1.Entity, 0, len(rows))
	for _, row := range rows {
		entity, err := row.entity()
		if err != nil {
			return nil, err
		}
		out = append(out, entity)
	}
	return out, nil
}

func (r entityRow) entity() (*duskv1alpha1.Entity, error) {
	attributes, err := unmarshalStruct(r.Attributes)
	if err != nil {
		return nil, fmt.Errorf("index: entity %q: %w", r.Ref, err)
	}
	return &duskv1alpha1.Entity{
		Ref:         r.Ref,
		Kind:        r.Kind,
		Namespace:   r.Namespace,
		Name:        r.Name,
		Title:       r.Title,
		Description: r.Description,
		Attributes:  attributes,
		Provenance:  provenance(r.Source, r.Version, r.ObservedAt),
	}, nil
}

func (r relationRow) relation() (*duskv1alpha1.Relation, error) {
	attributes, err := unmarshalStruct(r.Attributes)
	if err != nil {
		return nil, fmt.Errorf("index: relation %q -> %q: %w", r.FromRef, r.ToRef, err)
	}
	return &duskv1alpha1.Relation{
		From:       r.FromRef,
		To:         r.ToRef,
		Type:       r.Type,
		Attributes: attributes,
		Provenance: provenance(r.Source, r.Version, r.ObservedAt),
	}, nil
}
