package index

import (
	"context"
	"fmt"
	"strings"
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
	// ProblemDuplicate is one ref declared by more than one file. A read
	// returns whichever sorts first, which is a coin toss dressed as an answer.
	ProblemDuplicate = "duplicate_declaration"

	// ProblemDanglingRelation is a relation pointing at an entity nobody
	// declares. The graph looks connected and is not.
	ProblemDanglingRelation = "dangling_relation"

	// ProblemDanglingNote is a note attached to an entity nobody declares,
	// usually a typo, which silently attaches the note to nothing.
	ProblemDanglingNote = "dangling_note_ref"
)

// Integrity reports everything wrong with the graph at gitRef. One call for
// every class of problem, because an operator asking "is my catalog sound"
// wants the answer rather than three separate questions.
func (db *DB) Integrity(ctx context.Context, gitRef string) ([]Problem, error) {
	duplicates, err := db.duplicates(ctx, gitRef)
	if err != nil {
		return nil, err
	}
	relations, err := db.danglingRelations(ctx, gitRef)
	if err != nil {
		return nil, err
	}
	notes, err := db.danglingNotes(ctx, gitRef)
	if err != nil {
		return nil, err
	}

	problems := make([]Problem, 0, len(duplicates)+len(relations)+len(notes))
	problems = append(problems, duplicates...)
	problems = append(problems, relations...)
	problems = append(problems, notes...)
	return problems, nil
}

type duplicateRow struct {
	Ref    string
	Places string
	Copies int
}

// duplicates finds refs declared in more than one place. Two repositories
// describing the same service is a real situation and a real problem: the
// catalog has to pick one, and picking silently is what this stops.
func (db *DB) duplicates(ctx context.Context, gitRef string) ([]Problem, error) {
	clause, args := scopeClause("", gitRef)

	var rows []duplicateRow
	err := db.gorm.WithContext(ctx).
		Model(&entityRow{}).
		Select("ref, count(*) as copies, group_concat(repository || ' at ' || path, char(10)) as places").
		Where(clause, args...).
		Group("ref").
		Having("count(*) > 1").
		Order("ref").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: find duplicate declarations: %w", err)
	}

	problems := make([]Problem, 0, len(rows))
	for _, row := range rows {
		problems = append(problems, Problem{
			Kind:   ProblemDuplicate,
			Ref:    row.Ref,
			Detail: fmt.Sprintf("declared %d times. A read returns whichever sorts first, so the catalog is answering with one of them arbitrarily", row.Copies),
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
func (db *DB) danglingRelations(ctx context.Context, gitRef string) ([]Problem, error) {
	clause, args := scopeClause("relations", gitRef)
	entityScope, entityArgs := scopeClause("e", gitRef)

	var rows []danglingRow
	err := db.gorm.WithContext(ctx).
		Model(&relationRow{}).
		Select("relations.to_ref as target, group_concat(DISTINCT relations.from_ref) as places").
		Where(clause, args...).
		Where("NOT EXISTS (SELECT 1 FROM entities e WHERE e.ref = relations.to_ref AND "+entityScope+")", entityArgs...).
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

// danglingNotes finds notes attached to entities nobody declares. ADR-0031
// accepted this weakness at write time; this is where it stops being silent.
func (db *DB) danglingNotes(ctx context.Context, gitRef string) ([]Problem, error) {
	clause, args := scopeClause("note_refs", gitRef)
	entityScope, entityArgs := scopeClause("e", gitRef)

	var rows []danglingRow
	err := db.gorm.WithContext(ctx).
		Model(&noteRefRow{}).
		Select("note_refs.ref as target, group_concat(DISTINCT note_refs.note_id) as places").
		Where(clause, args...).
		Where("NOT EXISTS (SELECT 1 FROM entities e WHERE e.ref = note_refs.ref AND "+entityScope+")", entityArgs...).
		Group("note_refs.ref").
		Order("note_refs.ref").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: find dangling note refs: %w", err)
	}

	problems := make([]Problem, 0, len(rows))
	for _, row := range rows {
		problems = append(problems, Problem{
			Kind:   ProblemDanglingNote,
			Ref:    row.Target,
			Detail: "notes are attached to this, but nothing declares it. The note is findable by search and will never appear on the thing it is about",
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
