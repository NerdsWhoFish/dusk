// Package index is the materialized entity graph, keyed by git ref.
//
// Several refs are live at once so that a pull request can be rendered as the
// catalog as it would be after merge, and dropping one is a single delete.
// The index is derived and disposable: deleting it loses nothing that
// reconciling from git cannot rebuild.
//
// It is internal deliberately. The schema is disposable by contract, and
// exporting it would promise stability to something designed to be thrown away.
package index

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/glebarez/sqlite"
)

// ErrNotFound is returned when a ref is not present at the git ref asked for.
var ErrNotFound = errors.New("index: not found")

// DB is the materialized graph.
type DB struct {
	gorm *gorm.DB
}

type entityRow struct {
	Repository  string `gorm:"primaryKey"`
	GitRef      string `gorm:"primaryKey"`
	Ref         string `gorm:"primaryKey"`
	Path        string
	Kind        string `gorm:"index"`
	Namespace   string
	Name        string
	Title       string
	Description string
	Attributes  []byte
	Source      string
	Version     string
	ObservedAt  time.Time
}

func (entityRow) TableName() string { return "entities" }

type relationRow struct {
	Repository string `gorm:"primaryKey"`
	GitRef     string `gorm:"primaryKey"`
	FromRef    string `gorm:"primaryKey"`
	ToRef      string `gorm:"primaryKey"`
	Type       string `gorm:"primaryKey"`
	Attributes []byte
	Source     string
	Version    string
	ObservedAt time.Time
}

func (relationRow) TableName() string { return "relations" }

type noteRow struct {
	Repository string `gorm:"primaryKey"`
	GitRef     string `gorm:"primaryKey"`

	// NoteID is the note's path in its repository, which ADR-0031 makes the id
	// because a path is already unique, stable and meaningful in a diff.
	NoteID string `gorm:"primaryKey"`

	Kind        string `gorm:"index"`
	Body        string
	Pinned      bool
	ContentHash string `gorm:"index"`
	Source      string
	Version     string
	ObservedAt  time.Time
}

func (noteRow) TableName() string { return "notes" }

// noteRefRow attaches a note to an entity. A note names several, so the link is
// its own table rather than a column.
type noteRefRow struct {
	Repository string `gorm:"primaryKey"`
	GitRef     string `gorm:"primaryKey"`
	NoteID     string `gorm:"primaryKey"`
	Ref        string `gorm:"primaryKey;index"`
}

func (noteRefRow) TableName() string { return "note_refs" }

// Open opens the index at path, creating it if necessary. Use ":memory:" for
// an ephemeral one.
func Open(path string) (*DB, error) {
	gormDB, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("index: open %q: %w", path, err)
	}

	db := &DB{gorm: gormDB}
	if err := db.migrate(); err != nil {
		return nil, err
	}
	return db, nil
}

