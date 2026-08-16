package index

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
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
	// everything it returned rather than only naming it. It is whatever the
	// write path compares against: an entity's version, a note's content hash.
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

// NotesFor returns the notes attached to an entity, pinned first, then by what
// their kind is for, then by id. A gotcha reaching the top without anybody
// pinning it is what ADR-0049 exists for.
func (db *DB) NotesFor(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Note, error) {
	clause, args := scopeClause("notes", gitRef)

	var rows []noteRow
	err := db.gorm.WithContext(ctx).
		Model(&noteRow{}).
		Joins("JOIN note_refs ON note_refs.repository = notes.repository"+
			" AND note_refs.git_ref = notes.git_ref AND note_refs.note_id = notes.note_id").
		Where("note_refs.ref = ?", entityRef).
		Where(clause, args...).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: notes for %q at %q: %w", entityRef, gitRef, err)
	}

	minted, err := db.Minted(ctx, gitRef)
	if err != nil {
		return nil, err
	}
	rank(rows, minted)

	notes := make([]*duskv1alpha1.Note, 0, len(rows))
	for _, row := range rows {
		notes = append(notes, row.note())
	}
	return notes, nil
}

// rank orders notes by what their kind is for. It sorts here rather than in SQL
// because the rule belongs to vocab, and a second copy of it as a CASE would
// drift from the first.
func rank(rows []noteRow, minted []vocab.Kind) {
	slices.SortFunc(rows, func(a, b noteRow) int {
		if a.Pinned != b.Pinned {
			if a.Pinned {
				return -1
			}
			return 1
		}
		aRank := vocab.Rank(vocab.RoleOf(vocab.Note, a.Kind, minted))
		bRank := vocab.Rank(vocab.RoleOf(vocab.Note, b.Kind, minted))
		if aRank != bRank {
			return aRank - bRank
		}
		return strings.Compare(a.NoteID, b.NoteID)
	})
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
	// Id is one note by its path. It is what a refused write against a note
	// names, so without it nothing could re-read exactly the note it refused.
	Id string

	// Kind is a note kind such as idea or gotcha.
	Kind string

	// Status is open, done or dropped. Open also matches a note written before
	// there was a status, because empty means open rather than unknown.
	Status string

	// Ref limits to notes attached to one entity.
	Ref string

	// AboutRepository limits to notes attached to an entity that repository
	// declares. Not notes stored in it: a note about a service usually lives in
	// the config repository.
	AboutRepository string

	// Pinned limits to what somebody marked worth keeping at the top, for a
	// caller that wants only those rather than only their ordering.
	Pinned bool

	Limit int
}

// openNote matches a note nobody has closed. Empty counts as open, so a note
// written before there was a status is not read as finished.
func openNote(alias string) (string, []any) {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return prefix + "status IN (?, ?)", []any{duskmd.StatusOpen, ""}
}

