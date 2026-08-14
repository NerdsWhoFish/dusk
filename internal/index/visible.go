package index

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Visibility limits a read to what one person may see. A zero value sees
// everything, which is the single-operator posture; a set one derives from
// GitHub (ADR-0012), so Dusk's view of access cannot drift from GitHub's.
type Visibility struct {
	// Repositories the viewer can read. Nil means unrestricted.
	Repositories []string

	// Observed decides whether entities no repository backs are visible.
	// ADR-0012 gives them no natural access control, so there is no implicit
	// default: silent over-sharing is worse than a required decision.
	Observed bool
}

// Unrestricted is the zero visibility: everything.
func Unrestricted() Visibility { return Visibility{} }

// Restricted reports whether this visibility filters anything.
func (v Visibility) Restricted() bool { return v.Repositories != nil }

// clause limits a query to what the viewer may see. Observations are known by
// the reserved prefix on their repository, not by the entities table's
// `observed` column, because relations and notes carry no such column.
func (v Visibility) clause(alias string) (string, []any) {
	if !v.Restricted() {
		return "", nil
	}

	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	observed := prefix + "repository LIKE ?"

	if len(v.Repositories) == 0 {
		// An empty readable set still has to produce valid SQL, and it has to
		// match nothing rather than everything.
		if !v.Observed {
			return "1 = 0", nil
		}
		return observed, []any{observedPrefix + "%"}
	}

	args := make([]any, 0, len(v.Repositories)+1)
	for _, repository := range v.Repositories {
		args = append(args, repository)
	}

	limited := prefix + "repository IN (" + placeholders(len(v.Repositories)) + ")"
	if v.Observed {
		limited = "(" + limited + " OR " + observed + ")"
		args = append(args, observedPrefix+"%")
	}
	return limited, args
}

// within joins clause onto the ref scope for a correlated subquery. Those carry
// their predicates as text rather than as a Where of their own, so the two have
// to arrive together with their arguments already in order.
func (v Visibility) within(alias, gitRef string) (string, []any) {
	clause, args := scopeClause(alias, gitRef)
	limit, limitArgs := v.clause(alias)
	if limit == "" {
		return clause, args
	}
	return clause + " AND " + limit, append(args, limitArgs...)
}

// visible narrows a query to what v may see. It is a no-op for an unrestricted
// viewer, which is every single-operator deployment, so nobody pays for a
// filter they are not using.
func visible(tx *gorm.DB, v Visibility, alias string) *gorm.DB {
	clause, args := v.clause(alias)
	if clause == "" {
		return tx
	}
	return tx.Where(clause, args...)
}

func placeholders(n int) string {
	if n == 0 {
		return "NULL"
	}
	out := make([]byte, 0, n*3)
	for i := range n {
		if i > 0 {
			out = append(out, ',', ' ')
		}
		out = append(out, '?')
	}
	return string(out)
}

// VisibleTo returns the entities a viewer may see at gitRef.
func (db *DB) VisibleTo(ctx context.Context, gitRef string, v Visibility) ([]string, error) {
	query := visible(scoped(db.gorm.WithContext(ctx), gitRef).Model(&entityRow{}), v, "")

	var refs []string
	if err := query.Distinct().Order("ref").Pluck("ref", &refs).Error; err != nil {
		return nil, fmt.Errorf("index: list visible entities: %w", err)
	}
	return refs, nil
}