// Close releases the underlying database handle.
func (db *DB) Close() error {
	sqlDB, err := db.gorm.DB()
	if err != nil {
		return fmt.Errorf("index: close: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("index: close: %w", err)
	}
	return nil
}

func (db *DB) migrate() error {
	// WAL keeps a reconcile from blocking readers. It is a no-op in memory.
	if err := db.gorm.Exec(`PRAGMA journal_mode=WAL`).Error; err != nil {
		return fmt.Errorf("index: enable WAL: %w", err)
	}
	if err := db.gorm.Exec(`PRAGMA foreign_keys=ON`).Error; err != nil {
		return fmt.Errorf("index: enable foreign keys: %w", err)
	}
	if err := db.gorm.AutoMigrate(&entityRow{}, &relationRow{}, &noteRow{}, &noteRefRow{}, &defaultView{}); err != nil {
		return fmt.Errorf("index: migrate: %w", err)
	}
	for _, stmt := range ftsSchema {
		if err := db.gorm.Exec(stmt).Error; err != nil {
			return fmt.Errorf("index: create search index: %w", err)
		}
	}
	return nil
}

// ftsSchema mirrors catalog text into one FTS5 table through triggers. Entities
// and notes share it because ADR-0010 wants one ranked search over both, and
// two tables can only be concatenated, never ranked against each other.
var ftsSchema = []string{
	// The index is disposable, so replacing the shape costs a reconcile.
	`DROP TRIGGER IF EXISTS entity_fts_insert`,
	`DROP TRIGGER IF EXISTS entity_fts_delete`,
	`DROP TRIGGER IF EXISTS entity_fts_update`,
	`DROP TABLE IF EXISTS entity_fts`,

	`CREATE VIRTUAL TABLE IF NOT EXISTS catalog_fts USING fts5(
		repository UNINDEXED, git_ref UNINDEXED, kind_of UNINDEXED, id UNINDEXED,
		kind, name, title, body
	)`,

	`CREATE TRIGGER IF NOT EXISTS entities_fts_insert AFTER INSERT ON entities BEGIN
		INSERT INTO catalog_fts (repository, git_ref, kind_of, id, kind, name, title, body)
		VALUES (new.repository, new.git_ref, 'entity', new.ref, new.kind, new.name, new.title, new.description);
	END`,
	`CREATE TRIGGER IF NOT EXISTS entities_fts_delete AFTER DELETE ON entities BEGIN
		DELETE FROM catalog_fts
		 WHERE repository = old.repository AND git_ref = old.git_ref
		   AND kind_of = 'entity' AND id = old.ref;
	END`,
	`CREATE TRIGGER IF NOT EXISTS entities_fts_update AFTER UPDATE ON entities BEGIN
		DELETE FROM catalog_fts
		 WHERE repository = old.repository AND git_ref = old.git_ref
		   AND kind_of = 'entity' AND id = old.ref;
		INSERT INTO catalog_fts (repository, git_ref, kind_of, id, kind, name, title, body)
		VALUES (new.repository, new.git_ref, 'entity', new.ref, new.kind, new.name, new.title, new.description);
	END`,

	`CREATE TRIGGER IF NOT EXISTS notes_fts_insert AFTER INSERT ON notes BEGIN
		INSERT INTO catalog_fts (repository, git_ref, kind_of, id, kind, name, title, body)
		VALUES (new.repository, new.git_ref, 'note', new.note_id, new.kind, '', '', new.body);
	END`,
	`CREATE TRIGGER IF NOT EXISTS notes_fts_delete AFTER DELETE ON notes BEGIN
		DELETE FROM catalog_fts
		 WHERE repository = old.repository AND git_ref = old.git_ref
		   AND kind_of = 'note' AND id = old.note_id;
	END`,
	`CREATE TRIGGER IF NOT EXISTS notes_fts_update AFTER UPDATE ON notes BEGIN
		DELETE FROM catalog_fts
		 WHERE repository = old.repository AND git_ref = old.git_ref
		   AND kind_of = 'note' AND id = old.note_id;
		INSERT INTO catalog_fts (repository, git_ref, kind_of, id, kind, name, title, body)
		VALUES (new.repository, new.git_ref, 'note', new.note_id, new.kind, '', '', new.body);
	END`,
}

// Declaration is one entity and the file that declares it. They travel together
// because a write has to find its way back to that file.
type Declaration struct {
	Path   string
	Entity *duskv1alpha1.Entity
}

// Put replaces what repository contributes at gitRef, in one transaction, so a
// failed reconcile leaves the previous contents rather than a half-built graph.
// Scoping to one repository keeps a push to one from re-reading all the others.
func (db *DB) Put(ctx context.Context, repository, gitRef string, declarations []Declaration, relations []*duskv1alpha1.Relation, notes []*duskv1alpha1.Note) error {
	if repository == "" || gitRef == "" {
		return errors.New("index: put: a repository and a git ref are both required")
	}

	entityRows, err := entityRows(repository, gitRef, declarations)
	if err != nil {
		return err
	}
	relationRows, err := relationRows(repository, gitRef, relations)
	if err != nil {
		return err
	}
	noteRows, noteRefRows := noteRows(repository, gitRef, notes)

	return db.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := deleteScope(tx, repository, gitRef); err != nil {
			return err
		}
		for _, batch := range []struct {
			what string
			rows any
			n    int
		}{
			{"entities", entityRows, len(entityRows)},
			{"relations", relationRows, len(relationRows)},
			{"notes", noteRows, len(noteRows)},
			{"note refs", noteRefRows, len(noteRefRows)},
		} {
			if batch.n == 0 {
				continue
			}
			if err := tx.CreateInBatches(batch.rows, batchSize).Error; err != nil {
				return fmt.Errorf("index: put %s: %w", batch.what, err)
			}
		}
		return nil
	})
}

