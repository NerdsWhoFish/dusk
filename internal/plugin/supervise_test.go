package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NerdsWhoFish/dusk/internal/events"
	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/ingest"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// quick runs the same curve the defaults do, in milliseconds, so a test spends
// no time waiting out a backoff written for a real deployment.
func quick(limit int) plugin.RestartPolicy {
	return plugin.RestartPolicy{
		Base:   time.Millisecond,
		Max:    5 * time.Millisecond,
		Limit:  limit,
		Stable: time.Hour,
	}
}

// crasher declares one action that kills the plugin's own process, which is
// the only way to make a real subprocess die at a moment a test chooses.
func crasher(id string, class int32) standIn {
	return standIn{
		ID:      id,
		Kinds:   []string{"widget"},
		Actions: []standInAction{{Name: "poke", Class: class, Kinds: []string{"widget"}, Crash: true}},
	}
}

// supervised installs one plugin, starts it under the given restart policy,
// and enables everything it declares.
func supervised(t *testing.T, spec standIn, policy plugin.RestartPolicy) (*plugin.Manager, *rota) {
	t.Helper()

	manager, rotation := manager(t)
	manager.Restarts = policy
	manager.Catalog = &catalog{kind: "widget", version: "v1", observers: []string{"plugin:" + spec.ID}}
	manager.Proof = &proof.Store{}
	manager.Events = &events.Log{}

	install(t, manager.Store, spec)
	manager.Restore(t.Context())
	t.Cleanup(manager.Stop)

	for _, action := range spec.Actions {
		if err := manager.Enable(spec.ID, action.Name, true); err != nil {
			t.Fatalf("enable %s: %v", action.Name, err)
		}
	}
	return manager, rotation
}

// awaitPhase waits for the supervisor to reach a phase. A restart is
// asynchronous by nature: a process ending is what triggers it, so there is
// nothing a test can call to make it happen.
func awaitPhase(t *testing.T, manager *plugin.Manager, id, phase string) plugin.Process {
	t.Helper()

	var last plugin.Process
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		for _, report := range manager.Report() {
			if report.ID != id || report.Process == nil {
				continue
			}
			last = *report.Process
		}
		if last.Phase == phase {
			return last
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("%s never reached %q: it is %q, restarted %d times, %d attempts, last exit %q",
		id, phase, last.Phase, last.Restarts, last.Attempts, last.Exit)
	return last
}

func processOf(t *testing.T, manager *plugin.Manager, id string) plugin.Process {
	t.Helper()

	for _, report := range manager.Report() {
		if report.ID == id && report.Process != nil {
			return *report.Process
		}
	}
	t.Fatalf("nothing is known about %s's process", id)
	return plugin.Process{}
}

// ADR-0054: a plugin whose process dies is started again. Before this, every
// call to it failed until Dusk was restarted, which took actions down as well
// as observation.
func TestADR0055_ACrashedPluginIsStartedAgain(t *testing.T) {
	manager, rotation := supervised(t, crasher("crasher", readOnly), quick(5))

	if _, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:crasher/one", Action: "poke"}); err == nil {
		t.Fatal("the action killed the plugin's process, so it cannot have reported success")
	}

	process := awaitPhase(t, manager, "crasher", plugin.PhaseRunning)
	if process.Restarts < 1 {
		t.Errorf("restarts = %d, want the crash to have been counted", process.Restarts)
	}
	if process.Exit == "" {
		t.Error("the page cannot say how it died, and how it died is the whole question")
	}

	// Observing again is the point: a plugin that is up but out of the rotation
	// is the same outage with a better tag on it.
	observed := rotation.observed(t.Context(), t, "plugin:crasher")
	if len(observed.Entities) != 1 {
		t.Errorf("observed %d entities after the restart, want the plugin back in the rotation", len(observed.Entities))
	}
}

