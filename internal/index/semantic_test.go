package index

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type meaningEmbedder struct {
	err error
}

func (e meaningEmbedder) Embed(_ context.Context, input []string) ([][]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	vectors := make([][]float32, len(input))
	for i, text := range input {
		text = strings.ToLower(text)
		switch {
		case strings.Contains(text, "trace"), strings.Contains(text, "tracing"), strings.Contains(text, "opentelemetry"), strings.Contains(text, "grafana"):
			vectors[i] = []float32{1, 0}
		default:
			vectors[i] = []float32{0, 1}
		}
	}
	return vectors, nil
}

type batchingEmbedder struct {
	batches [][]string
}

func (e *batchingEmbedder) Embed(_ context.Context, input []string) ([][]float32, error) {
	e.batches = append(e.batches, append([]string(nil), input...))
	vectors := make([][]float32, len(input))
	for i, text := range input {
		text = strings.ToLower(text)
		if strings.Contains(text, "distributed spans") || strings.Contains(text, "tracing") {
			vectors[i] = []float32{1, 0}
		} else {
			vectors[i] = []float32{0, 1}
		}
	}
	return vectors, nil
}

func semanticDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir() + "/index.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func searchableEntity(ref, title, description string) *duskv1alpha1.Entity {
	return &duskv1alpha1.Entity{
		Ref: ref, Kind: "service", Namespace: "home",
		Name: strings.TrimPrefix(ref, "service:home/"), Title: title, Description: description,
		Provenance: &duskv1alpha1.Provenance{
			Source: "declared", Version: "v1", ObservedAt: timestamppb.Now(),
		},
	}
}

