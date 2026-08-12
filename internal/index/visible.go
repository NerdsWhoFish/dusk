package index

import (
	"context"
	"fmt"
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

// clause limits a query to what the viewer may see.
func (v Visibility) clause(alias string) (string, []any) {
	if !v.Restricted() {
		return "", nil
	}

	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}

	// An empty readable set still has to produce valid SQL, and it has to
	// match nothing rather than everything.
	if len(v.Repositories) == 0 && !v.Observed {
		return "1 = 0", nil
	}

	args := make([]any, 0, len(v.Repositories)+1)
	for _, repository := range v.Repositories {
		args = append(args, repository)
	}

	if len(v.Repositories) == 0 {
		return prefix + "observed = ?", []any{true}
	}

	clause := prefix + "repository IN (" + placeholders(len(v.Repositories)) + ")"
	if v.Observed {
		clause = "(" + clause + " OR " + prefix + "observed = ?)"
		args = append(args, true)
	}
	return clause, args
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
	query := scoped(db.gorm.WithContext(ctx), gitRef).Model(&entityRow{})
	if clause, args := v.clause(""); clause != "" {
		query = query.Where(clause, args...)
	}

	var refs []string
	if err := query.Distinct().Order("ref").Pluck("ref", &refs).Error; err != nil {
		return nil, fmt.Errorf("index: list visible entities: %w", err)
	}
	return refs, nil
}
