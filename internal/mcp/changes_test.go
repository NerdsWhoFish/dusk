package mcp_test

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/ingest"
	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
)

// clock is the instant every test in this file measures against, so an
// assertion names a phrase rather than whatever the machine's clock said.
var clock = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

// NextSweep is zero for the fixture in mcp_test.go, which is about the three
// repository states rather than about the poll floor.
func (f fakeSyncs) NextSweep() time.Time { return time.Time{} }

// syncs is a status feed that also knows when the poll floor next runs.
type syncs struct {
	statuses []mcp.SyncStatus
	next     time.Time
}

func (s syncs) Status() []mcp.SyncStatus { return s.statuses }
func (s syncs) NextSweep() time.Time     { return s.next }

// askChanges stands the surface up against a fixed clock and returns what
// `changes` answers, which is the only thing an agent ever sees.
func askChanges(t *testing.T, idx *index.DB, feed mcp.Syncs, plugins mcp.Plugins) string {
	t.Helper()

	session := serve(t, mcp.New(mcp.Options{
		Catalog: idx,
		Syncs:   feed,
		Plugins: plugins,
		Now:     func() time.Time { return clock },
		Version: "test",
	}))
	return call(t, session, "changes", nil)
}

// observed puts one entity in a scope with a known read time, which is what
// makes the age of that scope's contents assertable.
func observed(t *testing.T, idx *index.DB, repository, gitRef, ref string, at time.Time) {
	t.Helper()

	e := entity(ref, strings.SplitN(ref, ":", 2)[0], "", "")
	e.Provenance.ObservedAt = timestamppb.New(at)

	if err := idx.Put(t.Context(), repository, gitRef,
		[]index.Declaration{{Path: "dusk.md", Entity: e}}, nil, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), repository, gitRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("changes body missing %q:\n%s", want, body)
		}
	}
}

// Freshness is the whole reason `changes` exists, so an answer with no time on
// it cannot tell a stale catalog from a current one.
func TestChangesSaysWhenARepositoryWasRead(t *testing.T) {
	idx := newIndex(t)
	observed(t, idx, "example/homelab", mainRef, "service:home/jellyfin", clock.Add(-90*time.Minute))

	body := askChanges(t, idx, syncs{statuses: []mcp.SyncStatus{{
		Repository: "example/homelab",
		Commit:     "abc1234def",
		Entities:   1,
		At:         clock.Add(-90 * time.Minute),
		Attempted:  clock.Add(-90 * time.Minute),
	}}}, nil)

	// Relative for a reader with no clock of its own, absolute for one
	// correlating with anything else.
	mustContain(t, body, "1 hour ago", "2026-08-15T10:30:00Z")
}

// A repository that failed recently while last succeeding a week ago is a
// different situation from one read a minute ago, and both used to render the
// same way: a commit, a count, and no time at all.
func TestChangesSeparatesTheLastReadFromTheLastAttempt(t *testing.T) {
	idx := newIndex(t)
	observed(t, idx, "example/broken", mainRef, "service:home/old", clock.Add(-7*24*time.Hour))

	body := askChanges(t, idx, syncs{statuses: []mcp.SyncStatus{{
		Repository: "example/broken",
		Commit:     "abc1234def",
		Entities:   1,
		At:         clock.Add(-7 * 24 * time.Hour),
		Attempted:  clock.Add(-4 * time.Hour),
		Error:      "read dusk.md: boom",
	}}}, nil)

	mustContain(t, body, "7 days ago", "4 hours ago", "boom")
}

// ADR-0056: a read time is stored with the content it describes, so it survives
// a restart. Remembering it in the process would reset every repository to
// "never read" while the catalog carried on serving what it read last week.
func TestADR0058_AReadTimeSurvivesARestart(t *testing.T) {
	idx := newIndex(t)
	observed(t, idx, "example/homelab", mainRef, "service:home/jellyfin", clock.Add(-6*24*time.Hour))

	// Nothing in memory: this is a Dusk that has just come up, with a full
	// index and a sweep that has not finished.
	body := askChanges(t, idx, syncs{statuses: []mcp.SyncStatus{{
		Repository: "example/homelab",
		Commit:     "abc1234def",
		Entities:   1,
	}}}, nil)

	mustContain(t, body, "6 days ago", "2026-08-09T12:00:00Z")
	if strings.Contains(body, "never read") {
		t.Errorf("a restart made a six day old read read as no read at all:\n%s", body)
	}
}

