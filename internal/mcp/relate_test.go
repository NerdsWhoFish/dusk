package mcp_test

import (
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

func TestRelateCarriesTheEdgeThrough(t *testing.T) {
	session, writer := writableSession(t)

	token := tokenFrom(t, call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"}))
	body := call(t, session, "relate", map[string]any{
		"from":  "service:home/jellyfin",
		"to":    "host:home/nas",
		"type":  "runs_on",
		"proof": token,
	})

	if len(writer.relations) != 1 {
		t.Fatalf("the writer saw %d relations, want 1", len(writer.relations))
	}
	got := writer.relations[0]
	if got.From != "service:home/jellyfin" || got.To != "host:home/nas" || got.Type != "runs_on" {
		t.Errorf("relation = %+v, want all three passed through", got)
	}
	if !strings.Contains(body, "commit/c0ffee") {
		t.Errorf("answer carries no link to the commit:\n%s", body)
	}
}

// The read of the entity an edge points from is the one that can witness what
// it already declares, so that is the token relate takes.
func TestRelateTakesTheTokenFromAReadOfWhatItPointsFrom(t *testing.T) {
	for _, tt := range []struct {
		name string
		read string
		args map[string]any
		want bool
	}{
		{"a get on the source", "get", map[string]any{"ref": "service:home/jellyfin"}, true},
		{"a walk from the source", "neighbors", map[string]any{"ref": "service:home/jellyfin"}, true},
		{"a search that returned it", "search", map[string]any{"query": "jellyfin"}, true},
		{"a get on the target", "get", map[string]any{"ref": "host:home/nas"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			session, writer := writableSession(t)

			body := call(t, session, "relate", map[string]any{
				"from": "service:home/jellyfin", "to": "host:home/nas", "type": "runs_on",
				"proof": tokenFrom(t, call(t, session, tt.read, tt.args)),
			})

			if accepted := len(writer.relations) == 1; accepted != tt.want {
				t.Fatalf("accepted = %v, want %v:\n%s", accepted, tt.want, body)
			}
			if !tt.want && !strings.Contains(body, `get("service:home/jellyfin")`) {
				t.Errorf("the refusal does not name the read that fixes it:\n%s", body)
			}
		})
	}
}

// ADR-0061: the read names the write it authorizes, and a token covering an
// entity now buys an edge out of it as well as a change to it.
func TestADR0061_AReadOffersItsTokenToRelate(t *testing.T) {
	session, _ := notingSession(t, configRepo)

	for _, tt := range []struct {
		tool string
		args map[string]any
	}{
		{"search", map[string]any{"query": "jellyfin"}},
		{"get", map[string]any{"ref": "service:home/jellyfin"}},
		{"neighbors", map[string]any{"ref": "host:home/nas"}},
	} {
		t.Run(tt.tool, func(t *testing.T) {
			if body := call(t, session, tt.tool, tt.args); !strings.Contains(body, "`relate`") {
				t.Errorf("%s does not offer its token to relate:\n%s", tt.tool, body)
			}
		})
	}

	// An empty search covers nothing, so there is no entity to relate out of.
	t.Run("a search that found nothing", func(t *testing.T) {
		body := call(t, session, "search", map[string]any{"query": "nothinglikethis"})
		if strings.Contains(body, "`relate`") {
			t.Errorf("an empty search offered a token that covers no entity:\n%s", body)
		}
	})
}

func TestRelateWithoutATokenIsRefusedAndExplained(t *testing.T) {
	session, writer := writableSession(t)

	body := call(t, session, "relate", map[string]any{
		"from": "service:home/jellyfin", "to": "host:home/nas",
		"type": "runs_on", "proof": "made-up",
	})

	if len(writer.relations) != 0 {
		t.Errorf("a write happened without a token: %+v", writer.relations)
	}
	for _, want := range []string{"was not declared", proof.CodeRequired, `get("service:home/jellyfin")`} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal missing %q:\n%s", want, body)
		}
	}
}

// ADR-0033: a target nothing declares is written, and said. Refusing it would
// break incremental adoption; saying nothing would hide a typo.
func TestADR0033_RelateWritesAnUnknownTargetAndNamesIt(t *testing.T) {
	session, writer := writableSession(t)

	token := tokenFrom(t, call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"}))
	body := call(t, session, "relate", map[string]any{
		"from": "service:home/jellyfin", "to": "host:elsewhere/box",
		"type": "runs_on", "proof": token,
	})

	if len(writer.relations) != 1 {
		t.Fatalf("an edge to an undeclared target was refused:\n%s", body)
	}
	if !strings.Contains(body, "Nothing in the catalog declares `host:elsewhere/box`") {
		t.Errorf("the answer does not say the target is not in the catalog:\n%s", body)
	}
	if !strings.Contains(body, "not an error") {
		t.Errorf("the answer reads as a failure rather than as information:\n%s", body)
	}

	t.Run("and says nothing when the target is declared", func(t *testing.T) {
		session, _ := writableSession(t)
		token := tokenFrom(t, call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"}))

		body := call(t, session, "relate", map[string]any{
			"from": "service:home/jellyfin", "to": "host:home/nas",
			"type": "runs_on", "proof": token,
		})
		if strings.Contains(body, "Nothing in the catalog declares") {
			t.Errorf("a declared target was reported as missing:\n%s", body)
		}
	})
}

// A read-only deployment is a supported posture, so the tool is absent rather
// than present and always refusing (ADR-0005).
func TestRelateIsAbsentWhenReadOnly(t *testing.T) {
	session := serve(t, mcp.New(mcp.Options{Catalog: newIndex(t), Version: "test"}))

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "relate" {
			t.Fatal("relate is offered by a read-only deployment")
		}
	}

	if _, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "relate", Arguments: map[string]any{"from": "a", "to": "b", "type": "c", "proof": "d"},
	}); err == nil {
		t.Error("calling relate succeeded on a read-only deployment")
	}
}
