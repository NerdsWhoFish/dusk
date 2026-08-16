package index

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// Drift is a disagreement between what somebody wrote down and what an
// ingester found. It is the question no wiki can answer about itself.
type Drift struct {
	// Kind is what sort of disagreement, one of the Drift constants.
	Kind string

	Ref   string
	Title string

	// Declared is where a human wrote it down, empty when nobody did.
	Declared string

	// Observed is which ingester saw it, empty when none did.
	Observed string

	// Detail says what it means, in a sentence an operator can act on.
	Detail string
}

const (
	// DriftMissing is declared and not found. Either it is gone, or the
	// ingester cannot see where it lives.
	DriftMissing = "declared_not_observed"

	// DriftUndeclared is running and written down nowhere. A description of
	// reality rather than a task, so it is reported only when asked for
	// (ADR-0045).
	DriftUndeclared = "observed_not_declared"

	// DriftNoteRef is a note attached to a ref nothing holds. Usually what
	// removing a declaration leaves behind.
	DriftNoteRef = "note_ref_missing"
)

// DriftFilter narrows what Drift answers with. The zero value is everything
// written down that no longer resolves, which is the half an operator keeps.
type DriftFilter struct {
	// Undeclared adds what is running and nobody wrote down. Off by default:
	// it is reality reporting itself, and it buries the rows worth acting on.
	Undeclared bool
}

type driftRow struct {
	Ref        string
	Kind       string
	Title      string
	Repository string
}

// Drift reports what the catalog claims and reality does not support: declared
// and nowhere found, or a note pointing at nothing. Limited to what v may see
// (ADR-0051); running and undeclared comes only on request (ADR-0045).
func (db *DB) Drift(ctx context.Context, gitRef string, filter DriftFilter, v Visibility) ([]Drift, error) {
	drifts, err := db.declaredNotObserved(ctx, gitRef, v)
	if err != nil {
		return nil, err
	}
	notes, err := db.notesWithoutEntity(ctx, gitRef, v)
	if err != nil {
		return nil, err
	}
	drifts = append(drifts, notes...)

	if !filter.Undeclared {
		return drifts, nil
	}
	undeclared, err := db.observedNotDeclared(ctx, gitRef, v)
	if err != nil {
		return nil, err
	}
	return append(drifts, undeclared...), nil
}

// notesWithoutEntity finds open notes attached to a ref nothing holds, which
// is where ADR-0031's unchecked refs stop being silent. Closing one clears it:
// the report says to, and a queue ignoring its own advice cannot be emptied.
func (db *DB) notesWithoutEntity(ctx context.Context, gitRef string, v Visibility) ([]Drift, error) {
	clause, args := scopeClause("note_refs", gitRef)
	entityScope, entityArgs := v.within("e", gitRef)
	open, openArgs := openNote("n")

	query := db.gorm.WithContext(ctx).
		Model(&noteRefRow{}).
		Select("note_refs.ref as target, group_concat(DISTINCT note_refs.note_id) as places").
		Where(clause, args...).
		Where("NOT EXISTS (SELECT 1 FROM entities e WHERE e.ref = note_refs.ref AND "+entityScope+")", entityArgs...).
		Where("EXISTS (SELECT 1 FROM notes n WHERE n.repository = note_refs.repository"+
			" AND n.git_ref = note_refs.git_ref AND n.note_id = note_refs.note_id AND "+open+")", openArgs...)

	var rows []danglingRow
	err := visible(query, v, "note_refs").
		Group("note_refs.ref").
		Order("note_refs.ref").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: find notes without an entity: %w", err)
	}

	drifts := make([]Drift, 0, len(rows))
	for _, row := range rows {
		drifts = append(drifts, Drift{
			Kind: DriftNoteRef, Ref: row.Target,
			Declared: strings.Join(splitOn(row.Places, ","), ", "),
			Detail:   "notes point at this and nothing holds it. Either the ref is wrong, or what it named is gone and the notes want moving or closing",
		})
	}
	return drifts, nil
}

