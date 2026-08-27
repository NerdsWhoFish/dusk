package insights_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/NerdsWhoFish/dusk/internal/events"
	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/insights"
)

func TestSnapshotExplainsCurrentCatalogAndBoundedActions(t *testing.T) {
	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	provenance := &duskv1alpha1.Provenance{
		Source: "dusk.md", Version: "version", ObservedAt: timestamppb.Now(),
	}
	entity := func(ref, kind, name string) *duskv1alpha1.Entity {
		return &duskv1alpha1.Entity{
			Ref: ref, Kind: kind, Namespace: "home", Name: name, Provenance: provenance,
		}
	}
	notes := []*duskv1alpha1.Note{
		{Id: ".dusk/gotcha-wide.md", Kind: "gotcha", Body: "Wide knowledge", Refs: []string{"service:home/api", "host:home/one", "host:home/two"}, ContentHash: "one", Provenance: provenance},
		{Id: ".dusk/todo-open.md", Kind: "todo", Body: "Open work", Refs: []string{"service:home/api"}, ContentHash: "two", Provenance: provenance},
		{Id: ".dusk/idea-done.md", Kind: "idea", Body: "Finished work", Status: "done", ContentHash: "three", Provenance: provenance},
	}
	for i := range 9 {
		notes = append(notes, &duskv1alpha1.Note{
			Id: fmt.Sprintf(".dusk/runbook-%02d.md", i), Kind: "runbook", Body: "Routine knowledge",
			ContentHash: fmt.Sprintf("runbook-%02d", i), Provenance: provenance,
		})
	}
	if err := db.Put(t.Context(), "example/platform", "refs/heads/main", []index.Declaration{
		{Entity: entity("service:home/api", "service", "api")},
		{Entity: entity("host:home/one", "host", "one")},
	}, nil, notes); err != nil {
		t.Fatalf("put platform: %v", err)
	}
	if err := db.Put(t.Context(), "example/homelab", "refs/heads/main", []index.Declaration{
		{Entity: entity("host:home/two", "host", "two")},
	}, nil, nil); err != nil {
		t.Fatalf("put homelab: %v", err)
	}
	for _, repository := range []string{"example/platform", "example/homelab"} {
		if err := db.SetDefaultView(t.Context(), repository, "refs/heads/main"); err != nil {
			t.Fatalf("set default for %s: %v", repository, err)
		}
	}

	actions := &events.Log{}
	for _, event := range []*duskv1alpha1.Event{
		finished("one", "kubernetes", duskv1alpha1.EventStatus_EVENT_STATUS_SUCCEEDED),
		finished("two", "kubernetes", duskv1alpha1.EventStatus_EVENT_STATUS_FAILED),
		finished("three", "home-assistant", duskv1alpha1.EventStatus_EVENT_STATUS_SUCCEEDED),
	} {
		if err := actions.Emit(event); err != nil {
			t.Fatalf("emit action: %v", err)
		}
	}

	snapshot, err := (&insights.Service{Catalog: db, Actions: actions}).Read(t.Context())
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if snapshot.Entities != 3 || snapshot.Repositories != 2 || snapshot.Notes != 12 || snapshot.OpenWork != 1 {
		t.Fatalf("totals = %+v", snapshot)
	}
	if len(snapshot.Sources) != 2 || snapshot.Sources[0].Repository != "example/platform" || snapshot.Sources[0].Entities != 2 {
		t.Fatalf("sources = %+v", snapshot.Sources)
	}
	if len(snapshot.Knowledge) != 5 || snapshot.Knowledge[0].ID != ".dusk/gotcha-wide.md" || snapshot.Knowledge[0].Links != 3 {
		t.Fatalf("knowledge = %+v", snapshot.Knowledge)
	}
	if snapshot.Actions != 3 || len(snapshot.Plugins) != 2 {
		t.Fatalf("plugins = %+v, actions = %d", snapshot.Plugins, snapshot.Actions)
	}
	if got := snapshot.Plugins[0]; got.Plugin != "kubernetes" || got.Actions != 2 || got.Succeeded != 1 || got.Problems != 1 {
		t.Fatalf("top plugin = %+v", got)
	}
}

func finished(id, plugin string, status duskv1alpha1.EventStatus) *duskv1alpha1.Event {
	at := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	return events.Finish(events.Started(id, "", plugin, "", "run", "agent", at), status, "", nil, at)
}
