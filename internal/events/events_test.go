package events_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/events"
)

func TestHistoryReceiptsAndHandlesSurviveAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.json")
	log, err := events.Open(path, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	event := events.Started("event-1", "chain-1", "plugin", "service:home/one", "restart", "agent", time.Now())
	if err := log.Emit(event); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := log.Settle(events.Finish(event, duskv1alpha1.EventStatus_EVENT_STATUS_SUCCEEDED, "done", nil, time.Now())); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if err := log.Remember("retry-1", "fingerprint-1", []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if err := log.LinkHandle("plugin", "handle-1", "event-1", true); err != nil {
		t.Fatalf("link: %v", err)
	}

	restored, err := events.Open(path, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if recent := restored.Recent(0); len(recent) != 1 || recent[0].GetMessage() != "done" {
		t.Fatalf("restored events = %+v", recent)
	}
	if outcome, used, err := restored.Recall("retry-1", "fingerprint-1"); err != nil || !used || !json.Valid(outcome) {
		t.Fatalf("restored receipt = %q, %v, %v", outcome, used, err)
	}
	if eventID, mutating, ok := restored.Handle("plugin", "handle-1"); !ok || !mutating || eventID != "event-1" {
		t.Fatalf("restored handle = %q, %v, %v", eventID, mutating, ok)
	}
}

func TestAnIdempotencyKeyCannotNameTwoRequests(t *testing.T) {
	log := &events.Log{}
	if err := log.Remember("same", "first", []byte(`{}`)); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if _, used, err := log.Recall("same", "second"); err == nil || !used {
		t.Fatalf("reusing a key for a different request = used %v, error %v", used, err)
	}
}

func TestRecentForReturnsOnlyOneEntityNewestFirst(t *testing.T) {
	log := &events.Log{}
	for _, event := range []*duskv1alpha1.Event{
		events.Started("one-old", "", "plugin", "service:home/one", "restart", "agent", time.Now()),
		events.Started("two", "", "plugin", "service:home/two", "restart", "agent", time.Now()),
		events.Started("one-new", "", "plugin", "service:home/one", "restart", "agent", time.Now()),
	} {
		if err := log.Emit(event); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}

	recent := log.RecentFor("service:home/one", 2)
	if len(recent) != 2 || recent[0].GetId() != "one-new" || recent[1].GetId() != "one-old" {
		t.Fatalf("recent for one = %+v", recent)
	}
	if got := log.RecentFor("service:home/missing", 0); len(got) != 0 {
		t.Fatalf("recent for missing = %+v", got)
	}
}
