package mcp

import "time"

// NotesBudget is the byte bound ADR-0059 puts on the notes in one answer, so a
// test can assert the bound rather than a number copied beside it.
const NotesBudget = notesBudget

// SetElicitPatience shortens how long one question waits, so a test can reach
// the deadline without spending it. Returns what restores the original.
func SetElicitPatience(d time.Duration) func() {
	was := elicitPatience
	elicitPatience = d
	return func() { elicitPatience = was }
}
