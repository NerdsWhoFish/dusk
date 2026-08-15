package plugin

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Phase is what the supervisor believes about a plugin's process. It is what
// the plugin's page shows, because "installed" and "answering" are different
// facts and only one of them decides whether an action can run (ADR-0054).
const (
	// PhaseRunning means the process is up and serving its socket.
	PhaseRunning = "running"

	// PhaseRestarting means it died and a start is waiting out its backoff.
	PhaseRestarting = "restarting"

	// PhaseFailed means it would not stay up and nothing is retrying. The way
	// back is a deliberate act, which is what makes it a state rather than a
	// pause.
	PhaseFailed = "failed"

	// PhaseStopped means Dusk stopped it: uninstalled, reconfigured, or shut
	// down. Nothing restarts it, because nothing crashed.
	PhaseStopped = "stopped"
)

// Defaults for RestartPolicy. Doubling from a second, eight times, capped at a
// minute is a little under four minutes of trying, which ADR-0054 argues is
// the balance between riding out a blip and saying so while somebody is there.
const (
	defaultRestartBase   = time.Second
	defaultRestartMax    = time.Minute
	defaultRestartLimit  = 8
	defaultRestartStable = time.Minute
)

// RestartPolicy is how hard the supervisor tries to keep a plugin up. Zero
// fields take the defaults; a test sets them to run the same curve in
// milliseconds.
type RestartPolicy struct {
	// Base is the wait before the first restart, doubling with each attempt.
	Base time.Duration

	// Max caps that wait, so a crash loop costs a start a minute rather than
	// a start a millisecond.
	Max time.Duration

	// Limit is how many starts in a row may fail before the plugin is left
	// failed rather than retried.
	Limit int

	// Stable is how long a process must run to count as having worked, which
	// is what resets the attempts. Without it a plugin that has served for a
	// week is given up on the second time it ever crashes.
	Stable time.Duration
}

func (m *Manager) restarts() RestartPolicy {
	policy := m.Restarts
	if policy.Base <= 0 {
		policy.Base = defaultRestartBase
	}
	if policy.Max <= 0 {
		policy.Max = defaultRestartMax
	}
	if policy.Limit <= 0 {
		policy.Limit = defaultRestartLimit
	}
	if policy.Stable <= 0 {
		policy.Stable = defaultRestartStable
	}
	return policy
}

// Process is what the supervisor knows about a plugin's own process, as
// against Health, which is what its observations did. A plugin can be up and
// observing nothing, and it can be observing nothing because it is not up.
type Process struct {
	Phase string `json:"phase"`

	// Since is when the process now serving started, and zero when none is.
	// With Restarts it tells "crashed once and came back" from "has never
	// stayed up".
	Since time.Time `json:"since"`

	// Restarts is how many times Dusk has started this plugin again after it
	// died, for as long as this Dusk has been up. Never reset: "it has been
	// restarted forty times today" is the fact somebody needs.
	Restarts int `json:"restarts"`

	// Attempts is how many starts in a row have failed. Reaching the limit is
	// what makes the phase failed.
	Attempts int `json:"attempts,omitempty"`

	// Exit is how the process last ended, and when.
	Exit   string    `json:"exit,omitempty"`
	ExitAt time.Time `json:"exit_at"`

	// Next is when the next start will be attempted, while restarting.
	Next time.Time `json:"next"`
}

// supervision is the live record behind a Process. One per plugin for the life
// of the Manager, so a restart count survives the process it counts.
type supervision struct {
	phase    string
	since    time.Time
	restarts int
	attempts int
	exit     string
	exitAt   time.Time
	next     time.Time

	// halt closes when Dusk stops this plugin deliberately, so a retry waiting
	// out its backoff starts nothing. A fresh one per start doubles as the
	// identity a stale supervisor compares itself against.
	halt chan struct{}
}

// watched returns a plugin's supervision record, creating it on first use.
// Callers hold m.mu.
func (m *Manager) watched(id string) *supervision {
	if m.supervised == nil {
		m.supervised = map[string]*supervision{}
	}
	if watch, ok := m.supervised[id]; ok {
		return watch
	}

	watch := &supervision{phase: PhaseStopped}
	m.supervised[id] = watch
	return watch
}

// up records a process that is now serving, and answers with the channel that
// ends its supervision.
func (m *Manager) up(id string) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	watch := m.watched(id)
	watch.phase = PhaseRunning
	watch.since = m.now()
	watch.next = time.Time{}
	watch.halt = make(chan struct{})
	return watch.halt
}

// supervising returns the channel that ends this plugin's supervision, minting
// one when a start failed and there is no process to have attached it to.
func (m *Manager) supervising(id string) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	watch := m.watched(id)
	if watch.halt == nil {
		watch.halt = make(chan struct{})
	}
	return watch.halt
}

// died records a process that ended without being asked to.
func (m *Manager) died(id, why string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	watch := m.watched(id)
	at := m.now()

	// A plugin that ran and then broke is not one that will not start.
	// Counting them together gives up on a plugin that has served for a week
	// the second time it ever crashes.
	if !watch.since.IsZero() && at.Sub(watch.since) >= m.restarts().Stable {
		watch.attempts = 0
	}

	watch.attempts++
	watch.phase = PhaseRestarting
	watch.exit, watch.exitAt = why, at
	watch.since = time.Time{}
}