// ADR-0054, and ADR-0011 underneath it: a restart must never be observable as
// an empty ingest. An observation is complete by contract, so a process that
// cannot be asked has to fail rather than answer with nothing.
func TestADR0055_ARestartIsNeverAnEmptyObservation(t *testing.T) {
	// Long enough that the plugin is still waiting out its backoff while this
	// test asks it to observe.
	policy := plugin.RestartPolicy{Base: 30 * time.Second, Max: time.Minute, Limit: 5, Stable: time.Hour}
	manager, rotation := supervised(t, crasher("holder", readOnly), policy)

	db := newIndex(t)
	ingester := rotation.ingester(t, "plugin:holder")

	if result := ingest.Run(t.Context(), ingester, db, time.Now); result.Err != nil {
		t.Fatalf("the first run should have worked: %v", result.Err)
	}
	if got := observedCount(t, db); got != 1 {
		t.Fatalf("observed %d entities, want the one the plugin emits", got)
	}

	if _, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:holder/one", Action: "poke"}); err == nil {
		t.Fatal("the action killed the plugin's process, so it cannot have reported success")
	}
	awaitPhase(t, manager, "holder", plugin.PhaseRestarting)

	observed, err := ingester.Observe(t.Context())
	if err == nil {
		t.Fatalf("a plugin part way through a restart answered with %d entities instead of failing",
			len(observed.Entities))
	}
	if observed != nil {
		t.Error("a failed observation must be nil, because an empty one is a complete view of a source with nothing in it")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("the refusal should say the plugin is not running, got %q", err)
	}

	if result := ingest.Run(t.Context(), ingester, db, time.Now); result.Err == nil {
		t.Fatal("a run against a dead process reported success")
	}
	if got := observedCount(t, db); got != 1 {
		t.Errorf("observed %d entities after the failed run, want the one it had: a restart is not a deletion", got)
	}
}

// ADR-0054: a crash loop must not become a hot loop. A plugin that will not
// stay up ends in a state that says so rather than being started for ever.
func TestADR0055_APluginThatWillNotStayUpIsLeftFailed(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "refuse")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatalf("write the gate: %v", err)
	}

	spec := crasher("stubborn", readOnly)
	spec.RefuseWhile = gate

	manager, _ := manager(t)
	manager.Restarts = quick(3)
	install(t, manager.Store, spec)
	manager.Restore(t.Context())
	t.Cleanup(manager.Stop)

	process := awaitPhase(t, manager, "stubborn", plugin.PhaseFailed)
	if process.Attempts != 3 {
		t.Errorf("attempts = %d, want the limit of 3", process.Attempts)
	}
	if !strings.Contains(process.Exit, "widget server is unreachable") {
		t.Errorf("exit = %q, want what the plugin printed on its way out", process.Exit)
	}

	// Failed means nothing is retrying. A phase that says so while a goroutine
	// keeps starting processes would be the worst of both.
	settled := process.Restarts
	time.Sleep(50 * time.Millisecond)
	if after := processOf(t, manager, "stubborn"); after.Restarts != settled || after.Phase != plugin.PhaseFailed {
		t.Errorf("it is still being restarted after failing: %d restarts became %d, phase %q",
			settled, after.Restarts, after.Phase)
	}
}

// ADR-0054: failed is a state somebody can get out of. A plugin that is not
// running cannot be reconfigured either, so without this it would take an
// uninstall.
func TestRestartBringsBackAPluginThatHadFailed(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "refuse")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatalf("write the gate: %v", err)
	}

	spec := crasher("recovering", readOnly)
	spec.RefuseWhile = gate

	manager, rotation := manager(t)
	manager.Restarts = quick(2)
	install(t, manager.Store, spec)
	manager.Restore(t.Context())
	t.Cleanup(manager.Stop)

	awaitPhase(t, manager, "recovering", plugin.PhaseFailed)

	if err := os.Remove(gate); err != nil {
		t.Fatalf("remove the gate: %v", err)
	}
	if err := manager.Restart(t.Context(), "recovering"); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if process := processOf(t, manager, "recovering"); process.Phase != plugin.PhaseRunning {
		t.Errorf("phase = %q after a deliberate restart, want running", process.Phase)
	}
	if observed := rotation.observed(t.Context(), t, "plugin:recovering"); len(observed.Entities) != 1 {
		t.Errorf("observed %d entities, want the plugin back in the rotation", len(observed.Entities))
	}
}

// ADR-0054: a caller waiting on an action when the process goes away gets an
// error it can act on. A mutating one says the outcome is not known, because
// nothing survived that could say whether it landed.
func TestADR0055_AnActionInterruptedByTheProcessSaysSo(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		class int32
		says  []string
	}{
		{
			name:  "a read says only that the plugin went away",
			id:    "reader",
			class: readOnly,
			says:  []string{"is not running"},
		},
		{
			name:  "a mutation says the outcome is not known",
			id:    "mutator",
			class: mutating,
			says:  []string{"is not running", "not known"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, _ := supervised(t, crasher(test.id, test.class), quick(5))

			ref := "widget:" + test.id + "/one"
			request := plugin.Request{Ref: ref, Action: "poke"}
			if test.class != readOnly {
				request.Proof = read(manager.Proof, ref, "v1")
			}

			_, err := manager.Invoke(t.Context(), request)
			if err == nil {
				t.Fatal("the plugin killed its own process, so the invocation cannot have succeeded")
			}
			for _, want := range test.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the failure should mention %q, got %q", want, err)
				}
			}
		})
	}
}

