package index

import (
	"context"
	"fmt"
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

	// DriftUndeclared is running and written down nowhere. The common case at
	// first contact, and the queue of things worth documenting.
	DriftUndeclared = "observed_not_declared"
)

type driftRow struct {
	Ref        string
	Title      string
	Repository string
}

// Drift compares the declared half of the catalog against the observed half.
// Nothing is missing until something is looking for it, so a kind no ingester
// reports is left alone rather than named as entirely gone.
func (db *DB) Drift(ctx context.Context, gitRef string) ([]Drift, error) {
	missing, err := db.declaredNotObserved(ctx, gitRef)
	if err != nil {
		return nil, err
	}
	undeclared, err := db.observedNotDeclared(ctx, gitRef)
	if err != nil {
		return nil, err
	}
	return append(missing, undeclared...), nil
}

func (db *DB) declaredNotObserved(ctx context.Context, gitRef string) ([]Drift, error) {
	rows, err := db.compare(ctx, gitRef, false)
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

func (db *DB) observedNotDeclared(ctx context.Context, gitRef string) ([]Drift, error) {
	rows, err := db.compare(ctx, gitRef, true)
	if err != nil {
		return nil, err
	}

	drifts := make([]Drift, 0, len(rows))
	for _, row := range rows {
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
// Declared rows are limited to kinds something observes, because absence needs
// an observer to be absent from.
func (db *DB) compare(ctx context.Context, gitRef string, observed bool) ([]driftRow, error) {
	clause, args := scopeClause("", gitRef)
	otherScope, otherArgs := scopeClause("other", gitRef)
	aliasScope, aliasArgs := scopeClause("entity_aliases", gitRef)

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

	query := db.gorm.WithContext(ctx).Model(&entityRow{}).
		Select("ref, title, repository").
		Where(clause, args...).
		Where("observed = ?", observed).
		Where(matched, append(append([]any{!observed}, otherArgs...), aliasArgs...)...)

	if !observed {
		watchedScope, watchedArgs := scopeClause("watched", gitRef)
		query = query.Where("EXISTS (SELECT 1 FROM entities watched WHERE watched.observed = ?"+
			" AND "+watchedScope+" AND watched.kind = entities.kind)",
			append([]any{true}, watchedArgs...)...)
	}

	var rows []driftRow
	if err := query.Group("ref").Order("ref").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("index: compare declared against observed: %w", err)
	}
	return rows, nil
}
