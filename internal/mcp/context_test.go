package mcp_test

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
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

// ADR-0057: a section is charged what it wrote, not what it was granted. Notes
// too long to print whole are granted room for their bodies, print title lines,
// and used to leave everything below them nothing at all to spend.
func TestADR0057_UnspentAllowanceReachesTheNextSection(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)
	spread(t, idx)
	notes(t, idx, []*duskv1alpha1.Note{
		note("here-one", "gotcha", strings.Repeat("A long note about this repository. ", 200), true, "service:home/jellyfin"),
		note("here-two", "gotcha", strings.Repeat("Another long note about this repository. ", 200), true, "service:home/jellyfin"),
		note("estate", "gotcha", strings.Repeat("A long note about the whole estate. ", 200), true),
	})

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	t.Logf("the context spent %d of %d bytes", len(body), mcp.ContextBudget)
	if spent := len(body); spent < mcp.ContextBudget*3/4 {
		t.Errorf("the context spent %d of %d bytes, so a section that degraded to shorts kept what it could not use:\n%s",
			spent, mcp.ContextBudget, body)
	}
	if len(body) > mcp.ContextBudget {
		t.Errorf("the context is %d bytes, past the %d budget", len(body), mcp.ContextBudget)
	}
	if !strings.Contains(body, "service:home/jellyfin") {
		t.Errorf("the inventory was starved by notes that could not be printed whole:\n%s", body)
	}
}

// ADR-0057: infrastructure is what somebody maintains and reference is a fact
// about the world, so airports outranking services is exactly backwards.
func TestADR0057_InfrastructureOutranksReference(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)
	spread(t, idx)
	mint(t, idx, vocab.Kind{Namespace: vocab.Entity, Name: "airport", Role: vocab.Reference})

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	// airport carries more than service, so anything ranking on count alone
	// puts it first. Only the role it was minted with moves it.
	service := strings.Index(body, "**service**")
	airport := strings.Index(body, "**airport**")
	switch {
	case service < 0 || airport < 0:
		t.Fatalf("both kinds should be in the inventory:\n%s", body)
	case service > airport:
		t.Errorf("reference data outranked what the operator maintains:\n%s", body)
	}
}

// A count cannot say `service`. An inventory that elides the kind the catalog
// exists for, and says only how many it elided, has not named what it dropped.
func TestADR0057_TheOverflowNamesTheKindsItLeftOut(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)
	spread(t, idx)
	crowd(t, idx)

	var pinned []*duskv1alpha1.Note
	for i := range 20 {
		pinned = append(pinned, note(
			fmt.Sprintf("pinned-%02d", i), "gotcha",
			strings.Repeat(fmt.Sprintf("Pinned note %02d. ", i), 40), true, "service:home/jellyfin"))
	}
	notes(t, idx, pinned)

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	overflow := lineWith(t, body, "Kinds not listed:")
	if !strings.Contains(overflow, "datastore") {
		t.Errorf("the overflow counted what it left out instead of naming it: %q\n%s", overflow, body)
	}
}

// A ref reaching the index from two sources is one thing, so the inventory
// lists it once and the count above it agrees with the list below it.
func TestADR0057_ARefFromTwoSourcesIsListedOnce(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)
	alsoDeclares(t, idx, "example/mirror", "service:home/jellyfin")

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	listed := body[strings.Index(body, "## What this operator has"):]
	if count := strings.Count(listed, "service:home/jellyfin"); count != 1 {
		t.Errorf("a ref declared twice was listed %d times:\n%s", count, listed)
	}
	if !strings.Contains(body, "What this operator has (2)") {
		t.Errorf("the count included the same ref twice:\n%s", body)
	}
}

