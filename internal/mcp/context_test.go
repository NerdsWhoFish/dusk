package mcp_test

import (
	"fmt"
	"strings"
	"testing"

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/FetchHQ/dusk/internal/index"
	"github.com/FetchHQ/dusk/internal/mcp"
)

// ADR-0014: the injected content is an interaction manual and an inventory.
// An agent that has to ask three questions before it knows what exists will
// answer from a guess instead.
func TestContextIsAnInventory(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{})

	if !strings.Contains(body, "service:home/jellyfin") {
		t.Errorf("context did not name what the operator has:\n%s", body)
	}
	if !strings.Contains(body, "absence means nobody documented it") &&
		!strings.Contains(body, "nobody documented it") {
		t.Errorf("context did not explain what absence means:\n%s", body)
	}
}

// A repository nobody declared is the common case, and saying so plainly is
// more useful than an empty answer that reads like a failure.
func TestContextSaysWhenARepositoryIsUnknown(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{
		"root": "/Users/somebody/projects/src/github.com/example/unknown",
	})

	if !strings.Contains(body, "not in the catalog") {
		t.Errorf("an unknown repository was not reported as such:\n%s", body)
	}
	if !strings.Contains(body, "dusk.md") {
		t.Errorf("context did not say how to opt the repository in:\n%s", body)
	}
}

// An absolute path is what an agent actually has, so matching has to work on
// the trailing owner/name rather than requiring the slug.
func TestContextMatchesARepositoryFromItsPath(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{
		"root": "/Users/somebody/projects/src/github.com/example/homelab",
	})

	if strings.Contains(body, "not in the catalog") {
		t.Errorf("a known repository was not matched from its path:\n%s", body)
	}
	if !strings.Contains(body, "example/homelab") {
		t.Errorf("context did not name the repository it matched:\n%s", body)
	}
}

// Pinning is free to whoever pins and costs every future session, so the
// ceiling is enforced. A silently shortened context degrades every answer
// with nothing to connect the degradation to, hence the visible marker.
func TestContextTruncationIsVisible(t *testing.T) {
	idx := newIndex(t)

	// Comfortably past the budget, so the cut is certain.
	var declarations []index.Declaration
	for i := range 400 {
		ref := fmt.Sprintf("service:home/service-with-a-long-enough-name-%03d", i)
		declarations = append(declarations, index.Declaration{
			Path: "dusk.md",
			Entity: &duskv1alpha1.Entity{
				Ref: ref, Kind: "service", Namespace: "home", Name: ref,
			},
		})
	}
	if err := idx.Put(t.Context(), "example/big", "refs/heads/main", declarations, nil, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), "example/big", "refs/heads/main"); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{})

	if len(body) > mcp.ContextBudget+200 {
		t.Errorf("context is %d bytes, past the %d budget", len(body), mcp.ContextBudget)
	}
	if !strings.Contains(body, "Truncated") {
		t.Errorf("context was cut without saying so:\n%s", body[max(0, len(body)-300):])
	}
}
