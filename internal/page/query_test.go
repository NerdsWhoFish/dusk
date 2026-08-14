package page_test

import (
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/page"
)

func flight(name, date string) *duskv1alpha1.Entity {
	attributes, _ := structpb.NewStruct(map[string]any{"date": date})
	return &duskv1alpha1.Entity{
		Ref: "flight:airtrail/" + name, Kind: "flight", Name: name, Attributes: attributes,
	}
}

// "The latest three" is sorting then cutting. Cutting first would give three
// arbitrary flights put in order, which looks right and is not.
func TestEntitiesSortsBeforeCutting(t *testing.T) {
	recorded.listed = []*duskv1alpha1.Entity{
		flight("b", "2025-01-02"),
		flight("c", "2025-08-11"),
		flight("a", "2024-03-04"),
	}
	t.Cleanup(func() { recorded.listed = nil })

	resolved := page.Resolve(t.Context(), &recording{}, page.Page{
		Blocks: []page.Block{{
			Type: page.TypeEntities, Query: "kind:flight", Sort: "-date", Limit: 2,
		}},
	}, index.Unrestricted())

	got := resolved[0].Entities
	if len(got) != 2 {
		t.Fatalf("kept %d entities, want the limit", len(got))
	}
	if got[0].GetName() != "c" || got[1].GetName() != "b" {
		t.Errorf("order = %s then %s, want the two most recent newest first",
			got[0].GetName(), got[1].GetName())
	}
}

// A relation query is what makes a block a graph question. Direction is
// ignored, so one block covers arrivals and departures.
func TestEntitiesFiltersByRelationInEitherDirection(t *testing.T) {
	recorded.listed = []*duskv1alpha1.Entity{
		flight("through", "2025-01-01"),
		flight("elsewhere", "2025-01-02"),
		flight("inbound", "2025-01-03"),
	}
	recorded.relations = []*duskv1alpha1.Relation{
		{From: "flight:airtrail/through", To: "airport:airtrail/atl", Type: "departs_from"},
		{From: "airport:airtrail/atl", To: "flight:airtrail/inbound", Type: "serves"},
	}
	t.Cleanup(func() { recorded.listed, recorded.relations = nil, nil })

	resolved := page.Resolve(t.Context(), &recording{}, page.Page{
		Blocks: []page.Block{{
			Type: page.TypeEntities, Query: "kind:flight related:airport:airtrail/atl",
		}},
	}, index.Unrestricted())

	got := resolved[0].Entities
	if len(got) != 2 {
		t.Fatalf("kept %d entities, want both ends of the airport: %+v", len(got), got)
	}
	if recorded.neighborsOf != "airport:airtrail/atl" {
		t.Errorf("asked for neighbors of %q", recorded.neighborsOf)
	}
}

// A view block names a plugin, and the server fills in the rest. Naming none
// is the one thing worth refusing before it reaches a renderer.
func TestViewBlockNeedsAPlugin(t *testing.T) {
	resolved := page.Resolve(t.Context(), &recording{}, page.Page{
		Blocks: []page.Block{{Type: page.TypeView}},
	}, index.Unrestricted())
	if resolved[0].Err == "" {
		t.Error("a view block naming no plugin was accepted")
	}
}

// Search is the primary action, so a page that says nothing about it keeps it.
func TestSearchIsOnUnlessRemoved(t *testing.T) {
	if !(page.Page{}).Searchable() {
		t.Error("a page that says nothing about search lost it")
	}

	off := false
	if (page.Page{Search: &off}).Searchable() {
		t.Error("a page that removed search still has it")
	}
}
