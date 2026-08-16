package mcp

import (
	"fmt"
	"strings"
	"time"
)

// stamp renders a past time both ways, relative first. An agent has no
// dependable sense of now, so the relative half is what answers "is this
// stale"; the absolute half survives being quoted later (ADR-0056).
func stamp(now, at time.Time) string {
	if at.IsZero() {
		return "never"
	}
	return fmt.Sprintf("%s ago (%s)", span(now.Sub(at)), at.UTC().Format(time.RFC3339))
}

// due renders a scheduled time the same way, because a reader asking whether an
// answer is current is partly asking when it will be corrected.
func due(now, at time.Time) string {
	if at.IsZero() {
		return "not scheduled"
	}
	if wait := at.Sub(now); wait > 0 {
		return fmt.Sprintf("in %s (%s)", span(wait), at.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("now (%s)", at.UTC().Format(time.RFC3339))
}

// span phrases a gap in the largest unit that keeps it readable. Deterministic
// rather than approximate: two of these get compared, and a reader should not
// have to guess what a rounding covered.
func span(gap time.Duration) string {
	switch {
	case gap < 0:
		return "-" + span(-gap)
	case gap < time.Minute:
		return count(int(gap.Seconds()), "second")
	case gap < time.Hour:
		return count(int(gap.Minutes()), "minute")
	case gap < 24*time.Hour:
		return count(int(gap.Hours()), "hour")
	default:
		return count(int(gap.Hours()/24), "day")
	}
}

func count(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// readAt is when a repository's contents were last established. This process's
// own answer wins, because confirming a commit unmoved reads nothing and leaves
// no trace in the index; the durable one is a floor, never fresher (ADR-0056).
func readAt(status SyncStatus, lastRead map[string]time.Time) time.Time {
	if !status.At.IsZero() {
		return status.At
	}
	return lastRead[status.Repository]
}

// renderReads writes the per-repository half of `changes` and the tally under
// it, separating repositories that declared from ones that failed and ones with
// no dusk.md at all.
func renderReads(out *strings.Builder, now time.Time, statuses []SyncStatus, lastRead map[string]time.Time) {
	var declaring, failing, quiet int
	for _, status := range statuses {
		read := lastReadPhrase(now, readAt(status, lastRead))
		switch {
		case status.Error != "":
			failing++
			fmt.Fprintf(out, "- **%s**: %s, and the attempt %s failed: %s\n",
				status.Repository, read, stamp(now, status.Attempted), singleLine(status.Error))
		case status.Entities > 0:
			declaring++
			fmt.Fprintf(out, "- **%s** at `%s`: %d entities, %d relations, %s\n",
				status.Repository, short(status.Commit), status.Entities, status.Relations, read)
		default:
			quiet++
		}
	}

	if len(statuses) > 0 {
		fmt.Fprintf(out, "\n%d repository(s) declare entities, %d failed, and %d contain no dusk.md.\n",
			declaring, failing, quiet)
	}
}

// lastReadPhrase says when, or says nobody has. "Never read" has to be a
// sentence rather than a date nobody set, or it renders as 1970.
func lastReadPhrase(now, at time.Time) string {
	if at.IsZero() {
		return "never read"
	}
	return "last read " + stamp(now, at)
}

func renderSweep(out *strings.Builder, now, next time.Time) {
	if next.IsZero() {
		return
	}
	fmt.Fprintf(out, "Every repository is read again on the next sweep, %s.\n", due(now, next))
}
