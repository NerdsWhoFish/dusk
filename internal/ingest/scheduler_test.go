package ingest

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"github.com/NerdsWhoFish/dusk/internal/index"
)

type schedulerStore struct{}

func (schedulerStore) Put(context.Context, string, string, []index.Declaration, []*duskv1alpha1.Relation, []*duskv1alpha1.Note) error {
	return nil
}

func (schedulerStore) DropRepository(context.Context, string, string) error { return nil }
func (schedulerStore) SetDefaultView(context.Context, string, string) error { return nil }

type scheduledSource struct{ runs int }

func (*scheduledSource) Name() string            { return "late-plugin" }
func (*scheduledSource) Interval() time.Duration { return time.Hour }
func (s *scheduledSource) Observe(context.Context) (*Observation, error) {
	s.runs++
	return &Observation{}, nil
}

func TestSchedulerStartsPluginsAddedAfterBoot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		scheduler := NewScheduler(schedulerStore{}, slog.New(slog.DiscardHandler), time.Now)
		go scheduler.Start(ctx)
		synctest.Wait()

		source := &scheduledSource{}
		scheduler.Add(source)
		synctest.Wait()
		if source.runs != 1 {
			t.Fatalf("late plugin ran %d times, want its first observation immediately", source.runs)
		}

		scheduler.Due(source.Name())
		synctest.Wait()
		if source.runs != 2 {
			t.Fatalf("plugin ran %d times, want an immediate refresh after Due", source.runs)
		}
		cancel()
		synctest.Wait()
	})
}

func TestSchedulerRemovalClearsHealth(t *testing.T) {
	source := &scheduledSource{}
	scheduler := NewScheduler(schedulerStore{}, slog.New(slog.DiscardHandler), time.Now, source)
	scheduler.RunDue(t.Context())
	if len(scheduler.Status()) != 1 {
		t.Fatal("expected a completed observation before removal")
	}
	scheduler.Remove(source.Name())
	if status := scheduler.Status(); len(status) != 0 {
		t.Fatalf("removed ingester still reports health: %+v", status)
	}
	scheduler.RunDue(t.Context())
	if source.runs != 1 {
		t.Fatalf("removed ingester ran again: %d runs", source.runs)
	}
}

type blockedSource struct {
	scheduledSource
	started chan struct{}
	finish  chan struct{}
}

func (s *blockedSource) Observe(context.Context) (*Observation, error) {
	close(s.started)
	<-s.finish
	return nil, errors.New("retired source failed")
}

func TestSchedulerRetiredRunCannotPublishHealth(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "removed"
		if replace {
			name = "replaced"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				source := &blockedSource{started: make(chan struct{}), finish: make(chan struct{})}
				scheduler := NewScheduler(schedulerStore{}, slog.New(slog.DiscardHandler), time.Now, source)
				go scheduler.RunDue(t.Context())
				<-source.started
				scheduler.Remove(source.Name())
				if replace {
					scheduler.Add(&scheduledSource{})
					scheduler.RunDue(t.Context())
				}
				close(source.finish)
				synctest.Wait()
				status := scheduler.Status()
				if !replace {
					if len(status) != 0 {
						t.Fatalf("retired run restored health: %+v", status)
					}
					return
				}
				if len(status) != 1 || status[0].Err != nil || status[0].Failures != 0 || status[0].Next.IsZero() {
					t.Fatalf("retired failure replaced current health: %+v", status)
				}
			})
		})
	}
}
