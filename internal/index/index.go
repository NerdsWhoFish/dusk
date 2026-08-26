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
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/glebarez/sqlite"

	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// ErrNotFound is returned when a ref is not present at the git ref asked for.
var ErrNotFound = errors.New("index: not found")

// DB is the materialized graph.
type DB struct {
	gorm     *gorm.DB
	semantic *semanticIndex
}

type entityRow struct {
	Repository  string `gorm:"primaryKey;index:idx_entities_observed_ref,priority:3;index:idx_entities_observed_kind_namespace,priority:4"`
	GitRef      string `gorm:"primaryKey;index:idx_entities_observed_ref,priority:4;index:idx_entities_observed_kind_namespace,priority:5"`
	Ref         string `gorm:"primaryKey;index:idx_entities_observed_ref,priority:2"`
	Path        string
	ContentHash string

	// Observed marks a row an ingester saw rather than a human declared. It
	// orders reads: a person who wrote something down beats a machine that
	// inferred it, and without this the winner would be an ASCII accident.
	Observed bool `gorm:"index;index:idx_entities_observed_ref,priority:1;index:idx_entities_observed_kind_namespace,priority:1"`

	Kind        string `gorm:"index;index:idx_entities_observed_kind_namespace,priority:2"`
	Namespace   string `gorm:"index:idx_entities_observed_kind_namespace,priority:3"`
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

	// Status closes a note that is work. Empty means open, so a note written
	// before there was a status is not read as finished.
	Status string `gorm:"index"`

	Source     string
	Version    string
	ObservedAt time.Time
}

func (noteRow) TableName() string { return "notes" }

