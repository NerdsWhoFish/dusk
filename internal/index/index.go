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
	GitRef      string `gorm:"primaryKey"`
	Ref         string `gorm:"primaryKey"`
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
	if err := db.gorm.AutoMigrate(&entityRow{}, &relationRow{}); err != nil {
		return fmt.Errorf("index: migrate: %w", err)
	}
	for _, stmt := range ftsSchema {
		if err := db.gorm.Exec(stmt).Error; err != nil {
			return fmt.Errorf("index: create search index: %w", err)
		}
	}
	return nil
}

// ftsSchema mirrors entity text into FTS5 through triggers, so a second writer
// cannot forget to keep search in step.
var ftsSchema = []string{
	`CREATE VIRTUAL TABLE IF NOT EXISTS entity_fts USING fts5(
		git_ref UNINDEXED, ref UNINDEXED, kind, name, title, description
	)`,
	`CREATE TRIGGER IF NOT EXISTS entity_fts_insert AFTER INSERT ON entities BEGIN
		INSERT INTO entity_fts (git_ref, ref, kind, name, title, description)
		VALUES (new.git_ref, new.ref, new.kind, new.name, new.title, new.description);
	END`,
	`CREATE TRIGGER IF NOT EXISTS entity_fts_delete AFTER DELETE ON entities BEGIN
		DELETE FROM entity_fts WHERE git_ref = old.git_ref AND ref = old.ref;
	END`,
	`CREATE TRIGGER IF NOT EXISTS entity_fts_update AFTER UPDATE ON entities BEGIN
		DELETE FROM entity_fts WHERE git_ref = old.git_ref AND ref = old.ref;
		INSERT INTO entity_fts (git_ref, ref, kind, name, title, description)
		VALUES (new.git_ref, new.ref, new.kind, new.name, new.title, new.description);
	END`,
}

// Put replaces everything stored at gitRef, because git already gives the
// complete picture at a ref. One transaction, so a failed reconcile leaves the
// previous contents rather than a half-built graph.
func (db *DB) Put(ctx context.Context, gitRef string, entities []*duskv1alpha1.Entity, relations []*duskv1alpha1.Relation) error {
	if gitRef == "" {
		return errors.New("index: put: git ref is required")
	}

	entityRows, err := entityRows(gitRef, entities)
	if err != nil {
		return err
	}
	relationRows, err := relationRows(gitRef, relations)
	if err != nil {
		return err
	}

	return db.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := deleteGitRef(tx, gitRef); err != nil {
			return err
		}
		if len(entityRows) > 0 {
			if err := tx.CreateInBatches(entityRows, batchSize).Error; err != nil {
				return fmt.Errorf("index: put entities: %w", err)
			}
		}
		if len(relationRows) > 0 {
			if err := tx.CreateInBatches(relationRows, batchSize).Error; err != nil {
				return fmt.Errorf("index: put relations: %w", err)
			}
		}
		return nil
	})
}

// batchSize keeps a reconcile of a large repository under SQLite's limit on
// bound parameters per statement.
const batchSize = 200

// DropGitRef removes everything stored at gitRef, which is how a closed pull
// request's preview is garbage collected.
func (db *DB) DropGitRef(ctx context.Context, gitRef string) error {
	return db.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return deleteGitRef(tx, gitRef)
	})
}

func deleteGitRef(tx *gorm.DB, gitRef string) error {
	if err := tx.Where("git_ref = ?", gitRef).Delete(&relationRow{}).Error; err != nil {
		return fmt.Errorf("index: drop relations at %q: %w", gitRef, err)
	}
	if err := tx.Where("git_ref = ?", gitRef).Delete(&entityRow{}).Error; err != nil {
		return fmt.Errorf("index: drop entities at %q: %w", gitRef, err)
	}
	return nil
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

func entityRows(gitRef string, entities []*duskv1alpha1.Entity) ([]entityRow, error) {
	rows := make([]entityRow, 0, len(entities))
	for _, e := range entities {
		attributes, err := marshalStruct(e.GetAttributes())
		if err != nil {
			return nil, fmt.Errorf("index: entity %q: %w", e.GetRef(), err)
		}
		rows = append(rows, entityRow{
			GitRef:      gitRef,
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

func relationRows(gitRef string, relations []*duskv1alpha1.Relation) ([]relationRow, error) {
	rows := make([]relationRow, 0, len(relations))
	for _, r := range relations {
		attributes, err := marshalStruct(r.GetAttributes())
		if err != nil {
			return nil, fmt.Errorf("index: relation %q -> %q: %w", r.GetFrom(), r.GetTo(), err)
		}
		rows = append(rows, relationRow{
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
