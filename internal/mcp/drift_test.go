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
	body := call(t, session, "drift", map[string]any{"undeclared": true})

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

// A row is a thing to look into, not a declaration to delete, and the fix for
// the commonest cause has to be named where it is read. An agent acting on the
// old copy would have proposed removing the catalog (ADR-0056).
func TestADR0056_DriftSaysWhatARowCanMeanAndHowToFixIt(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	observed := index.ObservedScope("kubernetes")
	err := idx.Put(t.Context(), observed, "refs/heads/main", []index.Declaration{
		{Path: "observed", Entity: entity("service:home/other", "service", "Other", "")},
	}, nil, nil)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), observed, "refs/heads/main"); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "drift", map[string]any{})

	if !strings.Contains(body, "service:home/jellyfin") {
		t.Fatalf("a declaration inside the watched namespace was not reported:\n%s", body)
	}
	if strings.Contains(body, "host:home/nas") {
		t.Errorf("a declaration of a kind nothing observes was reported:\n%s", body)
	}
	if strings.Contains(body, "declarations should be removed") {
		t.Errorf("drift still tells the reader to delete what it cannot see:\n%s", body)
	}
	if !strings.Contains(body, "observed_as") {
		t.Errorf("drift did not name the alias that clears a naming mismatch:\n%s", body)
	}
}