func noteRows(repository, gitRef string, notes []*duskv1alpha1.Note) ([]noteRow, []noteRefRow) {
	rows := make([]noteRow, 0, len(notes))
	refs := make([]noteRefRow, 0, len(notes))

	for _, note := range notes {
		rows = append(rows, noteRow{
			Repository: repository, GitRef: gitRef, NoteID: note.GetId(),
			Kind: note.GetKind(), Body: note.GetBody(), Pinned: note.GetPinned(),
			ContentHash: note.GetContentHash(),
			Source:      note.GetProvenance().GetSource(),
			Version:     note.GetProvenance().GetVersion(),
			ObservedAt:  note.GetProvenance().GetObservedAt().AsTime(),
		})
		for _, ref := range note.GetRefs() {
			refs = append(refs, noteRefRow{
				Repository: repository, GitRef: gitRef, NoteID: note.GetId(), Ref: ref,
			})
		}
	}
	return rows, refs
}

// batchSize keeps a reconcile of a large repository under SQLite's limit on
// bound parameters per statement.
const batchSize = 200

// DropGitRef removes every repository's contents at gitRef, which is how a
// closed pull request's preview is garbage collected.
func (db *DB) DropGitRef(ctx context.Context, gitRef string) error {
	return db.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return deleteWhere(tx, "git_ref = ?", gitRef)
	})
}

// DropRepository removes one repository's contents at gitRef, which is what an
// uninstall or a repository leaving the catalog needs.
func (db *DB) DropRepository(ctx context.Context, repository, gitRef string) error {
	return db.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return deleteScope(tx, repository, gitRef)
	})
}

func deleteScope(tx *gorm.DB, repository, gitRef string) error {
	return deleteWhere(tx, "repository = ? AND git_ref = ?", repository, gitRef)
}

func deleteWhere(tx *gorm.DB, query string, args ...any) error {
	if err := tx.Where(query, args...).Delete(&relationRow{}).Error; err != nil {
		return fmt.Errorf("index: drop relations: %w", err)
	}
	if err := tx.Where(query, args...).Delete(&entityRow{}).Error; err != nil {
		return fmt.Errorf("index: drop entities: %w", err)
	}
	return nil
}

// defaultView records which ref is a repository's default branch. Repositories
// disagree about that, so no single ref means "the catalog as it stands" and a
// query given no ref spans these rows instead.
type defaultView struct {
	Repository string `gorm:"primaryKey"`
	GitRef     string
}

func (defaultView) TableName() string { return "default_views" }

// SetDefaultView records repository's default branch as part of the default
// view. Calling it with a different ref replaces the previous one.
func (db *DB) SetDefaultView(ctx context.Context, repository, gitRef string) error {
	if repository == "" || gitRef == "" {
		return errors.New("index: default view: a repository and a git ref are both required")
	}
	err := db.gorm.WithContext(ctx).Save(&defaultView{Repository: repository, GitRef: gitRef}).Error
	if err != nil {
		return fmt.Errorf("index: record default view for %q: %w", repository, err)
	}
	return nil
}

// scopeClause confines a query to one ref, or to the default view when gitRef
// is empty. The alias is the table it applies to, which the recursive traversal
// needs and the rest leave blank.
func scopeClause(alias, gitRef string) (string, []any) {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	if gitRef != "" {
		return prefix + "git_ref = ?", []any{gitRef}
	}
	return "(" + prefix + "repository, " + prefix + "git_ref) IN (SELECT repository, git_ref FROM default_views)", nil
}

// scoped applies scopeClause to a GORM query.
func scoped(tx *gorm.DB, gitRef string) *gorm.DB {
	clause, args := scopeClause("", gitRef)
	return tx.Where(clause, args...)
}

