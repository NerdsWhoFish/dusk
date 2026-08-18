// Package events records what an action invocation did.
//
// Events are written beside the disposable index, never into it. Action
// history and retry receipts cannot be rebuilt from Git, so they survive a
// restart even though the materialized catalog deliberately does not.
package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
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
	Path string

	mu       sync.Mutex
	ring     []*duskv1alpha1.Event
	receipts map[string]receipt
	handles  map[string]handle
}

type receipt struct {
	Fingerprint string          `json:"fingerprint"`
	Outcome     json.RawMessage `json:"outcome"`
}

type handle struct {
	Event    string `json:"event"`
	Mutating bool   `json:"mutating,omitempty"`
}

type persisted struct {
	Events   []json.RawMessage  `json:"events"`
	Receipts map[string]receipt `json:"receipts,omitempty"`
	Handles  map[string]handle  `json:"handles,omitempty"`
}

// Open restores a durable action log. A missing file is a new log; malformed
// history is refused because silently discarding retry receipts can repeat a
// mutation after a restart.
func Open(path string, logger *slog.Logger) (*Log, error) {
	log := &Log{Path: path, Slog: logger}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return log, nil
	}
	if err != nil {
		return nil, fmt.Errorf("events: read %s: %w", path, err)
	}
	var stored persisted
	if err := json.Unmarshal(body, &stored); err != nil {
		return nil, fmt.Errorf("events: decode %s: %w", path, err)
	}
	for _, encoded := range stored.Events {
		event := &duskv1alpha1.Event{}
		if err := protojson.Unmarshal(encoded, event); err != nil {
			return nil, fmt.Errorf("events: decode event in %s: %w", path, err)
		}
		log.ring = append(log.ring, event)
	}
	log.receipts = stored.Receipts
	log.handles = stored.Handles
	return log, nil
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
func (l *Log) Emit(event *duskv1alpha1.Event) error {
	if l == nil || event == nil {
		return nil
	}

	l.mu.Lock()
	before := append([]*duskv1alpha1.Event(nil), l.ring...)
	l.ring = append(l.ring, event)
	if extra := len(l.ring) - l.kept(); extra > 0 {
		l.ring = l.ring[extra:]
	}
	err := l.persistLocked()
	if err != nil {
		l.ring = before
	}
	l.mu.Unlock()
	if err != nil {
		return err
	}

	l.write(event)
	return nil
}

// Settle records how an invocation ended, replacing what Emit kept so the
// buffer holds one entry per invocation rather than one per state change. The
// log gets both lines, because a log wants the start and the end.
func (l *Log) Settle(event *duskv1alpha1.Event) error {
	if l == nil || event == nil {
		return nil
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
	err := l.persistLocked()
	l.mu.Unlock()

	l.write(event)
	return err
}

// Remember stores the completed answer for an idempotency key.
func (l *Log) Remember(key, fingerprint string, outcome []byte) error {
	if l == nil || key == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.receipts == nil {
		l.receipts = map[string]receipt{}
	}
	previous, existed := l.receipts[key]
	l.receipts[key] = receipt{Fingerprint: fingerprint, Outcome: append([]byte(nil), outcome...)}
	if err := l.persistLocked(); err != nil {
		if existed {
			l.receipts[key] = previous
		} else {
			delete(l.receipts, key)
		}
		return err
	}
	return nil
}

// Forget removes a reservation when a plugin asked a question rather than
// running. Resuming that conversation with the same key must reach the plugin.
func (l *Log) Forget(key string) error {
	if l == nil || key == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	previous, existed := l.receipts[key]
	delete(l.receipts, key)
	if err := l.persistLocked(); err != nil {
		if existed {
			l.receipts[key] = previous
		}
		return err
	}
	return nil
}

// Recall returns a previous answer, and refuses reuse of a key for a different
// request. The boolean says the key has already been used.
func (l *Log) Recall(key, fingerprint string) ([]byte, bool, error) {
	if l == nil || key == "" {
		return nil, false, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	kept, ok := l.receipts[key]
	if !ok {
		return nil, false, nil
	}
	if kept.Fingerprint != fingerprint {
		return nil, true, fmt.Errorf("events: idempotency key %q was already used for a different action", key)
	}
	return append([]byte(nil), kept.Outcome...), true, nil
}

// LinkHandle keeps an asynchronous plugin handle tied to the event it began.
func (l *Log) LinkHandle(plugin, value, event string, mutating bool) error {
	if l == nil || value == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.handles == nil {
		l.handles = map[string]handle{}
	}
	l.handles[plugin+"\x00"+value] = handle{Event: event, Mutating: mutating}
	return l.persistLocked()
}

// Handle returns the event behind an asynchronous plugin handle.
func (l *Log) Handle(plugin, value string) (string, bool, bool) {
	if l == nil {
		return "", false, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	linked, ok := l.handles[plugin+"\x00"+value]
	return linked.Event, linked.Mutating, ok
}

func (l *Log) persistLocked() error {
	if l.Path == "" {
		return nil
	}
	stored := persisted{Receipts: l.receipts, Handles: l.handles}
	for _, event := range l.ring {
		encoded, err := protojson.Marshal(event)
		if err != nil {
			return fmt.Errorf("events: encode %s: %w", event.GetId(), err)
		}
		stored.Events = append(stored.Events, encoded)
	}
	body, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("events: encode history: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return fmt.Errorf("events: make history directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(l.Path), filepath.Base(l.Path)+".*")
	if err != nil {
		return fmt.Errorf("events: create history: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("events: protect history: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("events: write history: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("events: close history: %w", err)
	}
	if err := os.Rename(tmp.Name(), l.Path); err != nil {
		return fmt.Errorf("events: publish history: %w", err)
	}
	return nil
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
	return l.recent(limit, "")
}

// RecentFor returns the newest events about one entity first, up to limit.
// It is deliberately an exact ref match: operational history for one thing
// must not absorb another entity whose ref merely shares a prefix.
func (l *Log) RecentFor(ref string, limit int) []*duskv1alpha1.Event {
	return l.recent(limit, ref)
}

func (l *Log) recent(limit int, ref string) []*duskv1alpha1.Event {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	capacity := limit
	if capacity <= 0 || capacity > len(l.ring) {
		capacity = len(l.ring)
	}

	newest := make([]*duskv1alpha1.Event, 0, capacity)
	for i := len(l.ring) - 1; i >= 0; i-- {
		if ref != "" && l.ring[i].GetRef() != ref {
			continue
		}
		newest = append(newest, l.ring[i])
		if limit > 0 && len(newest) == limit {
			break
		}
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
