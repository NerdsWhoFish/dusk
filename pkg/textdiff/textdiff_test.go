package textdiff_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/pkg/textdiff"
)

func TestUnified(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		before []byte
		after  []byte
		want   string
	}{
		{
			name:   "identical content is no diff at all",
			path:   "dusk.md",
			before: []byte("a\nb\n"),
			after:  []byte("a\nb\n"),
			want:   "",
		},
		{
			name:   "a file that does not exist yet comes from /dev/null",
			path:   ".dusk/gotcha-1234.md",
			before: nil,
			after:  []byte("one\ntwo\n"),
			want: "--- /dev/null\n" +
				"+++ b/.dusk/gotcha-1234.md\n" +
				"@@ -0,0 +1,2 @@\n" +
				"+one\n" +
				"+two\n",
		},
		{
			name:   "one changed line carries the lines around it",
			path:   "services/jellyfin/dusk.md",
			before: []byte("a\nb\nc\n"),
			after:  []byte("a\nB\nc\n"),
			want: "--- a/services/jellyfin/dusk.md\n" +
				"+++ b/services/jellyfin/dusk.md\n" +
				"@@ -1,3 +1,3 @@\n" +
				" a\n" +
				"-b\n" +
				"+B\n" +
				" c\n",
		},
		{
			name:   "a removed line",
			path:   "dusk.md",
			before: []byte("a\nb\nc\n"),
			after:  []byte("a\nc\n"),
			want: "--- a/dusk.md\n" +
				"+++ b/dusk.md\n" +
				"@@ -1,3 +1,2 @@\n" +
				" a\n" +
				"-b\n" +
				" c\n",
		},
		{
			// git says this with a marker, and a diff that omits it applies the
			// wrong bytes.
			name:   "a last line with no newline is marked",
			path:   "dusk.md",
			before: []byte("a\nb"),
			after:  []byte("a\nb\n"),
			want: "--- a/dusk.md\n" +
				"+++ b/dusk.md\n" +
				"@@ -1,2 +1,2 @@\n" +
				" a\n" +
				"-b\n" +
				"\\ No newline at end of file\n" +
				"+b\n",
		},
		{
			name:   "an emptied file",
			path:   "dusk.md",
			before: []byte("a\n"),
			after:  nil,
			want: "--- a/dusk.md\n" +
				"+++ b/dusk.md\n" +
				"@@ -1,1 +0,0 @@\n" +
				"-a\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := textdiff.Unified(tt.path, tt.before, tt.after)
			if got != tt.want {
				t.Errorf("Unified() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// Context exists so a diff is readable, which means unchanged lines far from
// any change do not appear at all.
func TestDistantChangesAreSeparateHunks(t *testing.T) {
	before := numbered("l", 1, 10)
	after := strings.Replace(before, "l1\n", "L1\n", 1)
	after = strings.Replace(after, "l10\n", "L10\n", 1)

	got := textdiff.Unified("dusk.md", []byte(before), []byte(after))

	if hunks := strings.Count(got, "@@ -"); hunks != 2 {
		t.Errorf("hunks = %d, want 2:\n%s", hunks, got)
	}
	for _, want := range []string{"@@ -1,4 +1,4 @@", "@@ -7,4 +7,4 @@", "-l1\n", "+L1\n", "-l10\n", "+L10\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	// The middle of the file is untouched and has no business being in a diff.
	for _, unwanted := range []string{" l5\n", " l6\n"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("printed %q, which is nowhere near a change:\n%s", unwanted, got)
		}
	}
}

// A pathological pair still answers, because a diff nobody can read beats a
// write path that allocates without limit.
func TestALargeReplacementStillAnswers(t *testing.T) {
	before := numbered("before", 1, 3000)
	after := numbered("after", 1, 3000)

	got := textdiff.Unified("dusk.md", []byte(before), []byte(after))

	if !strings.Contains(got, "-before1\n") || !strings.Contains(got, "+after1\n") {
		t.Errorf("diff does not replace the file:\n%s", got[:min(len(got), 200)])
	}
}

func numbered(prefix string, from, to int) string {
	var out strings.Builder
	for i := from; i <= to; i++ {
		out.WriteString(prefix)
		out.WriteString(strconv.Itoa(i))
		out.WriteString("\n")
	}
	return out.String()
}
