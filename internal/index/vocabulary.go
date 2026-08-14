package index

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// kindRow is a kind somebody minted. Everything else in the vocabulary is
// counted out of the entities and notes that carry it, so only the two facts
// counting cannot reach are stored (ADR-0048).
type kindRow struct {
	Repository string `gorm:"primaryKey"`
	GitRef     string `gorm:"primaryKey"`
	Namespace  string `gorm:"primaryKey"`
	Name       string `gorm:"primaryKey"`

	Role string

	// Aliases are joined, because a kind name is an identifier and cannot
	// contain the separator.
	Aliases string
}

func (kindRow) TableName() string { return "kinds" }

const aliasSeparator = ","

func (r kindRow) kind() vocab.Kind {
	var aliases []string
	if r.Aliases != "" {
		aliases = strings.Split(r.Aliases, aliasSeparator)
	}
	return vocab.Kind{
		Namespace: vocab.Namespace(r.Namespace),
		Name:      r.Name,
		Role:      vocab.Role(r.Role),
		Aliases:   aliases,
		Minted:    true,
	}
}

// PutVocabulary replaces the kinds repository mints at gitRef. It is its own
// write because the vocabulary is about the graph rather than part of it, and
// losing it degrades to the derived one (ADR-0048).
func (db *DB) PutVocabulary(ctx context.Context, repository, gitRef string, kinds []vocab.Kind) error {
	if repository == "" || gitRef == "" {
		return fmt.Errorf("index: put vocabulary: a repository and a git ref are both required")
	}

	rows := make([]kindRow, 0, len(kinds))
	for _, kind := range kinds {
		rows = append(rows, kindRow{
			Repository: repository, GitRef: gitRef,
			Namespace: string(kind.Namespace), Name: kind.Name,
			Role:    string(kind.Role),
			Aliases: strings.Join(kind.Aliases, aliasSeparator),
		})
	}

	return db.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Where("repository = ? AND git_ref = ?", repository, gitRef).Delete(&kindRow{}).Error
		if err != nil {
			return fmt.Errorf("index: drop vocabulary: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		if err := tx.CreateInBatches(rows, batchSize).Error; err != nil {
			return fmt.Errorf("index: put vocabulary: %w", err)
		}
		return nil
	})
}

// Minted returns the kinds somebody declared. It is what resolves a role, and
// it is separate from Vocabulary because ranking a page of notes wants the
// overlay without the counts.
func (db *DB) Minted(ctx context.Context, gitRef string) ([]vocab.Kind, error) {
	var rows []kindRow
	err := scoped(db.gorm.WithContext(ctx), gitRef).
		Model(&kindRow{}).
		Order("namespace, name").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: read the minted vocabulary at %q: %w", gitRef, err)
	}

	kinds := make([]vocab.Kind, 0, len(rows))
	for _, row := range rows {
		kinds = append(kinds, row.kind())
	}
	return kinds, nil
}

// Vocabulary is every kind the catalog knows: minted, seeded, or merely
// carried. Counts come from the rows themselves, so it cannot go stale, and a
// kind matching a minted alias counts against what it aliases.
func (db *DB) Vocabulary(ctx context.Context, gitRef string) ([]vocab.Kind, error) {
	minted, err := db.Minted(ctx, gitRef)
	if err != nil {
		return nil, err
	}

	kinds := append(seeded(minted), minted...)

	counted, err := db.carried(ctx, gitRef)
	if err != nil {
		return nil, err
	}
	for _, carried := range counted {
		kinds = add(kinds, carried)
	}

	slices.SortFunc(kinds, func(a, b vocab.Kind) int {
		if a.Namespace != b.Namespace {
			return strings.Compare(string(a.Namespace), string(b.Namespace))
		}
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Name, b.Name)
	})
	return kinds, nil
}

// seeded returns the well-known note kinds nobody has minted over. Minting one
// replaces it rather than sitting beside it, so `todo` can be re-roled.
func seeded(minted []vocab.Kind) []vocab.Kind {
	var out []vocab.Kind
	for _, kind := range vocab.WellKnown() {
		if _, ok := vocab.Lookup(kind.Namespace, kind.Name, minted); ok {
			continue
		}
		out = append(out, kind)
	}
	return out
}

// add folds a carried kind into the vocabulary, against its alias when it has
// one, and as a new entry when nobody has said anything about it.
func add(kinds []vocab.Kind, carried vocab.Kind) []vocab.Kind {
	for i, kind := range kinds {
		if kind.Namespace != carried.Namespace {
			continue
		}
		if _, ok := vocab.Lookup(kind.Namespace, carried.Name, []vocab.Kind{kind}); !ok {
			continue
		}
		kinds[i].Count += carried.Count
		return kinds
	}

	carried.Role = vocab.DefaultRole(carried.Namespace)
	return append(kinds, carried)
}

// carried counts what entities and notes actually use.
func (db *DB) carried(ctx context.Context, gitRef string) ([]vocab.Kind, error) {
	entities, err := db.Kinds(ctx, gitRef)
	if err != nil {
		return nil, err
	}

	var notes []KindCount
	err = scoped(db.gorm.WithContext(ctx), gitRef).
		Model(&noteRow{}).
		Select("kind, count(*) as count").
		Group("kind").
		Order("count DESC, kind").
		Find(&notes).Error
	if err != nil {
		return nil, fmt.Errorf("index: count note kinds at %q: %w", gitRef, err)
	}

	out := make([]vocab.Kind, 0, len(entities)+len(notes))
	for _, count := range entities {
		out = append(out, vocab.Kind{Namespace: vocab.Entity, Name: count.Kind, Count: count.Count})
	}
	for _, count := range notes {
		out = append(out, vocab.Kind{Namespace: vocab.Note, Name: count.Kind, Count: count.Count})
	}
	return out, nil
}

// namesWithRole lists every name resolving to role, including aliases, which is
// what a query needs to match the `kind` column against.
func namesWithRole(minted []vocab.Kind, namespace vocab.Namespace, role vocab.Role) []string {
	var candidates []string
	if namespace == vocab.Note {
		candidates = append(candidates, vocab.Names(vocab.WithRole(vocab.WellKnown(), role))...)
	}
	for _, kind := range minted {
		if kind.Namespace != namespace || kind.Role != role {
			continue
		}
		candidates = append(candidates, kind.Name)
		candidates = append(candidates, kind.Aliases...)
	}

	// A mint over a well-known kind re-roles it, so the seeded name is dropped
	// when the overlay disagrees.
	names := make([]string, 0, len(candidates))
	for _, name := range candidates {
		if vocab.RoleOf(namespace, name, minted) != role || slices.Contains(names, name) {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// anyOf widens names into bound arguments, for a query that assembles its own
// argument list rather than letting the driver expand a slice.
func anyOf(names []string) []any {
	args := make([]any, 0, len(names))
	for _, name := range names {
		args = append(args, name)
	}
	return args
}
