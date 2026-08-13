// Package events records what an action invocation did.
//
// Events are always emitted and deliberately never written to the index: that
// store is disposable by contract and rebuilt from git, and an event cannot be
// rebuilt from anything (ADR-0015). Durable retention is an exporter's job.
package events

import (
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
)

// Kept is how many invocations the buffer holds. Enough to answer "what just
// happened" and small enough that a busy day cannot grow the process.
const Kept = 500

// Log is the bounded record of recent invocations, newest last.
type Log struct {
	// Kept defaults to the package constant.
	Kept int

	// Slog is where an event also goes, because a buffer that dies with the
	// process is not an audit trail and must not be presented as one.
	Slog *slog.Logger

	mu   sync.Mutex
	ring []*duskv1alpha1.Event
}

func (l *Log) kept() int {
	if l.Kept > 0 {
		return l.Kept
	}
	return Kept
}

func (l *Log) log() *slog.Logger {
	if l.Slog != nil {
		return l.Slog
	}
	return slog.Default()
}

// Emit records an invocation beginning. A nil Log discards, so a deployment
// that never wired one up does not panic on its first action.
func (l *Log) Emit(event *duskv1alpha1.Event) {
	if l == nil || event == nil {
		return
	}

	l.mu.Lock()
	l.ring = append(l.ring, event)
	if extra := len(l.ring) - l.kept(); extra > 0 {
		l.ring = l.ring[extra:]
	}
	l.mu.Unlock()

	l.write(event)
}

// Settle records how an invocation ended, replacing what Emit kept so the
// buffer holds one entry per invocation rather than one per state change. The
// log gets both lines, because a log wants the start and the end.
func (l *Log) Settle(event *duskv1alpha1.Event) {
	if l == nil || event == nil {
		return
	}

	l.mu.Lock()
	replaced := false
	for i, kept := range l.ring {
		if kept.GetId() == event.GetId() {
			l.ring[i] = event
			replaced = true
			break
		}
	}
	if !replaced {
		l.ring = append(l.ring, event)
	}
	l.mu.Unlock()

	l.write(event)
}

func (l *Log) write(event *duskv1alpha1.Event) {
	attrs := []any{
		"event", event.GetId(),
		"action", event.GetAction(),
		"plugin", event.GetPlugin(),
		"status", event.GetStatus().String(),
		"actor", event.GetActor(),
	}
	if ref := event.GetRef(); ref != "" {
		attrs = append(attrs, "ref", ref)
	}
	if chain := event.GetChain(); chain != "" {
		attrs = append(attrs, "chain", chain)
	}
	if message := event.GetMessage(); message != "" {
		attrs = append(attrs, "message", message)
	}
	l.log().Info("action", attrs...)
}

// Recent returns the newest events first, up to limit. Zero or less means all
// of them.
func (l *Log) Recent(limit int) []*duskv1alpha1.Event {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if limit <= 0 || limit > len(l.ring) {
		limit = len(l.ring)
	}

	newest := make([]*duskv1alpha1.Event, 0, limit)
	for i := len(l.ring) - 1; i >= len(l.ring)-limit; i-- {
		newest = append(newest, l.ring[i])
	}
	return newest
}

// Find returns one event by id, which is how an invocation worth keeping is
// promoted to a note (ADR-0015).
func (l *Log) Find(id string) (*duskv1alpha1.Event, bool) {
	if l == nil {
		return nil, false
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	for _, event := range l.ring {
		if event.GetId() == id {
			return event, true
		}
	}
	return nil, false
}

// Started builds the event an invocation begins with.
func Started(id, chain, plugin, ref, action, actor string, at time.Time) *duskv1alpha1.Event {
	return &duskv1alpha1.Event{
		Id:        id,
		Chain:     chain,
		Plugin:    plugin,
		Ref:       ref,
		Action:    action,
		Actor:     actor,
		Status:    duskv1alpha1.EventStatus_EVENT_STATUS_STARTED,
		StartedAt: timestamppb.New(at),
	}
}

// Finish stamps an event with how it ended.
func Finish(event *duskv1alpha1.Event, status duskv1alpha1.EventStatus, message string, detail *structpb.Struct, at time.Time) *duskv1alpha1.Event {
	event.Status = status
	event.Message = message
	event.Detail = detail
	event.FinishedAt = timestamppb.New(at)
	return event
}
