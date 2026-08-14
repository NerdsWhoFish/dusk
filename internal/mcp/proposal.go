package mcp

import (
	"fmt"
	"strings"

	"github.com/NerdsWhoFish/dusk/internal/store"
	"github.com/NerdsWhoFish/dusk/internal/write"
)

// proposal renders a write Dusk was not granted as the change it would have
// made. ADR-0010 has a write report where it landed so an agent can hand a
// human a link; this is the same courtesy where there is no link to give.
func proposal(result *write.Result) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Nothing was committed. %s\n\n", why(result.Mode))

	if result.Diff == "" {
		fmt.Fprintf(&out, "It would have changed nothing anyway: `%s` in %s already says this.\n",
			result.Path, result.Repository)
		return out.String()
	}

	verb := "changed"
	if result.Created {
		verb = "added"
	}
	fmt.Fprintf(&out, "This is what it would have %s, at `%s` in %s:\n\n```diff\n%s```\n\n",
		verb, result.Path, result.Repository, result.Diff)
	out.WriteString("Hand that to somebody who can apply it. `git apply` takes it as it stands, from the root of that repository.\n")
	return out.String()
}

// why says which posture refused the write, because the answer is different
// work: read mode is a decision to revisit, proposal mode is a gap in Dusk.
func why(mode store.AccessMode) string {
	switch mode {
	case store.ModeRead:
		return "Dusk is in read mode, where it never writes to your repositories. That was chosen when the App was registered, and changing it means changing the App's permissions on GitHub."
	case store.ModeProposal:
		return "Dusk is in proposal mode: it may open pull requests but may not commit, and opening one is not built yet."
	default:
		return "Dusk was not granted write access to your repositories."
	}
}
