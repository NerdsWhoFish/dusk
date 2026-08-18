package mcp_test

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

// attributed declares one entity carrying attributes of every kind a `dusk.md`
// can hold, because the defect this covers is per kind rather than per entity.
func attributed(t *testing.T, idx *index.DB, attributes map[string]any) {
	t.Helper()

	fields, err := structpb.NewStruct(attributes)
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}

	subject := entity("router:example/box", "router", "The Router", "It routes.")
	subject.Attributes = fields
	put(t, idx, "example/homelab", []*duskv1alpha1.Entity{subject})
}

// An attribute is arbitrary JSON, and rendering a structpb value with %s put
// `%!s(float64=125)` on the page for every number an operator declared. An
// answer an agent cannot read back is worse than a missing one.
func TestAnAttributeIsRenderedByItsKind(t *testing.T) {
	session, idx := connect(t, nil)
	attributed(t, idx, map[string]any{
		"port":       125,
		"ratio":      1.5,
		"managed":    true,
		"tier":       "gold",
		"retired":    nil,
		"statuses":   []any{"Backlog", "To Do", "In Progress"},
		"thresholds": map[string]any{"cpu": "500m"},
	})

	body := call(t, session, "get", map[string]any{"ref": "router:example/box"})

	if strings.Contains(body, "%!") {
		t.Errorf("an attribute rendered as a Go formatting error:\n%s", body)
	}

	for _, want := range []string{
		"- **port**: 125",
		"- **ratio**: 1.5",
		"- **managed**: true",
		"- **tier**: gold",
		"- **retired**: none",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("get did not render %q:\n%s", want, body)
		}
	}

	// A list of multi-word strings printed as `[Backlog To Do In Progress]`
	// cannot be told from a list of five one-word strings, which is a different
	// answer rather than an uglier one.
	if !strings.Contains(body, `- **statuses**: ["Backlog","To Do","In Progress"]`) {
		t.Errorf("a list of multi-word strings is ambiguous:\n%s", body)
	}
	if !strings.Contains(body, `- **thresholds**: {"cpu":"500m"}`) {
		t.Errorf("a map attribute did not render as something readable:\n%s", body)
	}
	if strings.Contains(body, "map[") {
		t.Errorf("an attribute was rendered with Go formatting:\n%s", body)
	}
}

// Provenance carries a commit for a repository and a git ref for an
// observation. Taking the first seven characters is right for the first and
// turns the second into `refs/du`, which names nothing.
func TestAVersionIsAbbreviatedOnlyWhenItIsASha(t *testing.T) {
	for _, tt := range []struct {
		name    string
		source  string
		version string
		want    string
	}{
		{
			name:    "a commit is abbreviated",
			source:  "dusk.md",
			version: "abc1234def5678901234",
			want:    "from `abc1234`.",
		},
		{
			name:    "a git ref is left whole",
			source:  "ingester:plugin:example",
			version: "refs/dusk/observed",
			want:    "from `refs/dusk/observed`.",
		},
		{
			name:    "a branch is left whole",
			source:  "dusk.md",
			version: "refs/heads/main",
			want:    "from `refs/heads/main`.",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			session, idx := connect(t, nil)
			put(t, idx, "example/homelab", []*duskv1alpha1.Entity{{
				Ref: "router:example/box", Kind: "router", Namespace: "example", Name: "box",
				Title: "The Router",
				Provenance: &duskv1alpha1.Provenance{
					Source: tt.source, Version: tt.version, ObservedAt: timestamppb.Now(),
				},
			}})

			body := call(t, session, "get", map[string]any{"ref": "router:example/box"})
			if !strings.Contains(body, tt.want) {
				t.Errorf("provenance does not read %q:\n%s", tt.want, body)
			}
		})
	}
}

func TestObservedOnlyEntitySaysItHasNoDeclaration(t *testing.T) {
	session, idx := connect(t, nil)
	put(t, idx, index.ObservedScope("plugin:example"), []*duskv1alpha1.Entity{entity(
		"service:cluster/surprise", "service", "Surprise", "Observed at runtime.",
	)})

	body := call(t, session, "get", map[string]any{"ref": "service:cluster/surprise"})
	for _, want := range []string{"Observed by", "No repository declares this entity", "observed only"} {
		if !strings.Contains(body, want) {
			t.Errorf("observed-only provenance does not say %q:\n%s", want, body)
		}
	}
}
