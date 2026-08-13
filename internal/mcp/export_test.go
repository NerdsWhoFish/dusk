package mcp

import "time"

// SetElicitPatience shortens how long one question waits, so a test can reach
// the deadline without spending it. Returns what restores the original.
func SetElicitPatience(d time.Duration) func() {
	was := elicitPatience
	elicitPatience = d
	return func() { elicitPatience = was }
}
