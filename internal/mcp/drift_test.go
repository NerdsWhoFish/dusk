package mcp_test

import (
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/mcp"
)

// Drift is the question a wiki cannot answer about itself, and the two halves
// are different work: one is cleanup, the other is documentation.
func TestDriftSeparatesGoneFromUndeclared(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	observed := index.ObservedScope("kubernetes")
	err := idx.Put(t.Context(), observed, "refs/heads/main", []index.Declaration{
		{Path: "observed", Entity: &duskv1alpha1.Entity{
			Ref: "service:home/jellyfin", Kind: "service", Namespace: "home", Name: "jellyfin",
		}},
		{Path: "observed", Entity: &duskv1alpha1.Entity{
			Ref: "service:home/surprise", Kind: "service", Namespace: "home", Name: "surprise",
			Title: "Surprise",
		}},
	}, nil, nil)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), observed, "refs/heads/main"); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "drift", map[string]any{})

	if !strings.Contains(body, "service:home/surprise") {
		t.Errorf("drift did not report the undeclared service:\n%s", body)
	}
	if !strings.Contains(body, "undeclared") {
		t.Errorf("drift did not label it:\n%s", body)
	}

	// An entity that is both declared and observed is agreement.
	surprise := strings.Index(body, "service:home/surprise")
	jellyfin := strings.Index(body, "service:home/jellyfin")
	if jellyfin >= 0 && jellyfin < surprise {
		t.Errorf("an entity that is both declared and observed was reported:\n%s", body)
	}
}

// With nothing observed there is nothing to compare against, and the answer
// has to say so rather than reading as "all clear".
func TestDriftSaysWhenThereIsNothingToCompare(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "drift", map[string]any{})

	if !strings.Contains(body, "no ingester") {
		t.Errorf("drift with no ingesters did not explain itself:\n%s", body)
	}
}
