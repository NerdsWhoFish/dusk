package index

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"
)

// SearchResult is one full-text hit, carrying enough to render a list without
// a second read.
type SearchResult struct {
	Ref     string
	Kind    string
	Title   string
	Snippet string
}

// Dependent is an entity reached by walking relations inbound, with the length
// of the shortest path that reached it.
type Dependent struct {
	Ref   string
	Depth int
}

// Get returns one entity at gitRef, across every repository contributing to it.
// Ordering by repository keeps the answer stable when two declare the same
// entity, a conflict the catalog should surface and does not yet.
func (db *DB) Get(ctx context.Context, gitRef, entityRef string) (*duskv1alpha1.Entity, error) {
	var row entityRow
	err := db.gorm.WithContext(ctx).
		Where("git_ref = ? AND ref = ?", gitRef, entityRef).
		Order("repository").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("index: get %q at %q: %w", entityRef, gitRef, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("index: get %q at %q: %w", entityRef, gitRef, err)
	}
	return row.entity()
}

// List returns every entity at gitRef, optionally narrowed to one kind.
func (db *DB) List(ctx context.Context, gitRef, kind string) ([]*duskv1alpha1.Entity, error) {
	query := db.gorm.WithContext(ctx).Where("git_ref = ?", gitRef)
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
	err := db.gorm.WithContext(ctx).
		Where("git_ref = ? AND (from_ref = ? OR to_ref = ?)", gitRef, entityRef, entityRef).
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

	var results []SearchResult
	err := db.gorm.WithContext(ctx).Raw(`
		SELECT ref, kind, title,
		       -- Column 6 is description. snippet() takes a positional index,
		       -- so adding a column to entity_fts silently moves this.
		       snippet(entity_fts, 6, '', '', '...', 12) AS snippet
		  FROM entity_fts
		 WHERE entity_fts MATCH @match AND git_ref = @gitRef
		 ORDER BY rank
		 LIMIT @limit`,
		map[string]any{"match": match, "gitRef": gitRef, "limit": limit},
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

	var dependents []Dependent
	err := db.gorm.WithContext(ctx).Raw(`
		WITH RECURSIVE reached(ref, depth) AS (
			SELECT from_ref, 1
			  FROM relations
			 WHERE git_ref = @gitRef AND to_ref = @ref
			UNION
			SELECT r.from_ref, reached.depth + 1
			  FROM relations r
			  JOIN reached ON r.to_ref = reached.ref
			 WHERE r.git_ref = @gitRef AND reached.depth < @maxDepth
		)
		SELECT ref, MIN(depth) AS depth
		  FROM reached
		 GROUP BY ref
		 ORDER BY depth, ref`,
		map[string]any{"gitRef": gitRef, "ref": entityRef, "maxDepth": maxDepth},
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
