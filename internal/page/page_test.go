package page_test

import (
	"strings"
	"testing"

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
	})

	if len(resolved) != 2 {
		t.Fatalf("resolved %d blocks, want both", len(resolved))
	}
	for _, block := range resolved {
		if block.Err == "" {
			t.Errorf("block %s hid its failure", block.Type)
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

func TestQuerySplitsKindFromWords(t *testing.T) {
	resolved := page.Resolve(t.Context(), &recording{}, page.Page{
		Blocks: []page.Block{{Type: page.TypeEntities, Query: "kind:service jellyfin"}},
	})
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
}
