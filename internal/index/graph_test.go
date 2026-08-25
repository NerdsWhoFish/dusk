package index_test

import (
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

func TestGraphJoinsRelationsAndKnowledgeToVisibleEntities(t *testing.T) {
	db := newDB(t)
	service := entity("service:home/jellyfin", "Jellyfin", "")
	host := entity("host:home/nas", "NAS", "")
	note := &duskv1alpha1.Note{
		Id: ".dusk/gotcha-storage.md", Kind: "gotcha", Body: "Keep the volume mounted.",
		Refs: []string{service.GetRef()}, ContentHash: "note-hash", Provenance: testProvenance(),
	}
	relations := []*duskv1alpha1.Relation{
		relation(service.GetRef(), host.GetRef(), "runs_on"),
		relation(service.GetRef(), "host:home/undeclared", "depends_on"),
	}
	if err := db.Put(t.Context(), "example/homelab", mainRef, declare([]*duskv1alpha1.Entity{service, host}), relations, []*duskv1alpha1.Note{note}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), "example/homelab", mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	graph, err := db.Graph(t.Context(), "", index.Unrestricted())
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(graph.Nodes))
	}
	if len(graph.Relations) != 1 || graph.Relations[0].GetType() != "runs_on" {
		t.Fatalf("relations = %+v, want only the edge whose ends exist", graph.Relations)
	}

	for _, node := range graph.Nodes {
		if node.Entity.GetRef() != service.GetRef() {
			continue
		}
		if len(node.Notes) != 1 || node.Notes[0].GetId() != note.GetId() {
			t.Fatalf("service notes = %+v, want the attached gotcha", node.Notes)
		}
		return
	}
	t.Fatal("the service node is missing")
}