// note rebuilds the note. Refs are left empty: a caller reading a note by the
// entity it attaches to already knows one of them, and loading the rest would
// be a second query for something nothing has needed yet.
func (r noteRow) note() *duskv1alpha1.Note {
	return &duskv1alpha1.Note{
		Id: r.NoteID, Kind: r.Kind, Body: r.Body, Pinned: r.Pinned,
		Status: r.Status, ContentHash: r.ContentHash,
		Provenance: &duskv1alpha1.Provenance{
			Source: r.Source, Version: r.Version,
			ObservedAt: timestamppb.New(r.ObservedAt),
		},
	}
}

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
	if db.semantic != nil && db.semantic.cancel != nil {
		db.semantic.cancel()
		db.semantic.done.Wait()
	}
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
	if err := db.gorm.AutoMigrate(&entityRow{}, &relationRow{}, &noteRow{}, &noteRefRow{}, &aliasRow{}, &kindRow{}, &contextRow{}, &defaultView{}, &repositoryIdentity{}, &repositoryAlias{}, &embeddingRow{}); err != nil {
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
	`DROP TRIGGER IF EXISTS entities_fts_insert`,
	`DROP TRIGGER IF EXISTS entities_fts_delete`,
	`DROP TRIGGER IF EXISTS entities_fts_update`,
	`DROP TRIGGER IF EXISTS notes_fts_insert`,
	`DROP TRIGGER IF EXISTS notes_fts_delete`,
	`DROP TRIGGER IF EXISTS notes_fts_update`,
	`DROP TABLE IF EXISTS entity_fts`,
	`DROP TABLE IF EXISTS catalog_fts`,

	`CREATE VIRTUAL TABLE IF NOT EXISTS catalog_fts USING fts5(
		repository UNINDEXED, git_ref UNINDEXED, kind_of UNINDEXED, id UNINDEXED,
		kind, name, title, body
	)`,

	`CREATE TRIGGER IF NOT EXISTS entities_fts_insert AFTER INSERT ON entities BEGIN
		INSERT INTO catalog_fts (repository, git_ref, kind_of, id, kind, name, title, body)
		VALUES (new.repository, new.git_ref, 'entity', new.ref, new.kind, new.name, new.title,
		        new.ref || ' ' || new.description || ' ' || COALESCE(CAST(new.attributes AS TEXT), ''));
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
		VALUES (new.repository, new.git_ref, 'entity', new.ref, new.kind, new.name, new.title,
		        new.ref || ' ' || new.description || ' ' || COALESCE(CAST(new.attributes AS TEXT), ''));
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

	`INSERT INTO catalog_fts (repository, git_ref, kind_of, id, kind, name, title, body)
	 SELECT repository, git_ref, 'entity', ref, kind, name, title,
	        ref || ' ' || description || ' ' || COALESCE(CAST(attributes AS TEXT), '')
	   FROM entities`,
	`INSERT INTO catalog_fts (repository, git_ref, kind_of, id, kind, name, title, body)
	 SELECT repository, git_ref, 'note', note_id, kind, '', '', body
	   FROM notes`,
}

// Declaration is one entity and the file that declares it. They travel together
// because a write has to find its way back to that file.
type Declaration struct {
	Path        string
	ContentHash string
	Entity      *duskv1alpha1.Entity

	// ObservedAs names what an ingester calls this same thing, so drift can
	// tell "I named it differently" from "it is gone".
	ObservedAs []string
}

// aliasRow links a declared entity to what an ingester calls it.
type aliasRow struct {
	Repository string `gorm:"primaryKey;index:idx_aliases_ref_scope,priority:2;index:idx_aliases_alias_scope,priority:2"`
	GitRef     string `gorm:"primaryKey;index:idx_aliases_ref_scope,priority:3;index:idx_aliases_alias_scope,priority:3"`
	Ref        string `gorm:"primaryKey;index:idx_aliases_ref_scope,priority:1;index:idx_aliases_alias_scope,priority:4"`
	Alias      string `gorm:"primaryKey;index;index:idx_aliases_ref_scope,priority:4;index:idx_aliases_alias_scope,priority:1"`
}

func (aliasRow) TableName() string { return "entity_aliases" }

func aliasRows(repository, gitRef string, declarations []Declaration) []aliasRow {
	var rows []aliasRow
	for _, declared := range declarations {
		for _, alias := range declared.ObservedAs {
			rows = append(rows, aliasRow{
				Repository: repository, GitRef: gitRef,
				Ref: declared.Entity.GetRef(), Alias: alias,
			})
		}
	}
	return rows
}

// Put replaces what repository contributes at gitRef, in one transaction, so a
// failed reconcile leaves the previous contents rather than a half-built graph.
// Scoping to one repository keeps a push to one from re-reading all the others.
func (db *DB) Put(ctx context.Context, repository, gitRef string, declarations []Declaration, relations []*duskv1alpha1.Relation, notes []*duskv1alpha1.Note) error {
	return db.put(ctx, repository, gitRef, declarations, relations, notes, nil, nil)
}

// PutCatalog replaces a repository's graph and minted vocabulary in one transaction.
func (db *DB) PutCatalog(ctx context.Context, repository, gitRef string, declarations []Declaration, relations []*duskv1alpha1.Relation, notes []*duskv1alpha1.Note, kinds []vocab.Kind, contextProfile []byte) error {
	return db.put(ctx, repository, gitRef, declarations, relations, notes, kindRows(repository, gitRef, kinds), contextProfile)
}

func (db *DB) put(ctx context.Context, repository, gitRef string, declarations []Declaration, relations []*duskv1alpha1.Relation, notes []*duskv1alpha1.Note, kinds []kindRow, contextProfile []byte) error {
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
	aliasRows := aliasRows(repository, gitRef, declarations)

	err = db.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
			{"aliases", aliasRows, len(aliasRows)},
			{"vocabulary", kinds, len(kinds)},
		} {
			if batch.n == 0 {
				continue
			}
			if err := tx.CreateInBatches(batch.rows, batchSize).Error; err != nil {
				return fmt.Errorf("index: put %s: %w", batch.what, err)
			}
		}
		if len(contextProfile) > 0 {
			if err := tx.Create(&contextRow{Repository: repository, GitRef: gitRef, Body: contextProfile}).Error; err != nil {
				return fmt.Errorf("index: put context: %w", err)
			}
		}
		return nil
	})
	if err == nil {
		db.signalEmbeddings()
	}
	return err
}

func noteRows(repository, gitRef string, notes []*duskv1alpha1.Note) ([]noteRow, []noteRefRow) {
	rows := make([]noteRow, 0, len(notes))
	refs := make([]noteRefRow, 0, len(notes))

	for _, note := range notes {
		rows = append(rows, noteRow{
			Repository: repository, GitRef: gitRef, NoteID: note.GetId(),
			Kind: note.GetKind(), Body: note.GetBody(), Pinned: note.GetPinned(),
			Status: note.GetStatus(), ContentHash: note.GetContentHash(),
			Source:     note.GetProvenance().GetSource(),
			Version:    note.GetProvenance().GetVersion(),
			ObservedAt: note.GetProvenance().GetObservedAt().AsTime(),
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
	err := db.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return deleteWhere(tx, "git_ref = ?", gitRef)
	})
	if err == nil {
		db.signalEmbeddings()
	}
	return err
}

// DropRepository removes one repository's contents at gitRef, which is what an
// uninstall or a repository leaving the catalog needs.
func (db *DB) DropRepository(ctx context.Context, repository, gitRef string) error {
	err := db.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return deleteScope(tx, repository, gitRef)
	})
	if err == nil {
		db.signalEmbeddings()
	}
	return err
}

func deleteScope(tx *gorm.DB, repository, gitRef string) error {
	return deleteWhere(tx, "repository = ? AND git_ref = ?", repository, gitRef)
}

// deleteWhere clears every kind of row a scope holds, so that deleting a file
// from a repository actually removes what it contributed.
func deleteWhere(tx *gorm.DB, query string, args ...any) error {
	for _, target := range []struct {
		what string
		row  any
	}{
		{"relations", &relationRow{}},
		{"note refs", &noteRefRow{}},
		{"notes", &noteRow{}},
		{"aliases", &aliasRow{}},
		{"kinds", &kindRow{}},
		{"context", &contextRow{}},
		{"entities", &entityRow{}},
		{"embeddings", &embeddingRow{}},
	} {
		if err := tx.Where(query, args...).Delete(target.row).Error; err != nil {
			return fmt.Errorf("index: drop %s: %w", target.what, err)
		}
	}
	return nil
}

type contextRow struct {
	Repository string `gorm:"primaryKey"`
	GitRef     string `gorm:"primaryKey"`
	Body       []byte
}

func (contextRow) TableName() string { return "context_profiles" }

// Context returns the profile contributed by one exact repository.
func (db *DB) Context(ctx context.Context, gitRef, repository string) ([]byte, error) {
	var row contextRow
	err := scoped(db.gorm.WithContext(ctx), gitRef).Where("repository = ?", repository).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("index: context for %q: %w", repository, err)
	}
	return row.Body, nil
}

// defaultView records which ref is a repository's default branch. Repositories
// disagree about that, so no single ref means "the catalog as it stands" and a
// query given no ref spans these rows instead.
type defaultView struct {
	Repository string `gorm:"primaryKey"`
	GitRef     string
}

// repositoryIdentity follows a repository through a rename or transfer using
// GitHub's stable numeric id. The catalog rows keep the current human-readable
// slug; aliases exist only to resolve an old checkout remote to that slug.
type repositoryIdentity struct {
	ID   int64  `gorm:"primaryKey"`
	Slug string `gorm:"uniqueIndex"`
}

func (repositoryIdentity) TableName() string { return "repository_identities" }

type repositoryAlias struct {
	Alias        string `gorm:"primaryKey"`
	RepositoryID int64  `gorm:"index"`
}

func (repositoryAlias) TableName() string { return "repository_aliases" }

// TrackRepository records the stable identity behind a slug and moves the
// disposable materialized rows when GitHub reports that identity under a new
// owner or name. It returns the previous slug when one was superseded.
func (db *DB) TrackRepository(ctx context.Context, id int64, slug string) (string, error) {
	if id == 0 || strings.TrimSpace(slug) == "" {
		return "", errors.New("index: track repository: an id and slug are required")
	}

	var previous string
	err := db.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var identity repositoryIdentity
		err := tx.First(&identity, "id = ?", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&repositoryIdentity{ID: id, Slug: slug}).Error
		}
		if err != nil {
			return err
		}
		if identity.Slug == slug {
			return nil
		}

		previous = identity.Slug
		if err := tx.Save(&repositoryAlias{Alias: previous, RepositoryID: id}).Error; err != nil {
			return err
		}
		for _, table := range []string{"relations", "note_refs", "notes", "entity_aliases", "kinds", "context_profiles", "entities", "embedding_rows", "default_views"} {
			if err := tx.Table(table).Where("repository = ?", previous).Update("repository", slug).Error; err != nil {
				return fmt.Errorf("move %s: %w", table, err)
			}
		}
		identity.Slug = slug
		return tx.Save(&identity).Error
	})
	if err != nil {
		return "", fmt.Errorf("index: track repository %q: %w", slug, err)
	}
	if previous != "" {
		db.signalEmbeddings()
	}
	return previous, nil
}

// ResolveRepository returns the current slug for an exact current slug or a
// historical slug retained after a rename or transfer.
func (db *DB) ResolveRepository(ctx context.Context, candidate string) (string, error) {
	var exact int64
	if err := db.gorm.WithContext(ctx).Model(&defaultView{}).Where("repository = ?", candidate).Count(&exact).Error; err != nil {
		return "", fmt.Errorf("index: resolve repository %q: %w", candidate, err)
	}
	if exact > 0 {
		return candidate, nil
	}

	var identity repositoryIdentity
	err := db.gorm.WithContext(ctx).
		Where("slug = ? OR id IN (SELECT repository_id FROM repository_aliases WHERE alias = ?)", candidate, candidate).
		First(&identity).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("index: resolve repository %q: %w", candidate, err)
	}
	return identity.Slug, nil
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

// observedPrefix marks a partition holding what an ingester saw rather than a
// repository somebody can clone. It lives here because the index is what
// partitions and orders by it (ADR-0034).
const observedPrefix = "ingester:"

// ObservedScope is where an ingester's observations are stored, in the slot a
// repository would occupy.
func ObservedScope(name string) string { return observedPrefix + name }

// IsObserved reports whether a partition holds observations, so a caller does
// not offer to clone something that was never a repository.
func IsObserved(repository string) bool {
	return strings.HasPrefix(repository, observedPrefix)
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

// ScopeCount is one materialized partition and its entity count.
type ScopeCount struct {
	Repository string
	GitRef     string
	Entities   int
}

// ScopeCounts answers the portal's source summary in one aggregate read.
// Scopes remains the identity-only set the controller uses as map keys.
func (db *DB) ScopeCounts(ctx context.Context) ([]ScopeCount, error) {
	var counts []ScopeCount
	err := db.gorm.WithContext(ctx).Model(&entityRow{}).
		Select("repository, git_ref, count(*) as entities").
		Group("repository, git_ref").
		Order("repository, git_ref").
		Find(&counts).Error
	if err != nil {
		return nil, fmt.Errorf("index: count scopes: %w", err)
	}
	return counts, nil
}

// LastRead reports when each partition in the default view was last read, keyed
// by the repository slot, so an ingester's scope is dated like a repository. It
// is ADR-0011's observed_at, stored with the content, so it survives a restart.
func (db *DB) LastRead(ctx context.Context) (map[string]time.Time, error) {
	var rows []struct {
		Repository string
		ObservedAt time.Time
	}

	// The column itself rather than MAX of it: an aggregate loses the declared
	// type, and the driver will not scan what it cannot recognise as a time.
	// One reconcile stamps every row it writes alike, so this stays small.
	clause, args := scopeClause("", "")
	err := db.gorm.WithContext(ctx).Model(&entityRow{}).
		Distinct("repository", "observed_at").
		Where(clause, args...).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: last read: %w", err)
	}

	out := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		at := row.ObservedAt.UTC()
		if at.IsZero() || !at.After(out[row.Repository]) {
			continue
		}
		out[row.Repository] = at
	}
	return out, nil
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
			ContentHash: declared.ContentHash,
			Observed:    IsObserved(repository),
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
