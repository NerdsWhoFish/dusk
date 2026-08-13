package plugin

import (
	"strings"
	"sync"
	"time"
)

// keptLines is how much of a plugin's output is held. Enough to see what it
// said as it failed, and bounded so a chatty plugin cannot grow the process.
const keptLines = 500

// Line is one thing a plugin printed.
type Line struct {
	At     time.Time `json:"at"`
	Stream string    `json:"stream"`
	Text   string    `json:"text"`
}

// output is a plugin's own stdout and stderr, kept in memory. An error string
// says roughly what went wrong; anything it leaves out is in here, rather than
// only in the log of the pod running Dusk.
type output struct {
	now func() time.Time

	mu    sync.Mutex
	lines []Line
}

func (o *output) write(stream, text string) {
	for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		o.mu.Lock()
		o.lines = append(o.lines, Line{At: o.at(), Stream: stream, Text: line})
		if extra := len(o.lines) - keptLines; extra > 0 {
			o.lines = o.lines[extra:]
		}
		o.mu.Unlock()
	}
}

func (o *output) at() time.Time {
	if o.now != nil {
		return o.now()
	}
	return time.Now()
}

// recent returns what the plugin printed, oldest first, which is the order it
// is read in.
func (o *output) recent() []Line {
	o.mu.Lock()
	defer o.mu.Unlock()

	lines := make([]Line, len(o.lines))
	copy(lines, o.lines)
	return lines
}

// Output is what a plugin has printed since it started.
func (m *Manager) Output(id string) []Line {
	m.mu.Lock()
	running, ok := m.running[id]
	m.mu.Unlock()

	if !ok {
		return nil
	}
	return running.output.recent()
}
