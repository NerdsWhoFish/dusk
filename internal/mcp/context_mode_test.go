package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/internal/mcp"
)

func TestRepositoryRefreshKeepsLocalWarningsWithoutReplayingStartup(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)
	put(t, idx, "example/elsewhere", []*duskv1alpha1.Entity{
		entity("host:other/unrelated", "host", "Unrelated host", "Unrelated estate data."),
	})
	policy := "Mandatory global tracing and privacy requirements."
	profile := []byte("---\ndusk: context/v1\nbudget: 8000\nfull_note_kinds: []\n---\n" + policy)
	if err := idx.PutCatalog(t.Context(), "example/config", mainRef, nil, nil, []*duskv1alpha1.Note{
		note("global", "reference", "# Global obligations\nGlobal details.", true),
		note("local", "gotcha", "# Local restore warning\nKeep the database offline.", true, "host:home/nas"),
		note("elsewhere", "gotcha", "# Unrelated warning\nOther details.", true, "host:other/unrelated"),
	}, nil, profile); err != nil {
		t.Fatal(err)
	}
	if err := idx.SetDefaultView(t.Context(), "example/config", mainRef); err != nil {
		t.Fatal(err)
	}
	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Writer: &recordingWriter{notesGo: "example/config"}}))
	for _, mode := range []string{"startup", "repository", "startup"} {
		result, err := session.CallTool(t.Context(), &sdk.CallToolParams{
			Name: "dusk_context", Arguments: map[string]any{"root": homelabRoot, "mode": mode},
		})
		if err != nil || result.IsError {
			t.Fatalf("%s: %v, %#v", mode, err, result)
		}
		for _, half := range []any{result.Content, result.StructuredContent} {
			encoded, err := json.Marshal(half)
			if err != nil {
				t.Fatal(err)
			}
			body := string(encoded)
			for _, required := range []string{"host:home/nas", "service:home/jellyfin", ".dusk/local.md", "Local restore warning"} {
				if !strings.Contains(body, required) {
					t.Fatalf("%s lost %q", mode, required)
				}
			}
			for _, global := range []string{policy, ".dusk/global.md", ".dusk/elsewhere.md", "host:other/unrelated", "Working with this catalog", "What this operator has"} {
				if strings.Contains(body, global) != (mode == "startup") {
					t.Fatalf("%s has incorrect inclusion of %q", mode, global)
				}
			}
		}
	}
}

func TestRepositoryRefreshUnknownRepositoryDoesNotFallBackToEstate(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)
	notes(t, idx, []*duskv1alpha1.Note{note("global", "reference", "Global obligations.", true)})
	session := serve(t, mcp.New(mcp.Options{Catalog: idx}))
	body := call(t, session, "dusk_context", map[string]any{"root": "example/unknown", "mode": "repository"})
	if !strings.Contains(body, "not in the catalog") || !strings.Contains(body, "Repository refresh only") {
		t.Fatal("missing scoped absence and refresh guidance")
	}
	for _, forbidden := range []string{"host:home/nas", "Global obligations", "The catalog is empty", "Global Notes"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("unknown repository response included %q", forbidden)
		}
	}
}

func TestContextRefreshRejectsInvalidModeOrRepository(t *testing.T) {
	idx := newIndex(t)
	session := serve(t, mcp.New(mcp.Options{Catalog: idx}))
	for _, args := range []map[string]any{
		{"mode": "surprise"},
		{"mode": "repository"},
		{"mode": "repository", "root": "   "},
		{"mode": "repository", "root": "/tmp/example/homelab"},
		{"mode": "repository", "root": "example/"},
		{"mode": "repository", "root": "../homelab"},
		{"mode": "repository", "root": "https://github.com/example/homelab"},
	} {
		result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: "dusk_context", Arguments: args})
		if err == nil && !result.IsError {
			t.Fatalf("invalid refresh accepted: %#v", args)
		}
	}
}
