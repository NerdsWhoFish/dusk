package mcp_test

import (
	"fmt"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/mcp"
)

const homelabRoot = "/Users/somebody/projects/src/github.com/example/homelab"

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
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	if strings.Contains(body, "not in the catalog") {
		t.Errorf("a known repository was not matched from its path:\n%s", body)
	}
	if !strings.Contains(body, "example/homelab") {
		t.Errorf("context did not name the repository it matched:\n%s", body)
	}
	// Provenance records the file a declaration came from and never the
	// repository, so this half read as empty for every repository until
	// Declared existed.
	if !strings.Contains(body, "What this repository declares (2)") {
		t.Errorf("context did not say what the repository declares:\n%s", body)
	}
}

// ADR-0050: the shortest path from "the operator wrote down a gotcha" to "the
// agent knew it before it started". A pinned note about the repository being
// worked in arrives whole, without being asked for.
func TestADR0050_PinnedNotesReachTheContext(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)
	notes(t, idx, []*duskv1alpha1.Note{
		note("about-here", "gotcha", "Transcoding is off on purpose.", true, "service:home/jellyfin"),
		note("about-elsewhere", "gotcha", "The registry runs out of disk quarterly.", true),
		note("unpinned", "todo", "Somebody should tidy the media library.", false, "service:home/jellyfin"),
	})

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	for _, want := range []string{
		"Pinned, about this repository",
		"Transcoding is off on purpose.",
		"Pinned, across the estate",
		"The registry runs out of disk quarterly.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("context is missing %q:\n%s", want, body)
		}
	}

	// Pinning is the operator saying a note is worth every future session.
	// Widening past it is note ranking by kind, which is a separate decision.
	if strings.Contains(body, "Somebody should tidy") {
		t.Errorf("an unpinned note spent the budget:\n%s", body)
	}
}

// A note about a repository other than the one being worked in is not local to
// it, however loudly it is pinned. Relevance is what orders the pinned set.
func TestADR0050_RelevanceOrdersThePinnedSet(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)
	notes(t, idx, []*duskv1alpha1.Note{
		note("estate", "gotcha", "Nothing decrypts a sealed secret on the way in.", true),
		note("local", "gotcha", "The namespace still carries the old name.", true, "host:home/nas"),
	})

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	here := strings.Index(body, "The namespace still carries the old name.")
	elsewhere := strings.Index(body, "Nothing decrypts a sealed secret on the way in.")
	switch {
	case here < 0 || elsewhere < 0:
		t.Fatalf("both pinned notes should be present:\n%s", body)
	case here > elsewhere:
		t.Errorf("a note about this repository was ordered below one about the estate:\n%s", body)
	}
}

// A silently shortened context degrades every answer with nothing to connect
// the degradation to, so a section that cannot fit its items says how many it
// left out rather than ending early.
func TestADR0050_NothingIsDroppedSilently(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)
	crowd(t, idx)

	var pinned []*duskv1alpha1.Note
	for i := range 60 {
		pinned = append(pinned, note(
			fmt.Sprintf("pinned-%02d", i), "gotcha",
			fmt.Sprintf("Pinned note %02d, whose body is long enough that sixty of them cannot all fit inside the budget however it is spent.", i),
			true, "service:home/jellyfin"))
	}
	notes(t, idx, pinned)

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	if len(body) > mcp.ContextBudget {
		t.Errorf("context is %d bytes, past the %d budget", len(body), mcp.ContextBudget)
	}

	named := strings.Count(body, "`.dusk/pinned-")
	missing := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "more pinned note(s) about this repository did not fit") {
			if _, err := fmt.Sscanf(line, "%d more pinned note", &missing); err != nil {
				t.Fatalf("could not read what was left out of %q: %v", line, err)
			}
		}
	}
	if named+missing != len(pinned) {
		t.Errorf("%d notes named and %d reported left out, want all %d accounted for:\n%s",
			named, missing, len(pinned), body)
	}

	// A share cap is why pinning forty things does not erase the inventory,
	// which is the other half of what ADR-0014 promises.
	for _, want := range []string{"What this operator has", "not listed", "absence means nobody documented it"} {
		if !strings.Contains(body, want) {
			t.Errorf("an over-budget context did not say %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Truncated") {
		t.Errorf("allocation should keep the answer inside the budget, not truncate:\n%s", body)
	}
}

// A pinned note beats a ref that would otherwise be listed: the ref is one
// `search` away and the gotcha is reachable by nothing, because nobody asks for
// what they were never told exists.
func TestADR0050_WrittenKnowledgeOutranksTheInventory(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)
	crowd(t, idx)
	notes(t, idx, []*duskv1alpha1.Note{
		note("survivor", "gotcha", "The source repository is gone, so this cannot be rebuilt.", true, "service:home/jellyfin"),
	})

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	if !strings.Contains(body, "The source repository is gone, so this cannot be rebuilt.") {
		t.Errorf("a pinned note lost the budget to an inventory:\n%s", body)
	}
	if !strings.Contains(body, "names not listed") {
		t.Errorf("the inventory should give up its names first:\n%s", body)
	}
}

// crowd fills the catalog past anything the budget can hold, so what the
// allocation gives up is observable rather than theoretical.
func crowd(t *testing.T, idx *index.DB) {
	t.Helper()

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
	if err := idx.Put(t.Context(), "example/big", mainRef, declarations, nil, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), "example/big", mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}
}

// notes stores notes in a config repository of their own, which is where
// ADR-0031 puts anything Dusk writes.
func notes(t *testing.T, idx *index.DB, written []*duskv1alpha1.Note) {
	t.Helper()

	if err := idx.Put(t.Context(), "example/config", mainRef, nil, nil, written); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), "example/config", mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}
}

func note(id, kind, body string, pinned bool, refs ...string) *duskv1alpha1.Note {
	return &duskv1alpha1.Note{
		Id: ".dusk/" + id + ".md", Kind: kind, Body: body, Pinned: pinned, Refs: refs,
		ContentHash: "hash-" + id,
		Provenance:  &duskv1alpha1.Provenance{Source: "dusk.md"},
	}
}