func putSearchable(t *testing.T, db *DB, hash, description string, aliases ...string) {
	t.Helper()
	declaration := Declaration{
		Path: "grafana/dusk.md", ContentHash: hash,
		Entity:     searchableEntity("service:home/grafana-cloud", "Grafana Cloud", description),
		ObservedAs: aliases,
	}
	if err := db.Put(t.Context(), "example/estate", "refs/heads/main", []Declaration{declaration}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDefaultView(t.Context(), "example/estate", "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
}

// ADR-0083 makes vectors derived data whose content hash must match the current
// catalog row. A stale semantic hit would be worse than temporarily relying on
// FTS while the write-triggered refresh runs.
func TestADR0083_StaleEmbeddingsAreExcludedUntilRefreshed(t *testing.T) {
	db := semanticDB(t)
	putSearchable(t, db, "hash-1", "OpenTelemetry data is sent here.")
	db.semantic = &semanticIndex{
		db: db, embedder: meaningEmbedder{}, model: "test", repair: time.Hour,
		log: slog.New(slog.NewTextHandler(io.Discard, nil)), cache: make(map[string][]float32),
	}
	if _, err := db.semantic.rebuild(t.Context()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	results, _, err := db.Search(t.Context(), "", SearchFilter{Query: "tracing", Limit: 10})
	if err != nil || len(results) == 0 || results[0].Ref != "service:home/grafana-cloud" || results[0].MatchedBy != "semantic" {
		t.Fatalf("semantic Search = %+v, %v", results, err)
	}

	putSearchable(t, db, "hash-2", "Telemetry backend.")
	results, _, err = db.Search(t.Context(), "", SearchFilter{Query: "tracing", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("stale semantic Search = %+v, want no stale hit", results)
	}

	if _, err := db.semantic.rebuild(t.Context()); err != nil {
		t.Fatalf("repair: %v", err)
	}
	results, _, err = db.Search(t.Context(), "", SearchFilter{Query: "tracing", Limit: 10})
	if err != nil || len(results) == 0 || results[0].Ref != "service:home/grafana-cloud" {
		t.Fatalf("repaired Search = %+v, %v", results, err)
	}
}

func TestEmbeddingBackfillChunksDocumentsAndBoundsProviderBatches(t *testing.T) {
	db := semanticDB(t)
	description := strings.Repeat("ordinary metrics backend content ", 80) + "distributed spans live here"
	putSearchable(t, db, "hash-1", description)
	embedder := &batchingEmbedder{}
	db.semantic = &semanticIndex{
		db: db, embedder: embedder, model: "test", repair: time.Hour,
		log: slog.New(slog.NewTextHandler(io.Discard, nil)), cache: make(map[string][]float32),
	}
	if _, err := db.semantic.rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, batch := range embedder.batches {
		if len(batch) > embeddingProviderBatch {
			t.Fatalf("provider batch size = %d, want at most %d", len(batch), embeddingProviderBatch)
		}
		for _, input := range batch {
			if len(input) > embeddingChunkBytes {
				t.Fatalf("provider input length = %d, want at most %d", len(input), embeddingChunkBytes)
			}
		}
	}
	var row embeddingRow
	if err := db.gorm.Where("id = ?", "service:home/grafana-cloud").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if len(row.Vector) <= row.Dimensions*4 {
		t.Fatalf("stored vector bytes = %d, want multiple %d-dimension chunks", len(row.Vector), row.Dimensions)
	}

	results, _, err := db.Search(t.Context(), "", SearchFilter{Query: "tracing", Limit: 10})
	if err != nil || len(results) == 0 || results[0].Ref != "service:home/grafana-cloud" {
		t.Fatalf("semantic Search = %+v, %v", results, err)
	}
}

func TestSearchFindsAnExactEntityAliasWithoutEmbeddings(t *testing.T) {
	db := semanticDB(t)
	putSearchable(t, db, "hash-1", "Telemetry backend.", "observability")

	results, total, err := db.Search(t.Context(), "", SearchFilter{Query: "observability", Limit: 10})
	if err != nil || total != 1 || len(results) != 1 {
		t.Fatalf("Search = %+v (%d), %v", results, total, err)
	}
	if results[0].Ref != "service:home/grafana-cloud" || results[0].MatchedBy != "exact" {
		t.Fatalf("result = %+v", results[0])
	}
}

func TestSearchVisibilityAppliesBeforeRankingAndToNotes(t *testing.T) {
	db := semanticDB(t)
	for _, repository := range []string{"example/visible", "example/hidden"} {
		note := &duskv1alpha1.Note{
			Id:   ".dusk/" + strings.TrimPrefix(repository, "example/") + ".md",
			Kind: "reference", Body: "shared search phrase in " + repository,
			ContentHash: "hash-" + repository,
			Provenance: &duskv1alpha1.Provenance{
				Source: "declared", Version: "v1", ObservedAt: timestamppb.Now(),
			},
		}
		if err := db.Put(t.Context(), repository, "refs/heads/main", nil, nil, []*duskv1alpha1.Note{note}); err != nil {
			t.Fatal(err)
		}
		if err := db.SetDefaultView(t.Context(), repository, "refs/heads/main"); err != nil {
			t.Fatal(err)
		}
	}

	results, total, err := db.Search(t.Context(), "", SearchFilter{
		Query: "shared search", Limit: 10,
		Visibility: Visibility{Repositories: []string{"example/visible"}},
	})
	if err != nil || total != 1 || len(results) != 1 {
		t.Fatalf("Search = %+v (%d), %v", results, total, err)
	}
	if results[0].Ref != ".dusk/visible.md" {
		t.Fatalf("visible result = %+v", results[0])
	}
}

func TestEmbeddingRepairRemovesRowsFromAnOldModel(t *testing.T) {
	db := semanticDB(t)
	old := embeddingRow{
		Repository: "example/estate", GitRef: "refs/heads/main",
		KindOf: "entity", ID: "service:home/old", Model: "old-model",
		ContentHash: "hash", Dimensions: 1, Vector: encodeVector([]float32{1}),
	}
	if err := db.gorm.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	db.semantic = &semanticIndex{
		db: db, embedder: meaningEmbedder{}, model: "new-model", repair: time.Hour,
		log: slog.New(slog.NewTextHandler(io.Discard, nil)), cache: make(map[string][]float32),
	}
	if _, err := db.semantic.rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.gorm.Model(&embeddingRow{}).Where("model = ?", "old-model").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("old model rows = %d, want 0", count)
	}
}
