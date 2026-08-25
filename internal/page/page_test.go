package page_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/page"
)

func TestParseADeclaredPage(t *testing.T) {
	declared := []byte(`---
title: Home
blocks:
  - type: entities
    title: Everything on prod
    query: kind:service prod
    limit: 10
  - type: drift
  - type: recent-notes
    limit: 5
    wide: true
---

What this operator runs.
`)

	parsed, prose, err := page.Parse(declared)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Title != "Home" {
		t.Errorf("title = %q", parsed.Title)
	}
	if len(parsed.Blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(parsed.Blocks))
	}
	if parsed.Blocks[0].Query != "kind:service prod" {
		t.Errorf("query = %q", parsed.Blocks[0].Query)
	}
	if !parsed.Blocks[2].Wide {
		t.Error("wide was not read")
	}
	if prose != "What this operator runs." {
		t.Errorf("prose = %q", prose)
	}
}

// A page is authored by hand, so a mistake has to name itself. Silently
// dropping an unknown block would leave somebody staring at a missing panel.
func TestParseRefusesWhatItCannotRender(t *testing.T) {
	tests := map[string]string{
		"an unknown block type": "---\ntitle: Home\nblocks:\n  - type: horoscope\n---\n",
		"an unknown field":      "---\ntitle: Home\nblocks:\n  - type: drift\n    colour: red\n---\n",
		"no blocks at all":      "---\ntitle: Home\nblocks: []\n---\n",
		"no frontmatter":        "# Home\n\nJust prose.\n",
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := page.Parse([]byte(body)); err == nil {
				t.Error("Parse accepted it")
			}
		})
	}
}

// ADR-0013: a block is a typed query and a bad one renders empty rather than
// breaking the page. The reason travels with the block so it is not a mystery.
func TestABadBlockDoesNotBreakThePage(t *testing.T) {
	resolved := page.Resolve(t.Context(), failing{}, page.Page{
		Blocks: []page.Block{
			{Type: page.TypeKinds},
			{Type: page.TypeDrift},
		},
	}, index.Unrestricted())

	if len(resolved) != 2 {
		t.Fatalf("resolved %d blocks, want both", len(resolved))
	}
	for _, block := range resolved {
		if block.Err == "" {
			t.Errorf("block %s hid its failure", block.Type)
		}
	}
}

// ADR-0051: a block whose answer is a count or a comparison cannot be filtered
// after the fact, so the viewer has to reach the query. One that never arrives
// is a page block answering about the whole estate.
func TestADR0051_EveryAggregateBlockIsResolvedForTheViewer(t *testing.T) {
	recorded.asked = nil
	t.Cleanup(func() { recorded.asked = nil })

	want := index.Visibility{Repositories: []string{"example/public"}}
	aggregates := []page.Type{page.TypeKinds, page.TypeDrift, page.TypeIntegrity}

	blocks := make([]page.Block, 0, len(aggregates))
	for _, blockType := range aggregates {
		blocks = append(blocks, page.Block{Type: blockType})
	}
	page.Resolve(t.Context(), &recording{}, page.Page{Blocks: blocks}, want)

	for _, blockType := range aggregates {
		got, ok := recorded.asked[blockType]
		if !ok {
			t.Errorf("the %s block never reached the catalog", blockType)
			continue
		}
		if !slices.Equal(got.Repositories, want.Repositories) || got.Observed != want.Observed {
			t.Errorf("the %s block resolved for %+v, want %+v", blockType, got, want)
		}
	}
}

func TestDefaultPageNeedsNoDeclaration(t *testing.T) {
	if len(page.Default().Blocks) == 0 {
		t.Fatal("the default page is empty, so a catalog only looks presentable after homework")
	}
	for _, block := range page.Default().Blocks {
		if block.Type == "" {
			t.Error("a default block has no type")
		}
	}
}

func TestReadsBlockUsesOneAggregateInsteadOfLoadingEveryEntityPerScope(t *testing.T) {
	recorded.scopeCounts = []index.ScopeCount{
		{Repository: "example/homelab", GitRef: "refs/heads/main", Entities: 93},
		{Repository: index.ObservedScope("kubernetes"), GitRef: "refs/dusk/observed", Entities: 1979},
	}
	recorded.listCalls = 0
	t.Cleanup(func() {
		recorded.scopeCounts = nil
		recorded.listCalls = 0
	})

	resolved := page.Resolve(t.Context(), &recording{}, page.Page{
		Blocks: []page.Block{{Type: page.TypeReads}},
	}, index.Unrestricted())
	if resolved[0].Err != "" {
		t.Fatalf("resolve reads: %s", resolved[0].Err)
	}
	if recorded.listCalls != 0 {
		t.Fatalf("reads loaded all entities %d time(s), want one aggregate query", recorded.listCalls)
	}
	if len(resolved[0].Reads) != 2 || resolved[0].Reads[1].Entities != 1979 {
		t.Fatalf("reads = %+v, want the counts returned by the index", resolved[0].Reads)
	}
}

func TestQuerySplitsKindFromWords(t *testing.T) {
	resolved := page.Resolve(t.Context(), &recording{}, page.Page{
		Blocks: []page.Block{{Type: page.TypeEntities, Query: "kind:service jellyfin"}},
	}, index.Unrestricted())
	if resolved[0].Err != "" {
		t.Fatalf("resolve: %s", resolved[0].Err)
	}

	// A query with words goes through search, not list.
	if !strings.Contains(recorded.searched, "jellyfin") {
		t.Errorf("search saw %q, want the words without the kind filter", recorded.searched)
	}
	if strings.Contains(recorded.searched, "kind:") {
		t.Errorf("search saw %q, want kind: stripped out", recorded.searched)
	}

	// ADR-0059: the kind goes to the query. Kept back and applied to the page a
	// limit already cut, a block renders empty whenever that page holds none.
	if recorded.searchedKind != "service" {
		t.Errorf("search saw kind %q, want it narrowing the query", recorded.searchedKind)
	}
}