// Notes answers what has been written down, narrowed. It is one query rather
// than several so "my open ideas about this repository" is one question.
func (db *DB) Notes(ctx context.Context, gitRef string, filter NoteFilter) ([]*duskv1alpha1.Note, error) {
	if filter.Limit <= 0 {
		filter.Limit = 10
	}

	var rows []noteRow
	err := db.notesQuery(ctx, gitRef, filter).
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

// CountNotes is how many notes a filter matches, ignoring its limit, which is
// what lets a surface say how many it is not showing (ADR-0059). A second query
// rather than a total from Notes, whose other callers ask for a fixed number.
func (db *DB) CountNotes(ctx context.Context, gitRef string, filter NoteFilter) (int, error) {
	var total int64
	if err := db.notesQuery(ctx, gitRef, filter).Count(&total).Error; err != nil {
		return 0, fmt.Errorf("index: count notes at %q: %w", gitRef, err)
	}
	return int(total), nil
}

// notesQuery builds the predicates a note filter means, without an order or a
// limit. One builder, so a count and the page it describes cannot disagree
// about what the filter matched.
func (db *DB) notesQuery(ctx context.Context, gitRef string, filter NoteFilter) *gorm.DB {
	clause, args := scopeClause("notes", gitRef)
	query := db.gorm.WithContext(ctx).Model(&noteRow{}).Where(clause, args...)

	if filter.Id != "" {
		query = query.Where("notes.note_id = ?", filter.Id)
	}
	if filter.Kind != "" {
		query = query.Where("notes.kind = ?", filter.Kind)
	}
	switch filter.Status {
	case "":
	case duskmd.StatusOpen:
		open, openArgs := openNote("notes")
		query = query.Where(open, openArgs...)
	default:
		query = query.Where("notes.status = ?", filter.Status)
	}
	if filter.Pinned {
		query = query.Where("notes.pinned = ?", true)
	}
	if filter.Ref != "" {
		query = query.Where(attachedTo("note_refs.ref = ?"), filter.Ref)
	}
	if filter.AboutRepository != "" {
		scope, scopeArgs := scopeClause("entities", gitRef)
		where := attachedTo("EXISTS (SELECT 1 FROM entities WHERE entities.ref = note_refs.ref" +
			" AND entities.repository = ? AND " + scope + ")")
		query = query.Where(where, append([]any{filter.AboutRepository}, scopeArgs...)...)
	}
	return query
}

// attachedTo correlates a note_refs predicate to the note. A join would
// answer once per ref matched, duplicating a note about two things.
func attachedTo(predicate string) string {
	return "EXISTS (SELECT 1 FROM note_refs WHERE note_refs.repository = notes.repository" +
		" AND note_refs.git_ref = notes.git_ref AND note_refs.note_id = notes.note_id" +
		" AND " + predicate + ")"
}

// KindCount is how many entities share one kind.
type KindCount struct {
	Kind  string
	Count int
}

// Kinds counts entities by kind, the cheapest useful thing to show somebody
// who has not searched yet. It counts what v may see, never what exists,
// because the difference between the two is the leak (ADR-0051).
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

// Declared returns the refs one repository declares at gitRef, in ref order.
// Provenance records only the file a declaration came from, so which repository
// owns an entity is a column and cannot be derived from a read.
func (db *DB) Declared(ctx context.Context, gitRef, repository string) ([]string, error) {
	var refs []string
	err := scoped(db.gorm.WithContext(ctx), gitRef).
		Model(&entityRow{}).
		Where("repository = ? AND observed = ?", repository, false).
		Distinct().
		Order("ref").
		Pluck("ref", &refs).Error
	if err != nil {
		return nil, fmt.Errorf("index: declared by %q at %q: %w", repository, gitRef, err)
	}
	return refs, nil
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

// SearchFilter narrows a search.
type SearchFilter struct {
	// Query is the free text to match. Required.
	Query string

	// Kind restricts hits to one entity or note kind, without regard to case.
	// It narrows the query rather than the answer, because a kind applied to a
	// page a limit already cut reports nothing while matches sit past it.
	Kind string

	// Limit caps the page. Zero takes the default.
	Limit int
}

// Search runs a full-text query at gitRef, answering one page of hits and how
// many matched before Limit cut it. A note whose kind is work ranks below every
// other hit and by relevance within that group (ADR-0049).
func (db *DB) Search(ctx context.Context, gitRef string, filter SearchFilter) ([]SearchResult, int, error) {
	match := matchExpression(filter.Query)
	if match == "" {
		return nil, 0, errors.New("index: search: query is required")
	}
	if filter.Limit <= 0 {
		filter.Limit = defaultSearchLimit
	}

	minted, err := db.Minted(ctx, gitRef)
	if err != nil {
		return nil, 0, err
	}
	work := namesWithRole(minted, vocab.Note, vocab.Work)

	// Qualified, because the join makes a bare git_ref ambiguous.
	scope, scopeArgs := scopeClause("f", gitRef)
	kind, kindArgs := kindClause("f", filter.Kind)

	// In statement order: the demotion list sits in the select list, ahead of
	// the match and the predicates that narrow it.
	args := asAny(work)
	args = append(args, match)
	args = append(args, scopeArgs...)
	args = append(args, kindArgs...)
	args = append(args, filter.Limit)

	// The match is a common table expression so the window function outside it
	// never sees FTS5's rank column, which is only valid in a plain query on
	// the virtual table and errors with "row value misused" anywhere else.
	var rows []searchRow
	err = db.gorm.WithContext(ctx).Raw(`
		WITH hits AS (
			SELECT f.kind_of AS type, f.id AS ref, f.kind, f.title,
			       -- Column 7 is body. snippet() takes a positional index, so
			       -- adding a column to catalog_fts silently moves this.
			       snippet(catalog_fts, 7, '', '', '...', 12) AS snippet,
			       -- Whichever half of the catalog the hit came from. A note
			       -- joined against entities alone has no version, and a token
			       -- recording an empty one can never authorize writing it.
			       COALESCE(e.version, n.content_hash, '') AS version,
			       CASE WHEN f.kind_of = 'note' AND f.kind IN (`+placeholders(len(work))+`)
			            THEN 1 ELSE 0 END AS demoted,
			       rank AS relevance
			  FROM catalog_fts f
			  LEFT JOIN entities e
			    ON e.repository = f.repository AND e.git_ref = f.git_ref AND e.ref = f.id
			  LEFT JOIN notes n
			    ON n.repository = f.repository AND n.git_ref = f.git_ref AND n.note_id = f.id
			 WHERE catalog_fts MATCH ? AND `+scope+kind+`
		)
		SELECT type, ref, kind, title, snippet, version,
		       -- Over every row the match produced, before LIMIT. Free here
		       -- because ranking has already enumerated them all (ADR-0059).
		       COUNT(*) OVER () AS total
		  FROM hits
		 ORDER BY demoted, relevance
		 LIMIT ?`, args...,
	).Scan(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("index: search %q at %q: %w", filter.Query, gitRef, err)
	}

	results := make([]SearchResult, 0, len(rows))
	total := 0
	for _, row := range rows {
		results = append(results, row.SearchResult)
		total = row.Total
	}
	return results, total, nil
}

// searchRow is a hit plus the size of the result set it came from, which the
// window function repeats on every row.
type searchRow struct {
	SearchResult
	Total int
}

// kindClause restricts a query to one kind, and is empty for every kind. It
// returns the conjunction rather than the bare predicate so a caller can
// concatenate it onto a WHERE that always has something in it.
func kindClause(alias, kind string) (string, []any) {
	if kind == "" {
		return "", nil
	}

	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return " AND " + prefix + "kind = ? COLLATE NOCASE", []any{kind}
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