// An overflow line is meaningless without the thing it is overflowing from. A
// heading is reserved beside the line under it, so a section that wins no room
// at all still says which section the fragment belongs to.
func TestADR0057_AnOverflowLineCarriesItsHeading(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)
	spread(t, idx)
	notes(t, idx, []*duskv1alpha1.Note{
		note("here-one", "gotcha", strings.Repeat("A long note about this repository. ", 200), true, "service:home/jellyfin"),
		note("here-two", "gotcha", strings.Repeat("Another long note about this repository. ", 200), true, "service:home/jellyfin"),
		note("estate", "gotcha", strings.Repeat("A long note about the whole estate. ", 200), true),
	})

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	for _, block := range []struct{ heading, overflow string }{
		{"## Pinned, about this repository", "more pinned note(s) about this repository did not fit"},
		{"## What this repository declares", "more it declares are not listed"},
		{"## Pinned, across the estate", "more pinned note(s) did not fit"},
		{"## What this operator has", "Kinds not listed:"},
	} {
		heading := strings.Index(body, block.heading)
		if heading < 0 {
			t.Errorf("a section with entries printed no %q:\n%s", block.heading, body)
			continue
		}
		if fragment := strings.Index(body, block.overflow); fragment >= 0 && fragment < heading {
			t.Errorf("%q printed above its own %q:\n%s", block.overflow, block.heading, body)
		}
	}
}

// ADR-0057: an agent that never happens to `get` an entity carrying an action
// never learns the catalog can do anything, and nothing else says so.
func TestADR0057_TheContextSaysWhatCanBeDone(t *testing.T) {
	acting := acting(t, &offering{actions: []plugin.Action{{
		Plugin: "airtrail", Name: "delete_flight", Description: "Remove it from the logbook.",
		Class: plugin.ClassDestructive, Enabled: true, Kinds: []string{"service"},
	}}})
	seed(t, acting.index)

	body := call(t, acting.session, "dusk_context", map[string]any{"root": homelabRoot})

	for _, want := range []string{"carry actions", "`invoke`", "`service`"} {
		if !strings.Contains(body, want) {
			t.Errorf("the context never says %s is possible:\n%s", want, body)
		}
	}
}

// A read-only deployment does not register `invoke`, so naming it would send an
// agent at a tool that is not there.
func TestADR0057_NoPluginsMeansNoTalkOfActions(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	if strings.Contains(body, "invoke") {
		t.Errorf("a deployment with no plugins offered actions anyway:\n%s", body)
	}
}

// lineWith returns the line carrying marker, so a test asserts on the sentence
// the agent reads rather than on the whole answer.
func lineWith(t *testing.T, body, marker string) string {
	t.Helper()

	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	t.Fatalf("no line carrying %q:\n%s", marker, body)
	return ""
}

// mint declares a kind the way an operator does, which is the only way a kind
// carries a role other than the default.
func mint(t *testing.T, idx *index.DB, kinds ...vocab.Kind) {
	t.Helper()

	if err := idx.PutCatalog(t.Context(), "example/vocabulary", mainRef, nil, nil, nil, kinds); err != nil {
		t.Fatalf("PutCatalog: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), "example/vocabulary", mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}
}

// alsoDeclares gives a ref a second source, which is what a second repository
// declaring it, or an ingester observing it, looks like to the index.
func alsoDeclares(t *testing.T, idx *index.DB, repository, ref string) {
	t.Helper()

	kind, rest, _ := strings.Cut(ref, ":")
	namespace, name, _ := strings.Cut(rest, "/")
	declarations := []index.Declaration{{
		Path:   "dusk.md",
		Entity: &duskv1alpha1.Entity{Ref: ref, Kind: kind, Namespace: namespace, Name: name},
	}}

	if err := idx.Put(t.Context(), repository, mainRef, declarations, nil, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), repository, mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}
}

// spread fills the catalog with more kinds than the budget can name, so what
// the inventory gives up, and the order it gives it up in, are observable.
func spread(t *testing.T, idx *index.DB) {
	t.Helper()

	counts := map[string]int{
		"airport": 40, "board-card": 30, "container": 20, "dashboard": 15,
		"datastore": 6, "flight": 25, "host": 8, "service": 30,
		"vault-note": 12, "workflow": 10,
	}

	var declarations []index.Declaration
	for _, kind := range slices.Sorted(maps.Keys(counts)) {
		for i := range counts[kind] {
			name := fmt.Sprintf("%s-%03d", kind, i)
			declarations = append(declarations, index.Declaration{
				Path: "dusk.md",
				Entity: &duskv1alpha1.Entity{
					Ref: kind + ":estate/" + name, Kind: kind, Namespace: "estate", Name: name,
				},
			})
		}
	}

	if err := idx.Put(t.Context(), "example/estate", mainRef, declarations, nil, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), "example/estate", mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
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
