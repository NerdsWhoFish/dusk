package ingest

import (
	"context"
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
