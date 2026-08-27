// Package insights derives operator-facing analytics from Dusk's local state.
package insights

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

const rankedLimit = 5

// Catalog is the local materialized state an analytics snapshot reads.
type Catalog interface {
	Kinds(context.Context, string, index.Visibility) ([]index.KindCount, error)
	RepositoryCounts(context.Context, string) ([]index.RepositoryCount, error)
	AllNotes(context.Context, string, index.NoteFilter) ([]*duskv1alpha1.Note, error)
	Minted(context.Context, string) ([]vocab.Kind, error)
}

// Actions is the bounded local action history.
type Actions interface {
	Recent(int) []*duskv1alpha1.Event
}

// Service derives one analytics snapshot without recording a new event.
type Service struct {
	Catalog Catalog
	Actions Actions
}

// Snapshot is the dashboard's local view of catalog shape, knowledge reach,
// and plugin activity.
type Snapshot struct {
	Entities     int                     `json:"entities"`
	Repositories int                     `json:"repositories"`
	Notes        int                     `json:"notes"`
	OpenWork     int                     `json:"open_work"`
	Actions      int                     `json:"actions"`
	Sources      []index.RepositoryCount `json:"sources"`
	Knowledge    []Knowledge             `json:"knowledge"`
	Plugins      []PluginActivity        `json:"plugins"`
	NoteKinds    []KindCount             `json:"note_kinds"`
}

// Knowledge is one note ranked by how many catalog refs it connects.
type Knowledge struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Body   string `json:"body"`
	Links  int    `json:"links"`
	Pinned bool   `json:"pinned,omitempty"`
}

// PluginActivity summarizes one plugin's invocations in the retained window.
type PluginActivity struct {
	Plugin    string `json:"plugin"`
	Actions   int    `json:"actions"`
	Succeeded int    `json:"succeeded"`
	Problems  int    `json:"problems"`
	LastUsed  string `json:"last_used,omitempty"`
}

// KindCount is one note kind and how many notes carry it.
type KindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// Read derives a snapshot from the current default catalog view and the
// retained action log. It performs no network calls and writes nothing.
func (s *Service) Read(ctx context.Context) (Snapshot, error) {
	if s == nil || s.Catalog == nil {
		return Snapshot{}, fmt.Errorf("analytics are not configured")
	}

	kinds, err := s.Catalog.Kinds(ctx, "", index.Unrestricted())
	if err != nil {
		return Snapshot{}, err
	}
	repositories, err := s.Catalog.RepositoryCounts(ctx, "")
	if err != nil {
		return Snapshot{}, err
	}
	notes, err := s.Catalog.AllNotes(ctx, "", index.NoteFilter{})
	if err != nil {
		return Snapshot{}, err
	}
	minted, err := s.Catalog.Minted(ctx, "")
	if err != nil {
		return Snapshot{}, err
	}

	out := Snapshot{Repositories: len(repositories), Notes: len(notes)}
	for _, kind := range kinds {
		out.Entities += kind.Count
	}
	out.Sources = slices.Clone(repositories[:min(len(repositories), rankedLimit)])
	out.Knowledge, out.NoteKinds, out.OpenWork = knowledge(notes, minted)
	out.Plugins, out.Actions = plugins(s.Actions)
	return out, nil
}

func knowledge(notes []*duskv1alpha1.Note, minted []vocab.Kind) ([]Knowledge, []KindCount, int) {
	counts := map[string]int{}
	open := 0
	ranked := make([]Knowledge, 0, len(notes))
	for _, note := range notes {
		counts[note.GetKind()]++
		if vocab.RoleOf(vocab.Note, note.GetKind(), minted) == vocab.Work &&
			(note.GetStatus() == "" || note.GetStatus() == duskmd.StatusOpen) {
			open++
		}
		ranked = append(ranked, Knowledge{
			ID: note.GetId(), Kind: note.GetKind(), Body: note.GetBody(),
			Links: len(note.GetRefs()), Pinned: note.GetPinned(),
		})
	}

	slices.SortFunc(ranked, func(a, b Knowledge) int {
		if a.Links != b.Links {
			return b.Links - a.Links
		}
		if a.Pinned != b.Pinned {
			if a.Pinned {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	ranked = ranked[:min(len(ranked), rankedLimit)]

	byKind := make([]KindCount, 0, len(counts))
	for kind, count := range counts {
		byKind = append(byKind, KindCount{Kind: kind, Count: count})
	}
	slices.SortFunc(byKind, func(a, b KindCount) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Kind, b.Kind)
	})
	return ranked, byKind[:min(len(byKind), rankedLimit)], open
}

func plugins(actions Actions) ([]PluginActivity, int) {
	if actions == nil {
		return nil, 0
	}
	recent := actions.Recent(0)
	byPlugin := map[string]*PluginActivity{}
	for _, event := range recent {
		name := event.GetPlugin()
		if name == "" {
			continue
		}
		activity := byPlugin[name]
		if activity == nil {
			activity = &PluginActivity{Plugin: name}
			byPlugin[name] = activity
		}
		record(activity, event)
	}

	ranked := make([]PluginActivity, 0, len(byPlugin))
	for _, activity := range byPlugin {
		ranked = append(ranked, *activity)
	}
	slices.SortFunc(ranked, func(a, b PluginActivity) int {
		if a.Actions != b.Actions {
			return b.Actions - a.Actions
		}
		return strings.Compare(a.Plugin, b.Plugin)
	})
	return ranked[:min(len(ranked), rankedLimit)], len(recent)
}

func record(activity *PluginActivity, event *duskv1alpha1.Event) {
	activity.Actions++
	switch event.GetStatus() {
	case duskv1alpha1.EventStatus_EVENT_STATUS_SUCCEEDED:
		activity.Succeeded++
	case duskv1alpha1.EventStatus_EVENT_STATUS_FAILED,
		duskv1alpha1.EventStatus_EVENT_STATUS_DENIED:
		activity.Problems++
	}
	used := event.GetFinishedAt()
	if used == nil {
		used = event.GetStartedAt()
	}
	if used == nil {
		return
	}
	candidate := used.AsTime().UTC()
	current, _ := time.Parse(time.RFC3339, activity.LastUsed)
	if activity.LastUsed == "" || candidate.After(current) {
		activity.LastUsed = candidate.Format(time.RFC3339)
	}
}
