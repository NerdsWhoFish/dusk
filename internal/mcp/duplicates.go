package mcp

import (
	"fmt"
	"strings"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/write"
)

// opening is how much of a note is enough to recognise it. The whole body would
// cost context on every write to say something the caller already has.
const opening = 120

// renderExisting answers a note that was already written down. An agent with no
// memory of a previous session cannot know an id, so the answer is the id.
func renderExisting(result *write.Result) string {
	return fmt.Sprintf(
		"That note is already there, word for word, so nothing was written.\n\n"+
			"It is at `%s` in %s.\n\n"+
			"Pass that as `id`, with the proof token from a read that returned it, to change what it says. "+
			"Attaching it to something else is the same call.",
		result.Path, result.Repository)
}

// renderSimilar warns that something nearly says this already. It is a warning
// and not a refusal, because two notes that overlap are often both worth
// keeping and only a person can tell (ADR-0053).
func renderSimilar(alike []index.Similarity) string {
	if len(alike) == 0 {
		return ""
	}

	var out strings.Builder
	fmt.Fprintf(&out, "\n\n**%s already says something close to this:**\n\n", counted(len(alike)))
	for _, similar := range alike {
		fmt.Fprintf(&out, "- `%s` (%s) %s\n", similar.Id, similar.Kind, firstWords(similar.Body))
	}
	out.WriteString("\nIf one of them is the note you meant, replace it by its id rather than leaving two.\n")
	return out.String()
}

func counted(n int) string {
	if n == 1 {
		return "One note"
	}
	return fmt.Sprintf("%d notes", n)
}

func firstWords(body string) string {
	flat := singleLine(body)
	if len(flat) <= opening {
		return flat
	}
	return flat[:opening] + "..."
}
