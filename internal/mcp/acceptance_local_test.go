package mcp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
)

// Drives dusk_context over the operator's real notes rather than fixtures,
// because the defect it covers was invisible to every fixture in this package.
// Skipped without DUSK_ACCEPTANCE_CATALOG pointing at a catalog checkout.
func TestAcceptanceRealCatalogContext(t *testing.T) {
	root := os.Getenv("DUSK_ACCEPTANCE_CATALOG")
	if root == "" {
		t.Skip("set DUSK_ACCEPTANCE_CATALOG to a catalog checkout")
	}

	paths, err := filepath.Glob(filepath.Join(root, ".dusk", "*.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no notes under %s/.dusk: %v", root, err)
	}

	var written []*duskv1alpha1.Note
	var pinned []string
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil || !duskmd.IsNote(data) {
			continue
		}
		parsed, err := duskmd.ParseNote(".dusk/"+filepath.Base(path), data,
			duskmd.Provenance{Version: "acceptance"})
		if err != nil {
			t.Fatalf("ParseNote(%s): %v", path, err)
		}
		written = append(written, parsed)
		if parsed.GetPinned() {
			pinned = append(pinned, parsed.GetId())
		}
	}

	idx := newIndex(t)
	seed(t, idx)
	notes(t, idx, written)
	t.Logf("%d notes, %d pinned: %v", len(written), len(pinned), pinned)

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "acceptance"}))
	result, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "dusk_context", Arguments: map[string]any{"root": homelabRoot},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	structured, _ := result.StructuredContent.(map[string]any)
	data, _ := structured["data"].(map[string]any)
	rendered, _ := data["context"].(string)
	t.Logf("structured context is %d bytes:\n%s", len(rendered), rendered)

	for _, id := range pinned {
		if !strings.Contains(rendered, id) {
			t.Errorf("pinned note %s reaches no client reading structured content", id)
		}
	}
}