// ADR-0011 keeps what a failing ingester last observed on purpose, which makes
// "failing since Tuesday, serving what it saw on Monday" the normal state
// rather than an edge case. Nothing surfaced either half of it.
func TestADR0011_AFailingIngesterSaysHowOldWhatItServesIs(t *testing.T) {
	idx := newIndex(t)
	scope := ingest.Scope("plugin:kubernetes")
	observed(t, idx, scope, ingest.ObservedRef, "service:cluster/api", clock.Add(-3*24*time.Hour))

	body := askChanges(t, idx, syncs{}, &offering{reports: []plugin.Report{{
		ID: "kubernetes", Version: "v0.2.0", Running: true,
		Health: []plugin.Health{{
			Instance: "prod",
			Scope:    scope,
			At:       clock.Add(-20 * time.Minute),
			Problem:  "dial tcp: i/o timeout",
			Failures: 5,
			Next:     clock.Add(26 * time.Minute),
		}},
	}}})

	mustContain(t, body,
		"3 days ago",           // what it is still serving
		"20 minutes ago",       // when it last tried
		"dial tcp",             // why that failed
		"5 runs in a row",      // how long it has been failing
		"26 minutes",           // when it tries again
		"2026-08-12T12:00:00Z", // the absolute half of the observation
	)
}

// A healthy instance still says how old its answer is, because "is this
// current" is asked of working plugins more often than of broken ones.
func TestChangesDatesAHealthyObservation(t *testing.T) {
	idx := newIndex(t)
	scope := ingest.Scope("plugin:kubernetes")
	observed(t, idx, scope, ingest.ObservedRef, "service:cluster/api", clock.Add(-4*time.Minute))

	body := askChanges(t, idx, syncs{}, &offering{reports: []plugin.Report{{
		ID: "kubernetes", Version: "v0.2.0", Running: true,
		Health: []plugin.Health{{
			Scope: scope, At: clock.Add(-4 * time.Minute), Entities: 12,
		}},
	}}})

	mustContain(t, body, "4 minutes ago")
}

// The poll floor is a day (ADR-0006), so how old an answer is means little
// without knowing how long it can get before anything corrects it.
func TestChangesSaysWhenTheNextSweepIs(t *testing.T) {
	body := askChanges(t, newIndex(t), syncs{
		statuses: []mcp.SyncStatus{{Repository: "example/homelab", Commit: "abc", Entities: 1, At: clock}},
		next:     clock.Add(19 * time.Hour),
	}, nil)

	mustContain(t, body, "19 hours", "2026-08-16T07:00:00Z")
}

// A relative phrase is what answers "is this stale", so it has to be
// deterministic rather than approximate: an agent comparing two of them should
// not have to guess what a rounding covered.
func TestChangesPhrasesEveryGapTheSameWay(t *testing.T) {
	for _, tc := range []struct {
		name string
		ago  time.Duration
		want string
	}{
		{"seconds", 30 * time.Second, "30 seconds ago"},
		{"one minute", time.Minute, "1 minute ago"},
		{"minutes", 45 * time.Minute, "45 minutes ago"},
		{"one hour", time.Hour, "1 hour ago"},
		{"hours", 5 * time.Hour, "5 hours ago"},
		{"one day", 24 * time.Hour, "1 day ago"},
		{"days", 9 * 24 * time.Hour, "9 days ago"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := askChanges(t, newIndex(t), syncs{statuses: []mcp.SyncStatus{{
				Repository: "example/homelab", Commit: "abc", Entities: 1,
				At: clock.Add(-tc.ago),
			}}}, nil)
			mustContain(t, body, tc.want)
		})
	}
}

// A repository nothing has ever read says so, rather than borrowing a zero time
// and claiming it was read at the epoch.
func TestChangesNamesARepositoryItHasNeverRead(t *testing.T) {
	body := askChanges(t, newIndex(t), syncs{statuses: []mcp.SyncStatus{{
		Repository: "example/broken",
		Attempted:  clock.Add(-time.Hour),
		Error:      "read dusk.md: boom",
	}}}, nil)

	mustContain(t, body, "never read", "1 hour ago")
	if strings.Contains(body, "1970") {
		t.Errorf("a zero read time was rendered as a date:\n%s", body)
	}
}

// The index is the durable half, so it has to report the read time of a scope
// whatever kind of scope it is: a repository read from git and an ingester's
// observations are dated the same way.
func TestLastReadCoversRepositoriesAndObservations(t *testing.T) {
	idx := newIndex(t)
	observed(t, idx, "example/homelab", mainRef, "service:home/jellyfin", clock.Add(-time.Hour))
	observed(t, idx, ingest.Scope("plugin:kubernetes"), ingest.ObservedRef, "service:cluster/api", clock.Add(-time.Minute))

	reads, err := idx.LastRead(t.Context())
	if err != nil {
		t.Fatalf("LastRead: %v", err)
	}

	for scope, want := range map[string]time.Time{
		"example/homelab":            clock.Add(-time.Hour),
		"ingester:plugin:kubernetes": clock.Add(-time.Minute),
	} {
		if got := reads[scope]; !got.Equal(want) {
			t.Errorf("LastRead[%q] = %v, want %v", scope, got, want)
		}
	}
}
