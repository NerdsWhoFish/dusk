package index

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Problem is something wrong with the graph that nothing else would report.
// Each of these used to resolve silently, to "whichever sorted first" or to
// nothing, which is how a catalog ends up confidently wrong.
type Problem struct {
	// Kind is what went wrong, one of the Problem constants.
	Kind string

	// Ref is the entity, relation target, or note the problem is about.
	Ref string

	// Detail says what specifically is wrong, in a sentence.
	Detail string

	// Where lists the repository and path of everything involved, so a fix
	// does not start with a search.
	Where []string
}

const (
	// ProblemDuplicate is one ref written down twice by sources of equal
	// standing, where a read is a coin toss dressed as an answer. A declaration
	// over its own observation is not this: ADR-0034 says which wins.
	ProblemDuplicate = "duplicate_declaration"

	// ProblemDanglingRelation is a relation pointing at an entity nobody
	// declares. The graph looks connected and is not.
	ProblemDanglingRelation = "dangling_relation"

	// ProblemOrphanedObservations is a scope no ingester will refresh, left by
	// one renamed or removed. Nothing clears it, because a failed run must
	// never look like a deletion (ADR-0011), so forgetting it is asked for.
	ProblemOrphanedObservations = "orphaned_observations"
)

// Integrity reports everything wrong with the graph at gitRef, limited to what
// v may see (ADR-0051). One call for every class of problem, because an
// operator asking "is my catalog sound" wants the answer, not three questions.
func (db *DB) Integrity(ctx context.Context, gitRef string, v Visibility) ([]Problem, error) {
	duplicates, err := db.duplicates(ctx, gitRef, v)
	if err != nil {
		return nil, err
	}
	relations, err := db.danglingRelations(ctx, gitRef, v)
	if err != nil {
		return nil, err
	}
	problems := make([]Problem, 0, len(duplicates)+len(relations))
	problems = append(problems, duplicates...)
	problems = append(problems, relations...)
	return problems, nil
}

// Orphans reports observation scopes no live ingester claims. Renaming an
// ingester renames its scope, so the old one keeps answering with entities
// nothing updates, and every ref reads as declared twice.
func (db *DB) Orphans(ctx context.Context, live []string, v Visibility) ([]Problem, error) {
	// Naming a scope names an ingester and what it saw, so a viewer who may
	// not see observations may not see that there were any.
	if v.Restricted() && !v.Observed {
		return []Problem{}, nil
	}

	scopes, err := db.Scopes(ctx)
	if err != nil {
		return nil, err
	}

	claimed := make(map[string]bool, len(live))
	for _, name := range live {
		claimed[ObservedScope(name)] = true
	}

	problems := make([]Problem, 0)
	for _, scope := range scopes {
		if !IsObserved(scope.Repository) || claimed[scope.Repository] {
			continue
		}
		problems = append(problems, Problem{
			Kind:   ProblemOrphanedObservations,
			Ref:    scope.Repository,
			Detail: "observations from an ingester that is no longer running, which nothing will refresh or remove. Forget them, or restore the ingester that made them",
			Where:  []string{scope.Repository + " at " + scope.GitRef},
		})
	}
	return problems, nil
}

// Forget removes an observation scope. Only ever on request: a temporarily
// unconfigured ingester would otherwise lose the history it is about to
// re-observe, which is the deletion ADR-0011 exists to prevent.
func (db *DB) Forget(ctx context.Context, scope string) error {
	if !IsObserved(scope) {
		return fmt.Errorf("index: %q is a repository rather than an observation scope, and repositories are forgotten by uninstalling the App", scope)
	}

	// Every ref, not one: an ingester's observations are stored under a ref
	// this package does not name, and matching the wrong one deletes nothing
	// while reporting success.
	return db.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return deleteWhere(tx, "repository = ?", scope)
	})
}

type duplicateRow struct {
	Ref      string
	Places   string
	Copies   int
	Observed bool
}

// duplicates finds refs written down twice by sources that rank equally.
// Grouping on observed excludes a declaration over its own observation, which
// is the good case and has a winner. Two repositories do not, nor do two
// ingesters.
func (db *DB) duplicates(ctx context.Context, gitRef string, v Visibility) ([]Problem, error) {
	clause, args := scopeClause("", gitRef)

	// Filtered before the grouping rather than after: a copy in a repository
	// the viewer cannot read must raise no duplicate and appear in no Where.
	var rows []duplicateRow
	err := visible(db.gorm.WithContext(ctx).Model(&entityRow{}), v, "").
		Select("ref, observed, count(*) as copies, group_concat(repository || ' at ' || path, char(10)) as places").
		Where(clause, args...).
		Group("ref, observed").
		Having("count(*) > 1").
		Order("ref, observed").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: find duplicate declarations: %w", err)
	}

	problems := make([]Problem, 0, len(rows))
	for _, row := range rows {
		wrote := "declared"
		if row.Observed {
			wrote = "observed"
		}
		problems = append(problems, Problem{
			Kind:   ProblemDuplicate,
			Ref:    row.Ref,
			Detail: fmt.Sprintf("%s %d times. A read returns whichever sorts first, so the catalog is answering with one of them arbitrarily", wrote, row.Copies),
			Where:  splitOn(row.Places, "\n"),
		})
	}
	return problems, nil
}

type danglingRow struct {
	Ref    string
	Target string
	Places string
}

// danglingRelations finds relations whose target nothing declares. The target
// may legitimately live in a repository Dusk cannot see, which is why this is
// reported rather than rejected at write time.
func (db *DB) danglingRelations(ctx context.Context, gitRef string, v Visibility) ([]Problem, error) {
	clause, args := scopeClause("relations", gitRef)
	entityScope, entityArgs := v.within("e", gitRef)

	query := db.gorm.WithContext(ctx).
		Model(&relationRow{}).
		Select("relations.to_ref as target, group_concat(DISTINCT relations.from_ref) as places").
		Where(clause, args...).
		Where("NOT EXISTS (SELECT 1 FROM entities e WHERE e.ref = relations.to_ref AND "+entityScope+")", entityArgs...)

	var rows []danglingRow
	err := visible(query, v, "relations").
		Group("relations.to_ref").
		Order("relations.to_ref").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: find dangling relations: %w", err)
	}

	problems := make([]Problem, 0, len(rows))
	for _, row := range rows {
		problems = append(problems, Problem{
			Kind:   ProblemDanglingRelation,
			Ref:    row.Target,
			Detail: "pointed at by a relation but declared nowhere. Either it is a typo, or it lives in a repository Dusk cannot see",
			Where:  splitOn(row.Places, ","),
		})
	}
	return problems, nil
}

// splitOn unpacks what group_concat joined. SQLite has no array type, so a
// grouped query returns its members as one delimited string.
func splitOn(joined, sep string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(joined, sep)
}
