package catalogfs_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/NerdsWhoFish/dusk/pkg/catalogfs"
)

const commit = "a866a20af5b14f34b7fe993e1b1fa0b6df0a46b7"

func treeOf(paths ...string) *catalogfs.Tree {
	files := map[string][]byte{}
	for _, p := range paths {
		files[p] = []byte("x")
	}
	return catalogfs.New(commit, files)
}

// `**` is what a documentation tree needs, and path.Match has no such thing.
// Every reader shares this one matcher, so a gap here is a gap everywhere.
func TestGlob(t *testing.T) {
	tree := treeOf(
		"dusk.md",
		"entities/dusk.md",
		"entities/net/switch.md",
		"docs/a.md",
		"docs/deep/b.md",
		"docs/deeper/still/c.md",
		"elsewhere/d.md",
		".dusk/gotcha.md",
		".dusk/notes/nested.md",
	)

	tests := []struct {
		pattern string
		want    []string
	}{
		// The case that shipped broken: `**` must match no directories too, or
		// `entities/**/*.md` silently skips everything directly under it.
		{"entities/**/*.md", []string{"entities/dusk.md", "entities/net/switch.md"}},
		{"entities/*.md", []string{"entities/dusk.md"}},
		{"docs/**/*.md", []string{"docs/a.md", "docs/deep/b.md", "docs/deeper/still/c.md"}},
		{"docs/*.md", []string{"docs/a.md"}},
		{".dusk/**/*.md", []string{".dusk/gotcha.md", ".dusk/notes/nested.md"}},
		{"*.md", []string{"dusk.md"}},
		{"nothing/*.md", nil},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got, err := tree.Glob(tt.pattern)
			if err != nil {
				t.Fatalf("Glob: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Glob(%q) = %v, want %v", tt.pattern, got, tt.want)
			}
		})
	}

	t.Run("a malformed pattern is an error, not a silent miss", func(t *testing.T) {
		if _, err := tree.Glob("[unclosed"); err == nil {
			t.Error("a malformed pattern matched nothing instead of failing")
		}
	})
}

// A pattern that reads a file has to be able to name one, or an entity can be
// declared into a repository only by hand. The two rules lived in two packages
// and disagreed about `**`, which is what this pins.
func TestPlace(t *testing.T) {
	tests := []struct {
		pattern string
		want    string
	}{
		{"services/*/dusk.md", "services/navidrome/dusk.md"},
		{"entities/*.md", "entities/navidrome.md"},
		{"entities/**/*.md", "entities/navidrome.md"},
		{"**/*.md", "navidrome.md"},
		{"*.md", "navidrome.md"},
		{".dusk/**/*.md", ".dusk/navidrome.md"},

		// The name is the only thing a `**` can be filled with here.
		{"**/dusk.md", "navidrome/dusk.md"},

		// Nothing to fill in, so it names one file rather than a destination.
		{"entities/nas.md", ""},
		{"[unclosed/*.md", ""},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got, ok := catalogfs.Place(tt.pattern, "navidrome")
			if got != tt.want || ok != (tt.want != "") {
				t.Fatalf("Place(%q) = %q, %v, want %q", tt.pattern, got, ok, tt.want)
			}
			if !ok {
				return
			}
			if matched, err := catalogfs.Match(tt.pattern, got); err != nil || !matched {
				t.Errorf("Place put a file at %q that %q does not match", got, tt.pattern)
			}
		})
	}
}

// The name reaches this from a ref an agent supplied, so it is the same trust
// level as a request body.
func TestPlaceRefusesANameThatIsNotOne(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", "../escape", "*"} {
		if got, ok := catalogfs.Place("entities/*.md", name); ok {
			t.Errorf("Place(%q) = %q, want it refused", name, got)
		}
	}
}

func TestReadReportsAMissAsNotExisting(t *testing.T) {
	tree := treeOf("dusk.md")

	if _, err := tree.Read("dusk.md"); err != nil {
		t.Errorf("Read: %v", err)
	}

	// The reconciler tells "has not opted in" from "could not look" by this
	// error alone, so it has to satisfy fs.ErrNotExist.
	_, err := tree.Read("absent.md")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read = %v, want fs.ErrNotExist", err)
	}
}

func TestPathsAreSorted(t *testing.T) {
	tree := treeOf("z.md", "a.md", "m/n.md")
	if got := tree.Paths(); !slices.Equal(got, []string{"a.md", "m/n.md", "z.md"}) {
		t.Errorf("Paths = %v, want lexical order", got)
	}
}

func TestIsCatalogFile(t *testing.T) {
	tests := map[string]bool{
		"dusk.md":               true,
		"DUSK.MD":               true,
		"a/b/c.Md":              true,
		"main.go":               false,
		"README":                false,
		"notes.md.bak":          false,
		".git/objects/ab/cd.md": false,
		".git":                  false,
		// Not git's directory, just a name that starts the same way.
		".gitignore-notes/a.md": true,
	}
	for name, want := range tests {
		if got := catalogfs.IsCatalogFile(name); got != want {
			t.Errorf("IsCatalogFile(%q) = %v, want %v", name, got, want)
		}
	}
}
