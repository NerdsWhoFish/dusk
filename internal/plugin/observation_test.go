package plugin

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"google.golang.org/grpc"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/ingest"
)

type observationClient struct {
	duskv1alpha1.PluginServiceClient
	hang bool
}

func (c *observationClient) Ingest(ctx context.Context, _ *duskv1alpha1.IngestRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[duskv1alpha1.IngestResponse], error) {
	return &observationStream{ctx: ctx, hang: c.hang}, nil
}

type observationStream struct {
	grpc.ClientStream
	ctx  context.Context
	hang bool
}

func (s *observationStream) Recv() (*duskv1alpha1.IngestResponse, error) {
	if s.hang {
		<-s.ctx.Done()
		return nil, s.ctx.Err()
	}
	return nil, io.EOF
}

type observationStore struct{ writes int }

func (s *observationStore) Put(context.Context, string, string, []index.Declaration, []*duskv1alpha1.Relation, []*duskv1alpha1.Note) error {
	s.writes++
	return nil
}

func (*observationStore) DropRepository(context.Context, string, string) error { return nil }
func (*observationStore) SetDefaultView(context.Context, string, string) error { return nil }

func TestObservationTimeoutPreservesThePreviousRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := &observationClient{}
		running := &Running{ID: "bounded", client: client, exited: make(chan struct{})}
		store := &observationStore{}
		if result := ingest.Run(t.Context(), running, store, time.Now); result.Err != nil {
			t.Fatal(result.Err)
		}

		client.hang = true
		started := time.Now()
		result := ingest.Run(t.Context(), running, store, time.Now)
		if !errors.Is(result.Err, context.DeadlineExceeded) {
			t.Fatalf("hung observation returned %v, want deadline exceeded", result.Err)
		}
		if elapsed := time.Since(started); elapsed != observationTimeout {
			t.Errorf("observation took %s, want %s", elapsed, observationTimeout)
		}
		if store.writes != 1 {
			t.Fatalf("timed-out observation replaced the previous result: %d writes", store.writes)
		}

		client.hang = false
		if result := ingest.Run(t.Context(), running, store, time.Now); result.Err != nil {
			t.Fatalf("observation did not recover: %v", result.Err)
		}
		if store.writes != 2 {
			t.Errorf("recovery wrote %d results, want 2", store.writes)
		}
	})
}

func TestObservationRespectsAShorterCallerDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		running := &Running{ID: "bounded", client: &observationClient{hang: true}, exited: make(chan struct{})}
		started := time.Now()
		_, err := running.Observe(ctx)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != time.Minute {
			t.Fatalf("observation ignored caller deadline: elapsed %s, error %v", time.Since(started), err)
		}
	})
}
