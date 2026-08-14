// Package textdiff renders the difference between two versions of one file as
// a unified diff.
//
// It exists because a change Dusk may not commit still has to come back as
// something a person can act on, and a unified diff is the one form every
// editor, reviewer and git apply already understands.
//
// It is textual and file-scoped. The semantic difference between two versions
// of the catalog is a different question and belongs to internal/index.
package textdiff

import (
	"fmt"
	"strings"
)

// Context is how many unchanged lines surround each change, matching what git
// emits by default.
const Context = 3

// maxCells bounds the table the line pairing allocates. Past it the diff
// degrades to replacing the block wholesale, which is a worse diff rather than
// an unbounded allocation.
const maxCells = 1 << 20

// Unified returns the diff turning before into after for the file at path,
// with the a/ and b/ prefixes git apply expects. A nil before means the file
// does not exist yet and renders from /dev/null; identical contents return "".
func Unified(path string, before, after []byte) string {
	if before != nil && string(before) == string(after) {
		return ""
	}

	sections := hunks(lines(before), lines(after))
	if len(sections) == 0 {
		return ""
	}

	from := "a/" + path
	if before == nil {
		from = "/dev/null"
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ b/%s\n", from, path)
	for _, section := range sections {
		section.write(&out)
	}
	return out.String()
}

// lines splits a file after each newline, keeping the terminator on the line it
// belongs to. Carrying it makes an unterminated last line a different line from
// a terminated one, which is what the marker in a unified diff exists to say.
func lines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	split := strings.SplitAfter(string(data), "\n")
	if last := len(split) - 1; split[last] == "" {
		split = split[:last]
	}
	return split
}

// edit is one line of the diff: kept, removed, or added.
type edit struct {
	kind byte
	text string
}

// hunk is one run of changes with its surrounding context, and where that run
// sits in each version.
type hunk struct {
	aStart, aCount int
	bStart, bCount int
	edits          []edit
}

func (h hunk) write(out *strings.Builder) {
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", h.aStart, h.aCount, h.bStart, h.bCount)
	for _, e := range h.edits {
		out.WriteByte(e.kind)
		out.WriteString(strings.TrimSuffix(e.text, "\n"))
		out.WriteByte('\n')
		if !strings.HasSuffix(e.text, "\n") {
			out.WriteString("\\ No newline at end of file\n")
		}
	}
}

// hunks turns a whole edit script into the sections worth printing, which is
// every change plus Context lines around it.
func hunks(a, b []string) []hunk {
	edits := diff(a, b)
	shown := mark(edits)

	// Where each edit sits in each version, so a hunk header is a lookup rather
	// than a second pass counting lines.
	aAt := make([]int, len(edits)+1)
	bAt := make([]int, len(edits)+1)
	aAt[0], bAt[0] = 1, 1
	for i, e := range edits {
		aAt[i+1], bAt[i+1] = aAt[i], bAt[i]
		if e.kind != '+' {
			aAt[i+1]++
		}
		if e.kind != '-' {
			bAt[i+1]++
		}
	}

	var sections []hunk
	for start := 0; start < len(edits); {
		if !shown[start] {
			start++
			continue
		}
		end := start
		for end < len(edits) && shown[end] {
			end++
		}
		sections = append(sections, section(edits[start:end], aAt[start], bAt[start]))
		start = end
	}
	return sections
}

// section builds one hunk from the edits it covers.
func section(edits []edit, aStart, bStart int) hunk {
	h := hunk{aStart: aStart, bStart: bStart, edits: edits}
	for _, e := range edits {
		if e.kind != '+' {
			h.aCount++
		}
		if e.kind != '-' {
			h.bCount++
		}
	}

	// A side contributing nothing starts at the line before, which is how a
	// unified diff writes a file that did not exist.
	if h.aCount == 0 {
		h.aStart--
	}
	if h.bCount == 0 {
		h.bStart--
	}
	return h
}

// mark reports which edits are printed: every change, and Context lines either
// side of one. Everything else is the part of the file nobody needs to see.
func mark(edits []edit) []bool {
	shown := make([]bool, len(edits))
	for i, e := range edits {
		if e.kind == ' ' {
			continue
		}
		for k := max(0, i-Context); k <= min(len(edits)-1, i+Context); k++ {
			shown[k] = true
		}
	}
	return shown
}

// diff is the whole edit script from a to b. The common ends are trimmed first,
// because the expensive pairing is only worth running on what actually differs.
func diff(a, b []string) []edit {
	prefix := common(a, b)
	suffix := commonEnd(a[prefix:], b[prefix:])

	edits := make([]edit, 0, len(a)+len(b))
	for _, line := range a[:prefix] {
		edits = append(edits, edit{' ', line})
	}
	edits = append(edits, pair(a[prefix:len(a)-suffix], b[prefix:len(b)-suffix])...)
	for _, line := range a[len(a)-suffix:] {
		edits = append(edits, edit{' ', line})
	}
	return edits
}

// pair diffs what is left once the common ends are gone, matching lines through
// a longest common subsequence.
func pair(a, b []string) []edit {
	if len(a) == 0 || len(b) == 0 || len(a)*len(b) > maxCells {
		return replace(a, b)
	}

	// table[i][j] is the length of the longest common subsequence of a[i:] and
	// b[j:], which lets the walk below go forwards.
	table := make([][]int, len(a)+1)
	for i := range table {
		table[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
				continue
			}
			table[i][j] = max(table[i+1][j], table[i][j+1])
		}
	}

	edits := make([]edit, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			edits = append(edits, edit{' ', a[i]})
			i++
			j++
		// Removals before additions where the pairing is a tie, so a replaced
		// block reads as one deleted run and one added run.
		case table[i+1][j] >= table[i][j+1]:
			edits = append(edits, edit{'-', a[i]})
			i++
		default:
			edits = append(edits, edit{'+', b[j]})
			j++
		}
	}
	return append(edits, replace(a[i:], b[j:])...)
}

// replace removes everything on one side and adds everything on the other.
func replace(a, b []string) []edit {
	edits := make([]edit, 0, len(a)+len(b))
	for _, line := range a {
		edits = append(edits, edit{'-', line})
	}
	for _, line := range b {
		edits = append(edits, edit{'+', line})
	}
	return edits
}

func common(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func commonEnd(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}
