package controller

import (
	"fmt"
	"strings"

	"github.com/FetchHQ/dusk/internal/index"
)

// commentLimit caps how many changes are listed. A review comment longer than
// the diff it describes is one nobody reads.
const commentLimit = 25

// renderComment describes what merging would do to the catalog, which is what
// a reviewer cannot get from the file diff: an entity appearing or vanishing
// is not obvious from a few lines of YAML moving around.
func renderComment(changes []index.Change, previewURL string) string {
	if len(changes) == 0 {
		return "**Dusk**: this changes nothing in the catalog.\n\n" +
			"The files differ, but what the catalog would say after merge is identical.\n"
	}

	var added, removed, modified []index.Change
	for _, change := range changes {
		switch change.Kind {
		case index.ChangeAdded:
			added = append(added, change)
		case index.ChangeRemoved:
			removed = append(removed, change)
		default:
			modified = append(modified, change)
		}
	}

	var out strings.Builder
	out.WriteString("**Dusk**: " + summarise(added, removed, modified) + "\n\n")

	section(&out, "Added", added, func(c index.Change) string {
		return fmt.Sprintf("`%s` — %s", c.Ref, c.After)
	})
	section(&out, "Removed", removed, func(c index.Change) string {
		return fmt.Sprintf("`%s` — %s", c.Ref, c.Before)
	})
	section(&out, "Changed", modified, func(c index.Change) string {
		return fmt.Sprintf("`%s` %s: %s → %s", c.Ref, c.Field, quote(c.Before), quote(c.After))
	})

	if previewURL != "" {
		fmt.Fprintf(&out, "\n[Browse the catalog as it would be after merge](%s)\n", previewURL)
	}
	return out.String()
}

func summarise(added, removed, modified []index.Change) string {
	var parts []string
	if len(added) > 0 {
		parts = append(parts, fmt.Sprintf("%d added", len(added)))
	}
	if len(removed) > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", len(removed)))
	}
	if len(modified) > 0 {
		parts = append(parts, fmt.Sprintf("%d changed", len(modified)))
	}

	// Removal is the one worth flagging: a merged pull request that quietly
	// deletes an entity takes everything anybody knew about it.
	if len(removed) > 0 {
		return strings.Join(parts, ", ") + ". **Something would stop existing.**"
	}
	return strings.Join(parts, ", ") + "."
}

func section(out *strings.Builder, title string, changes []index.Change, line func(index.Change) string) {
	if len(changes) == 0 {
		return
	}

	fmt.Fprintf(out, "### %s\n\n", title)
	for i, change := range changes {
		if i == commentLimit {
			fmt.Fprintf(out, "- and %d more\n", len(changes)-commentLimit)
			break
		}
		fmt.Fprintf(out, "- %s\n", line(change))
	}
	out.WriteString("\n")
}

// quote renders a value for a one-line diff, collapsing prose so a paragraph
// does not become a wall inside a bullet.
func quote(value string) string {
	cleaned := strings.Join(strings.Fields(value), " ")
	switch {
	case cleaned == "":
		return "_empty_"
	case len(cleaned) > 60:
		return "`" + cleaned[:60] + "…`"
	}
	return "`" + cleaned + "`"
}
