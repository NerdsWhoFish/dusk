package index

import (
	"context"
	"fmt"
	"slices"
	"strings"

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"
)

// Change is one difference between the catalog at two refs, semantic rather
// than textual: reformatting a file or moving an entity between files changes
// nothing here, because what matters is what the catalog says after merge.
type Change struct {
	Kind string
	Ref  string

	// Field is what changed, for a modification.
	Field  string
	Before string
	After  string
}

const (
	// ChangeAdded is an entity the catalog does not have yet.
	ChangeAdded = "added"

	// ChangeRemoved is an entity that would stop existing.
	ChangeRemoved = "removed"

	// ChangeModified is a field whose value would change.
	ChangeModified = "modified"
)

// Diff reports what merging head into base would do to the catalog.
func (db *DB) Diff(ctx context.Context, base, head string) ([]Change, error) {
	before, err := db.entitiesAt(ctx, base)
	if err != nil {
		return nil, err
	}
	after, err := db.entitiesAt(ctx, head)
	if err != nil {
		return nil, err
	}

	var changes []Change
	for _, ref := range sortedRefs(before, after) {
		old, hadOld := before[ref]
		fresh, hasNew := after[ref]

		switch {
		case !hadOld:
			changes = append(changes, Change{Kind: ChangeAdded, Ref: ref, After: describe(fresh)})
		case !hasNew:
			changes = append(changes, Change{Kind: ChangeRemoved, Ref: ref, Before: describe(old)})
		default:
			changes = append(changes, compareEntities(ref, old, fresh)...)
		}
	}
	return changes, nil
}

func (db *DB) entitiesAt(ctx context.Context, gitRef string) (map[string]*duskv1alpha1.Entity, error) {
	entities, err := db.List(ctx, gitRef, "")
	if err != nil {
		return nil, fmt.Errorf("index: read %q for a diff: %w", gitRef, err)
	}

	byRef := make(map[string]*duskv1alpha1.Entity, len(entities))
	for _, entity := range entities {
		byRef[entity.GetRef()] = entity
	}
	return byRef, nil
}

// compareEntities reports field-level changes. Provenance is skipped: every
// entity's commit differs between two refs, and reporting that would bury the
// changes a reviewer actually cares about.
func compareEntities(ref string, before, after *duskv1alpha1.Entity) []Change {
	var changes []Change

	for _, field := range []struct {
		name          string
		before, after string
	}{
		{"title", before.GetTitle(), after.GetTitle()},
		{"kind", before.GetKind(), after.GetKind()},
		{"namespace", before.GetNamespace(), after.GetNamespace()},
		{"description", before.GetDescription(), after.GetDescription()},
	} {
		if field.before != field.after {
			changes = append(changes, Change{
				Kind: ChangeModified, Ref: ref, Field: field.name,
				Before: field.before, After: field.after,
			})
		}
	}

	return append(changes, compareAttributes(ref, before, after)...)
}

func compareAttributes(ref string, before, after *duskv1alpha1.Entity) []Change {
	old := before.GetAttributes().GetFields()
	fresh := after.GetAttributes().GetFields()

	keys := make([]string, 0, len(old)+len(fresh))
	for key := range old {
		keys = append(keys, key)
	}
	for key := range fresh {
		if _, seen := old[key]; !seen {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)

	var changes []Change
	for _, key := range keys {
		wasValue, hadOld := old[key]
		isValue, hasNew := fresh[key]

		was, is := "", ""
		if hadOld {
			was = fmt.Sprint(wasValue.AsInterface())
		}
		if hasNew {
			is = fmt.Sprint(isValue.AsInterface())
		}
		if was != is {
			changes = append(changes, Change{
				Kind: ChangeModified, Ref: ref, Field: "attribute " + key,
				Before: was, After: is,
			})
		}
	}
	return changes
}

func sortedRefs(before, after map[string]*duskv1alpha1.Entity) []string {
	seen := make(map[string]bool, len(before)+len(after))
	refs := make([]string, 0, len(before)+len(after))

	for _, source := range []map[string]*duskv1alpha1.Entity{before, after} {
		for ref := range source {
			if !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	slices.Sort(refs)
	return refs
}

func describe(entity *duskv1alpha1.Entity) string {
	name := entity.GetTitle()
	if name == "" {
		name = entity.GetName()
	}
	return strings.TrimSpace(entity.GetKind() + " " + name)
}
