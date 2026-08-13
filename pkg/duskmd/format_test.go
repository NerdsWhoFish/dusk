package duskmd_test

import (
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
)

// A write path that rewrote somebody's prose would be worse than no write path,
// so rendering has to be a fixed point: what parses out renders back the same.
func TestRoundTrip(t *testing.T) {
	rendered, err := duskmd.FormatRoot(mustParseRoot(t, validRoot))
	if err != nil {
		t.Fatalf("FormatRoot: %v", err)
	}

	first := mustParseRoot(t, validRoot)
	second := mustParseRoot(t, string(rendered))

	for _, field := range []struct{ name, got, want string }{
		{"ref", second.Entity.GetRef(), first.Entity.GetRef()},
		{"kind", second.Entity.GetKind(), first.Entity.GetKind()},
		{"namespace", second.Entity.GetNamespace(), first.Entity.GetNamespace()},
		{"name", second.Entity.GetName(), first.Entity.GetName()},
		{"title", second.Entity.GetTitle(), first.Entity.GetTitle()},
		{"description", second.Entity.GetDescription(), first.Entity.GetDescription()},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}

	if len(second.Relations) != len(first.Relations) {
		t.Fatalf("relations = %d, want %d", len(second.Relations), len(first.Relations))
	}
	for i, want := range first.Relations {
		got := second.Relations[i]
		if got.GetType() != want.GetType() || got.GetTo() != want.GetTo() || got.GetFrom() != want.GetFrom() {
			t.Errorf("relation %d = %s %s -> %s, want %s %s -> %s",
				i, got.GetFrom(), got.GetType(), got.GetTo(),
				want.GetFrom(), want.GetType(), want.GetTo())
		}
	}

	if len(second.Include) != len(first.Include) {
		t.Errorf("include = %v, want %v", second.Include, first.Include)
	}
	if got, want := second.Entity.GetAttributes().GetFields()["tier"].GetStringValue(), "1"; got != want {
		t.Errorf("attributes.tier = %q, want %q", got, want)
	}

	t.Run("rendering twice changes nothing", func(t *testing.T) {
		again, err := duskmd.FormatRoot(second)
		if err != nil {
			t.Fatalf("FormatRoot: %v", err)
		}
		if string(again) != string(rendered) {
			t.Errorf("second render differs:\n--- first ---\n%s\n--- second ---\n%s", rendered, again)
		}
	})
}

// The description is markdown somebody wrote. Rendering it through anything
// would quietly rewrite their file.
func TestProseSurvivesVerbatim(t *testing.T) {
	const prose = `Media server.

## Gotchas

**Transcoding is off.** Anything that will not direct play is a client problem.

    an indented block
    that must survive

A [link](https://example.com) and a | pipe | that YAML would mangle.`

	source := "---\ndusk: v1alpha1\nnamespace: home\nkind: service\nname: jellyfin\n---\n\n" + prose + "\n"

	rendered, err := duskmd.FormatRoot(mustParseRoot(t, source))
	if err != nil {
		t.Fatalf("FormatRoot: %v", err)
	}
	if got := mustParseRoot(t, string(rendered)).Entity.GetDescription(); got != prose {
		t.Errorf("prose changed:\n--- want ---\n%s\n--- got ---\n%s", prose, got)
	}
}

func TestFormatOmitsWhatTheParserWouldDefault(t *testing.T) {
	t.Run("a title equal to the name is not written", func(t *testing.T) {
		source := "---\ndusk: v1alpha1\nnamespace: home\nkind: service\nname: jellyfin\n---\n\nProse.\n"
		rendered, err := duskmd.FormatRoot(mustParseRoot(t, source))
		if err != nil {
			t.Fatalf("FormatRoot: %v", err)
		}
		if strings.Contains(string(rendered), "title:") {
			t.Errorf("wrote a title that only repeats the name:\n%s", rendered)
		}
	})

	t.Run("an inherited namespace is not repeated", func(t *testing.T) {
		source := "---\ndusk: v1alpha1\nkind: service\nname: jellyfin\n---\n\nProse.\n"
		file, err := duskmd.ParseIncluded("services/jellyfin/dusk.md", []byte(source), "home", testProvenance)
		if err != nil {
			t.Fatalf("ParseIncluded: %v", err)
		}

		rendered, err := duskmd.FormatIncluded(file, "home")
		if err != nil {
			t.Fatalf("FormatIncluded: %v", err)
		}
		if strings.Contains(string(rendered), "namespace:") {
			t.Errorf("repeated the inherited namespace:\n%s", rendered)
		}

		// It still has to parse back to the same entity, inheriting again.
		again, err := duskmd.ParseIncluded("services/jellyfin/dusk.md", rendered, "home", testProvenance)
		if err != nil {
			t.Fatalf("re-parse: %v", err)
		}
		if got, want := again.Entity.GetRef(), "service:home/jellyfin"; got != want {
			t.Errorf("ref = %q, want %q", got, want)
		}
	})

	t.Run("an overridden namespace is written", func(t *testing.T) {
		source := "---\ndusk: v1alpha1\nnamespace: billing\nkind: service\nname: payments\n---\n\nProse.\n"
		file, err := duskmd.ParseIncluded("x/dusk.md", []byte(source), "home", testProvenance)
		if err != nil {
			t.Fatalf("ParseIncluded: %v", err)
		}
		rendered, err := duskmd.FormatIncluded(file, "home")
		if err != nil {
			t.Fatalf("FormatIncluded: %v", err)
		}
		if !strings.Contains(string(rendered), "namespace: billing") {
			t.Errorf("dropped an override that changes the ref:\n%s", rendered)
		}
	})
}

// Whatever is rendered has to survive the parser's strictness, or the write
// path produces files the read path rejects.
func TestRenderedFilesParse(t *testing.T) {
	rendered, err := duskmd.FormatRoot(mustParseRoot(t, validRoot))
	if err != nil {
		t.Fatalf("FormatRoot: %v", err)
	}
	if _, err := duskmd.ParseRoot("dusk.md", rendered, testProvenance); err != nil {
		t.Fatalf("a rendered file did not parse:\n%s\n%v", rendered, err)
	}
}

func mustParseRoot(t *testing.T, source string) *duskmd.File {
	t.Helper()
	file, err := duskmd.ParseRoot("dusk.md", []byte(source), testProvenance)
	if err != nil {
		t.Fatalf("ParseRoot: %v", err)
	}
	return file
}