func (db *DB) declaredNotObserved(ctx context.Context, gitRef string, v Visibility) ([]Drift, error) {
	rows, err := db.compare(ctx, gitRef, false, v)
	if err != nil {
		return nil, err
	}

	drifts := make([]Drift, 0, len(rows))
	for _, row := range rows {
		drifts = append(drifts, Drift{
			Kind: DriftMissing, Ref: row.Ref, Title: row.Title,
			Declared: row.Repository,
			Detail:   "declared in the catalog, and no ingester can find it. Either it is gone, or nothing is watching where it runs",
		})
	}
	return drifts, nil
}

// observedNotDeclared lists what is running and written down nowhere, minus
// the kinds minted as reference. ADR-0045 wanted exactly this and was blocked
// on an interface; a mint is the operator saying it instead (ADR-0048).
func (db *DB) observedNotDeclared(ctx context.Context, gitRef string, v Visibility) ([]Drift, error) {
	rows, err := db.compare(ctx, gitRef, true, v)
	if err != nil {
		return nil, err
	}

	minted, err := db.Minted(ctx, gitRef)
	if err != nil {
		return nil, err
	}
	reference := namesWithRole(minted, vocab.Entity, vocab.Reference)

	drifts := make([]Drift, 0, len(rows))
	for _, row := range rows {
		if slices.Contains(reference, row.Kind) {
			continue
		}
		drifts = append(drifts, Drift{
			Kind: DriftUndeclared, Ref: row.Ref, Title: row.Title,
			Observed: row.Repository,
			Detail:   "running, and nobody has written it down. Declare it to say what it is for",
		})
	}
	return drifts, nil
}

// compare finds refs on one side and absent on the other, where an
// `observed_as` alias counts as a match. Without the alias every entity would
// appear on both sides, because a human and an ingester never pick one name.
// Declared rows are limited to what something watches, because absence needs
// an observer to be absent from (ADR-0056).
func (db *DB) compare(ctx context.Context, gitRef string, observed bool, v Visibility) ([]driftRow, error) {
	clause, args := scopeClause("", gitRef)
	otherScope, otherArgs := v.within("other", gitRef)
	aliasScope, aliasArgs := v.within("entity_aliases", gitRef)

	matched := "NOT EXISTS (SELECT 1 FROM entities other WHERE other.observed = ? AND " + otherScope +
		" AND (other.ref = entities.ref OR EXISTS (SELECT 1 FROM entity_aliases WHERE " + aliasScope + " AND "
	if observed {
		// Asking about an observed row: it is claimed if some declaration
		// names it as an alias.
		matched += "entity_aliases.alias = entities.ref)))"
	} else {
		// Asking about a declared row: it is matched if one of its own
		// aliases is what the ingester saw.
		matched += "entity_aliases.ref = entities.ref AND entity_aliases.alias = other.ref)))"
	}

	query := visible(db.gorm.WithContext(ctx).Model(&entityRow{}), v, "").
		Select("ref, kind, title, repository").
		Where(clause, args...).
		Where("observed = ?", observed).
		Where(matched, append(append([]any{!observed}, otherArgs...), aliasArgs...)...)

	if !observed {
		watchedClause, watchedArgs := watched(gitRef, v)
		query = query.Where(watchedClause, watchedArgs...)
	}

	var rows []driftRow
	if err := query.Group("ref").Order("ref").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("index: compare declared against observed: %w", err)
	}
	return rows, nil
}

// watched limits declared rows to the ones absence says something about: a
// kind is watched only in the namespaces it was observed in, and an
// `observed_as` is the operator supplying the witness themselves (ADR-0056).
func watched(gitRef string, v Visibility) (string, []any) {
	coveredScope, coveredArgs := v.within("covered", gitRef)
	claimedScope, claimedArgs := v.within("claimed", gitRef)

	clause := "(EXISTS (SELECT 1 FROM entities covered WHERE covered.observed = ?" +
		" AND " + coveredScope + " AND covered.kind = entities.kind" +
		" AND covered.namespace = entities.namespace)" +
		" OR EXISTS (SELECT 1 FROM entity_aliases claimed WHERE " + claimedScope +
		" AND claimed.ref = entities.ref))"

	args := append([]any{true}, coveredArgs...)
	return clause, append(args, claimedArgs...)
}