// ADR-0054: Dusk stopping a plugin is not a crash. Restarting what somebody
// just uninstalled, or counting a reconfigure as a failure, would both be
// worse than not restarting anything.
func TestADR0055_ADeliberateStopIsNotACrash(t *testing.T) {
	t.Run("an uninstalled plugin stays gone", func(t *testing.T) {
		manager, _ := supervised(t, crasher("removed", readOnly), quick(5))

		if err := manager.Uninstall("removed"); err != nil {
			t.Fatalf("Uninstall: %v", err)
		}

		time.Sleep(50 * time.Millisecond)
		for _, report := range manager.Report() {
			if report.ID == "removed" {
				t.Fatalf("an uninstalled plugin is still reported: %+v", report)
			}
		}
	})

	t.Run("a reconfigure is not counted as a failure", func(t *testing.T) {
		spec := crasher("settled", readOnly)
		spec.Fields = []string{"cluster"}
		manager, _ := supervised(t, spec, quick(5))

		if err := manager.Configure(t.Context(), "settled", "", map[string]any{"cluster": "one"}); err != nil {
			t.Fatalf("Configure: %v", err)
		}

		time.Sleep(50 * time.Millisecond)
		process := processOf(t, manager, "settled")
		if process.Phase != plugin.PhaseRunning {
			t.Errorf("phase = %q after a reconfigure, want running", process.Phase)
		}
		if process.Restarts != 0 || process.Attempts != 0 {
			t.Errorf("a reconfigure was counted as a crash: %d restarts, %d attempts",
				process.Restarts, process.Attempts)
		}
	})
}

// What a plugin printed as it died is the answer to why it died, and a fresh
// buffer per process threw it away exactly when it mattered.
func TestOutputSurvivesARestart(t *testing.T) {
	manager, _ := supervised(t, crasher("noisy", readOnly), quick(5))

	if _, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:noisy/one", Action: "poke"}); err == nil {
		t.Fatal("the action killed the plugin's process, so it cannot have reported success")
	}
	awaitPhase(t, manager, "noisy", plugin.PhaseRunning)

	var said []string
	for _, line := range manager.Output("noisy") {
		said = append(said, line.Text)
	}
	joined := strings.Join(said, "\n")

	if !strings.Contains(joined, "the widget exploded") {
		t.Errorf("what the plugin printed before it died was lost: %q", joined)
	}
	if !strings.Contains(joined, "went away") {
		t.Errorf("the output does not say the process went away, so the restart is invisible in it: %q", joined)
	}
}

// ADR-0054: sockets are named by plugin id, so two Dusks sharing one directory
// each removed the other's on start and on stop. A supervisor multiplies those
// events, which is what made this worth fixing rather than recording.
func TestADR0055_TwoDusksDoNotShareASocket(t *testing.T) {
	was := plugin.SocketDir
	plugin.SocketDir = shortDir(t)
	t.Cleanup(func() { plugin.SocketDir = was })

	spec := standIn{ID: "shared", Kinds: []string{"widget"}}

	first, firstRota := newManager(t)
	install(t, first.Store, spec)
	first.Restore(t.Context())
	t.Cleanup(first.Stop)

	second, secondRota := newManager(t)
	install(t, second.Store, spec)
	second.Restore(t.Context())
	t.Cleanup(second.Stop)

	// The second start is what used to unlink the first one's live socket, so
	// asking the first to observe is the assertion.
	for name, rotation := range map[string]*rota{"first": firstRota, "second": secondRota} {
		observed := rotation.observed(t.Context(), t, "plugin:shared")
		if len(observed.Entities) != 1 {
			t.Errorf("the %s Dusk observed %d entities, want 1", name, len(observed.Entities))
		}
	}
}

// newIndex and observedCount stand in for what the ingest tests do, because the
// never-delete rule is only meaningful against a real store.
func newIndex(t *testing.T) *index.DB {
	t.Helper()

	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func observedCount(t *testing.T, db *index.DB) int {
	t.Helper()

	entities, err := db.List(t.Context(), ingest.ObservedRef, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return len(entities)
}