// Scope is one materialized partition: a repository at a git ref.
type Scope struct {
	Repository string
	GitRef     string
}

// Scopes lists every partition currently materialized, which is how a sweep
// finds contents belonging to a repository it can no longer see.
func (db *DB) Scopes(ctx context.Context) ([]Scope, error) {
	var scopes []Scope
	err := db.gorm.WithContext(ctx).Model(&entityRow{}).
		Distinct("repository", "git_ref").
		Order("repository, git_ref").
		Find(&scopes).Error
	if err != nil {
		return nil, fmt.Errorf("index: list scopes: %w", err)
	}
	return scopes, nil
}

// Participates reports whether a repository contributes to the catalog at this
// ref. A delivery asks before spending a request on the push, so the answer
// comes from SQLite, and unlike an in-memory one it survives a restart.
func (db *DB) Participates(ctx context.Context, repository, gitRef string) (bool, error) {
	var count int64
	err := db.gorm.WithContext(ctx).Model(&entityRow{}).
		Where("repository = ? AND git_ref = ?", repository, gitRef).
		Limit(1).Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("index: check participation of %q: %w", repository, err)
	}
	return count > 0, nil
}

// GitRefs lists the git refs currently materialized.
func (db *DB) GitRefs(ctx context.Context) ([]string, error) {
	var refs []string
	err := db.gorm.WithContext(ctx).Model(&entityRow{}).
		Distinct().Order("git_ref").Pluck("git_ref", &refs).Error
	if err != nil {
		return nil, fmt.Errorf("index: list git refs: %w", err)
	}
	return refs, nil
}

func entityRows(repository, gitRef string, declarations []Declaration) ([]entityRow, error) {
	rows := make([]entityRow, 0, len(declarations))
	for _, declared := range declarations {
		e := declared.Entity
		attributes, err := marshalStruct(e.GetAttributes())
		if err != nil {
			return nil, fmt.Errorf("index: entity %q: %w", e.GetRef(), err)
		}
		rows = append(rows, entityRow{
			Repository:  repository,
			GitRef:      gitRef,
			Path:        declared.Path,
			Ref:         e.GetRef(),
			Kind:        e.GetKind(),
			Namespace:   e.GetNamespace(),
			Name:        e.GetName(),
			Title:       e.GetTitle(),
			Description: e.GetDescription(),
			Attributes:  attributes,
			Source:      e.GetProvenance().GetSource(),
			Version:     e.GetProvenance().GetVersion(),
			ObservedAt:  e.GetProvenance().GetObservedAt().AsTime(),
		})
	}
	return rows, nil
}

func relationRows(repository, gitRef string, relations []*duskv1alpha1.Relation) ([]relationRow, error) {
	rows := make([]relationRow, 0, len(relations))
	for _, r := range relations {
		attributes, err := marshalStruct(r.GetAttributes())
		if err != nil {
			return nil, fmt.Errorf("index: relation %q -> %q: %w", r.GetFrom(), r.GetTo(), err)
		}
		rows = append(rows, relationRow{
			Repository: repository,
			GitRef:     gitRef,
			FromRef:    r.GetFrom(),
			ToRef:      r.GetTo(),
			Type:       r.GetType(),
			Attributes: attributes,
			Source:     r.GetProvenance().GetSource(),
			Version:    r.GetProvenance().GetVersion(),
			ObservedAt: r.GetProvenance().GetObservedAt().AsTime(),
		})
	}
	return rows, nil
}

func marshalStruct(s *structpb.Struct) ([]byte, error) {
	if s == nil {
		return nil, nil
	}
	data, err := protojson.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode attributes: %w", err)
	}
	return data, nil
}

func unmarshalStruct(data []byte) (*structpb.Struct, error) {
	if len(data) == 0 {
		return nil, nil
	}
	s := &structpb.Struct{}
	if err := protojson.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("index: decode attributes: %w", err)
	}
	return s, nil
}

func provenance(source, version string, observedAt time.Time) *duskv1alpha1.Provenance {
	p := &duskv1alpha1.Provenance{Source: source, Version: version}
	if !observedAt.IsZero() {
		p.ObservedAt = timestamppb.New(observedAt)
	}
	return p
}
