package controller_test

import (
	"context"
	"testing"
	"time"

	"github.com/NerdsWhoFish/dusk/internal/controller"
)

// noon is the instant these tests measure from, so an assertion names a time
// rather than whatever the machine's clock said.
var noon = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

func statusOf(t *testing.T, c *controller.Controller, repository string) controller.Status {
	t.Helper()

	for _, status := range c.Status() {
		if status.Repository == repository {
			return status
		}
	}
	t.Fatalf("no status for %q", repository)
	return controller.Status{}
}

// A repository that failed this morning and last read a week ago is a different
// situation from one read this morning, and recording the failure against the
// read time made the two identical.
func TestAFailedReadDoesNotDateTheCatalogByIt(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id: 10, account: "example",
		repos: map[string]string{"example/homelab": rootFile("jellyfin")},
	}}}

	clock := noon
	c, _ := newController(t, fake, "example", controller.Options{
		Now: func() time.Time { return clock },
	})

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// A week later the commit moves and reading it breaks.
	clock = noon.Add(7 * 24 * time.Hour)
	fake.sha = "0000000feedface"
	fake.failTarballs = 99
	_ = c.Sync(t.Context())

	status := statusOf(t, c, "example/homelab")
	if !status.At.Equal(noon) {
		t.Errorf("At = %v, want the last successful read at %v", status.At, noon)
	}
	if !status.Attempted.Equal(clock) {
		t.Errorf("Attempted = %v, want the failed try at %v", status.Attempted, clock)
	}
	if status.Error == "" {
		t.Error("a failed read recorded no error to explain the gap")
	}
}

// Confirming a commit has not moved reads nothing and is still a read: the
// catalog provably matches git as of that moment. Without it a repository
// nobody changes reads as one nobody has looked at since it last did.
func TestAnUnchangedCommitStillCountsAsARead(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id: 10, account: "example",
		repos: map[string]string{"example/homelab": rootFile("jellyfin")},
	}}}

	clock := noon
	c, _ := newController(t, fake, "example", controller.Options{
		Now: func() time.Time { return clock },
	})

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	clock = noon.Add(time.Hour)
	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	status := statusOf(t, c, "example/homelab")
	if !status.At.Equal(clock) {
		t.Errorf("At = %v, want the confirmation at %v", status.At, clock)
	}
	if status.Entities == 0 {
		t.Error("confirming an unchanged commit lost what the repository declares")
	}
}

// The poll floor is a day (ADR-0006), so how old a read is means little without
// knowing how much older it can get before anything corrects it.
func TestRunSaysWhenTheNextSweepIs(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id: 10, account: "example",
		repos: map[string]string{"example/homelab": rootFile("jellyfin")},
	}}}

	c, idx := newController(t, fake, "example", controller.Options{
		Interval: time.Hour,
		Now:      func() time.Time { return noon },
	})

	if got := c.NextSweep(); !got.IsZero() {
		t.Errorf("NextSweep = %v before Run started, want zero", got)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.After(10 * time.Second)
	for {
		if _, err := idx.Get(t.Context(), mainRef, "service:home/jellyfin"); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run did not sweep before its first tick")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if got, want := c.NextSweep(), noon.Add(time.Hour); !got.Equal(want) {
		t.Errorf("NextSweep = %v, want %v", got, want)
	}
}