// halted records a plugin Dusk stopped on purpose and ends its supervision.
// Callers hold m.mu.
func (m *Manager) halted(id string) {
	watch := m.watched(id)
	if watch.halt != nil {
		close(watch.halt)
		watch.halt = nil
	}
	watch.phase = PhaseStopped
	watch.since = time.Time{}
	watch.next = time.Time{}
}

// afresh forgets how many starts have failed, so a deliberate start gets the
// full run of attempts rather than inheriting a spent budget.
func (m *Manager) afresh(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.watched(id).attempts = 0
}

// supervise waits for one process to end and decides what that means. It is
// the whole of the restart trigger: nothing polls, because a process ending is
// something the operating system already tells us.
func (m *Manager) supervise(ctx context.Context, id string, running *Running, halt chan struct{}) {
	select {
	case <-halt:
		return
	case <-ctx.Done():
		return
	case <-running.exited:
	}
	if running.stopped.Load() {
		return
	}

	m.log().Error("a plugin's process went away", "plugin", id, "exit", running.wentAway())
	running.output.write("dusk", "the process went away: "+running.wentAway())
	m.died(id, running.wentAway())
	m.retry(ctx, id, halt)
}

// retry starts a plugin again, waiting longer between each attempt, until one
// works or the policy runs out of them.
func (m *Manager) retry(ctx context.Context, id string, halt chan struct{}) {
	for {
		wait, ok := m.nextStart(id, halt)
		if !ok {
			return
		}

		timer := time.NewTimer(wait)
		select {
		case <-halt:
			timer.Stop()
			return
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		record, err := m.Store.Read(id)
		if err != nil {
			// Uninstalled while the backoff ran. There is nothing to start and
			// nothing has gone wrong.
			return
		}
		if !m.current(id, halt) {
			return
		}

		started := m.start(ctx, *record)
		if started == nil {
			// Counted here rather than at the attempt, so "restarted three
			// times" means three processes and not three tries.
			m.restarted(id)
			return
		}

		m.log().Error("could not start a plugin again", "plugin", id, "error", started)
		m.died(id, started.Error())
	}
}

// nextStart decides whether to try again and how long to wait first. Giving up
// is a state rather than silence: a plugin that will not stay up has to be
// readable as that rather than as one nobody has looked at.
func (m *Manager) nextStart(id string, halt chan struct{}) (time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	watch := m.watched(id)
	if watch.halt != halt {
		return 0, false
	}

	policy := m.restarts()
	if watch.attempts >= policy.Limit {
		watch.phase = PhaseFailed
		watch.next = time.Time{}
		m.log().Error("a plugin will not stay up and is no longer being restarted",
			"plugin", id, "attempts", watch.attempts, "exit", watch.exit)
		return 0, false
	}

	wait := backoffFor(watch.attempts, policy)
	watch.phase = PhaseRestarting
	watch.next = m.now().Add(wait)
	return wait, true
}

// current reports whether this supervisor is still the one in charge, which it
// is not once something else has stopped or started the plugin.
func (m *Manager) current(id string, halt chan struct{}) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.watched(id).halt == halt
}

func (m *Manager) restarted(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.watched(id).restarts++
}

// forget drops what is known about a plugin's process, so reinstalling one is
// not haunted by the exit and the restart count of the install before it.
func (m *Manager) forget(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.supervised, id)
}

// backoffFor grows the wait between starts, so a plugin dying on start cannot
// become a fork bomb. Its own curve rather than the ingest scheduler's: that
// one paces an upstream API budget, this one paces exec.
func backoffFor(attempts int, policy RestartPolicy) time.Duration {
	if attempts < 1 {
		attempts = 1
	}

	grown := float64(policy.Base) * math.Pow(2, float64(attempts-1))
	if grown > float64(policy.Max) {
		return policy.Max
	}
	return time.Duration(grown)
}

// serving reports whether a process is answering, which is not the same as
// Dusk holding a handle to one.
func serving(process *Process) bool {
	return process != nil && process.Phase == PhaseRunning
}

// process is what to report about a plugin's process. Callers hold m.mu.
func (m *Manager) process(id string) *Process {
	watch, ok := m.supervised[id]
	if !ok {
		return nil
	}

	return &Process{
		Phase:    watch.phase,
		Since:    watch.since,
		Restarts: watch.restarts,
		Attempts: watch.attempts,
		Exit:     watch.exit,
		ExitAt:   watch.exitAt,
		Next:     watch.next,
	}
}

// launch starts a plugin and supervises it either way. A start that failed is
// still a plugin Dusk wants up, so the retry begins from the failure rather
// than only from a process that had once worked.
func (m *Manager) launch(ctx context.Context, record Installed) error {
	err := m.start(ctx, record)
	if err == nil {
		return nil
	}

	m.died(record.ID, err.Error())
	go m.retry(context.WithoutCancel(ctx), record.ID, m.supervising(record.ID))
	return err
}

// Restart starts a plugin again now, whatever the supervisor had decided. It
// is the only way back from failed: a plugin that is not running cannot be
// reconfigured, because only a running one says which fields are secret.
func (m *Manager) Restart(ctx context.Context, id string) error {
	record, err := m.Store.Read(id)
	if err != nil {
		return fmt.Errorf("plugin: %q is not installed", id)
	}

	m.stop(id)
	m.afresh(id)
	return m.launch(ctx, *record)
}
