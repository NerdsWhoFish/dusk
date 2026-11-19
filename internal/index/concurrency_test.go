package index

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCatalogAndEmbeddingWritesWaitForActiveTransaction(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	sqlDB, err := db.gorm.DB()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "INSERT INTO default_views (repository, git_ref) VALUES (?, ?)", "example/held", "main"); err != nil {
		t.Fatal(err)
	}

	results := make(chan error, 2)
	go func() {
		results <- db.PutCatalog(ctx, "example/catalog", "main", []Declaration{{
			Path: "dusk.md", Entity: searchableEntity("service:home/test", "Test", ""),
		}}, nil, nil, nil, nil)
	}()
	go func() {
		semantic := &semanticIndex{db: db, embedder: meaningEmbedder{}, model: "test"}
		_, err := semantic.embedBatch(ctx, []embeddingDocument{{
			Repository: "example/catalog", GitRef: "main", KindOf: "entity",
			ID: "service:home/test", ContentHash: "test", Text: "Test",
		}})
		results <- err
	}()

	// Outlast the driver's five-second busy timeout, as a large reconcile can.
	// Competing writes must queue in the pool instead of failing in SQLite.
	select {
	case err := <-results:
		t.Fatalf("write returned before the active transaction finished: %v", err)
	case <-time.After(6 * time.Second):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("queued write failed: %v", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if _, err := db.Get(ctx, "main", "service:home/test"); err != nil {
		t.Errorf("catalog write missing: %v", err)
	}
	var row embeddingRow
	if err := db.gorm.WithContext(ctx).First(&row, "id = ?", "service:home/test").Error; err != nil {
		t.Errorf("embedding write missing: %v", err)
	}
}
