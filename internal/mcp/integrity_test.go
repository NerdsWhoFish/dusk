package mcp_test

import (
	"strings"
	"testing"

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/FetchHQ/dusk/internal/mcp"
)

// An agent that has to know to ask whether an answer is trustworthy will not
// ask, so soundness rides along with freshness rather than being its own tool.
func TestChangesReportsWhatIsWrongWithTheCatalog(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	// A relation to something nobody declares, which every other read serves
	// without comment.
	err := idx.Put(t.Context(), "example/homelab", "refs/heads/main",
		nil,
		[]*duskv1alpha1.Relation{{
			From: "service:home/jellyfin", To: "host:home/typo", Type: "runs_on",
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "changes", map[string]any{})

	if !strings.Contains(body, "host:home/typo") {
		t.Errorf("changes did not report the dangling relation:\n%s", body)
	}
	if !strings.Contains(body, "problem") {
		t.Errorf("changes did not label it as a problem:\n%s", body)
	}
}

// A sound catalog has to say so out loud. Silence would be indistinguishable
// from a check that did not run.
func TestChangesSaysWhenTheCatalogIsSound(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))
	body := call(t, session, "changes", map[string]any{})

	if strings.Contains(body, "problem") {
		t.Errorf("a sound catalog reported a problem:\n%s", body)
	}
	if !strings.Contains(body, "points at something that exists") {
		t.Errorf("a sound catalog did not say so:\n%s", body)
	}
}
