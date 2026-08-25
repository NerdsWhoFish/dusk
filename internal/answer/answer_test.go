package answer_test

import (
	"context"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/answer"
	"github.com/NerdsWhoFish/dusk/internal/index"
)

type catalog struct {
	hits       []index.SearchResult
	graph      index.Graph
	visibility index.Visibility
	query      string
}

func (c *catalog) Search(_ context.Context, _ string, filter index.SearchFilter) ([]index.SearchResult, int, error) {
	c.query = filter.Query
	return c.hits, len(c.hits), nil
}

func (c *catalog) Graph(_ context.Context, _ string, visibility index.Visibility) (index.Graph, error) {
	c.visibility = visibility
	return c.graph, nil
}

type completer struct {
	model    string
	messages []answer.Message
	response string
}

func (c *completer) Complete(_ context.Context, model string, messages []answer.Message) (string, error) {
	c.model = model
	c.messages = messages
	return c.response, nil
}

func TestQuestionUsesConfiguredDefaultModel(t *testing.T) {
	service, provider, _ := fixture()

	result, err := service.Ask(t.Context(), "", index.Unrestricted(), "What do you know about Jellyfin?", "")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if provider.model != "model-a" || result.Model != "model-a" {
		t.Fatalf("model = %q, result model = %q", provider.model, result.Model)
	}
	if result.Answer != "Jellyfin runs on the NAS. [S1]" {
		t.Errorf("Answer = %q", result.Answer)
	}
}

func TestUnsupportedModelIsRejectedBeforeAProviderCall(t *testing.T) {
	service, provider, _ := fixture()
	_, err := service.Ask(t.Context(), "", index.Unrestricted(), "What is Jellyfin?", "not-allowed")
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("Ask error = %v", err)
	}
	if provider.model != "" {
		t.Fatalf("provider was called with %q", provider.model)
	}
}

// ADR-0081: the provider receives only the same catalog slice the current
// viewer may read, even when an unrestricted search ranks a hidden ref first.
func TestADR0081_AIContextContainsOnlyViewerVisibleCatalogData(t *testing.T) {
	service, provider, catalog := fixture()
	catalog.hits = append([]index.SearchResult{{
		Type: "entity", Ref: "service:secret/payroll", Kind: "service", Title: "Payroll",
	}}, catalog.hits...)
	visibility := index.Visibility{Repositories: []string{"example/visible"}}

	result, err := service.Ask(t.Context(), "", visibility, "Where is Jellyfin?", "model-b")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if catalog.visibility.Repositories[0] != "example/visible" {
		t.Fatalf("Graph visibility = %+v", catalog.visibility)
	}
	prompt := provider.messages[len(provider.messages)-1].Content
	if strings.Contains(prompt, "payroll") || strings.Contains(prompt, "Payroll") {
		t.Fatalf("hidden search result reached provider prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "service:home/jellyfin") || !strings.Contains(prompt, "host:home/nas") {
		t.Fatalf("visible entity and neighbor did not reach prompt:\n%s", prompt)
	}
	if len(result.Sources) != 3 {
		t.Fatalf("sources = %+v, want entity, note, and neighbor", result.Sources)
	}
	if provider.model != "model-b" {
		t.Errorf("provider model = %q", provider.model)
	}
}

func TestQuestionWordsAreRemovedFromCatalogRetrieval(t *testing.T) {
	service, _, catalog := fixture()
	if _, err := service.Ask(t.Context(), "", index.Unrestricted(), "What do you know about Jellyfin?", ""); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if catalog.query != "jellyfin" {
		t.Errorf("search query = %q, want jellyfin", catalog.query)
	}
}

func fixture() (*answer.Service, *completer, *catalog) {
	service := &duskv1alpha1.Entity{
		Ref: "service:home/jellyfin", Kind: "service", Namespace: "home", Name: "jellyfin",
		Title: "Jellyfin", Description: "Media server.",
	}
	host := &duskv1alpha1.Entity{
		Ref: "host:home/nas", Kind: "host", Namespace: "home", Name: "nas", Title: "NAS",
	}
	note := &duskv1alpha1.Note{
		Id: ".dusk/gotcha-storage.md", Kind: "gotcha", Body: "# Storage\nKeep the media volume mounted.",
	}
	catalog := &catalog{
		hits: []index.SearchResult{{Type: "entity", Ref: service.GetRef(), Kind: "service", Title: "Jellyfin"}},
		graph: index.Graph{
			Nodes:     []index.GraphNode{{Entity: service, Notes: []*duskv1alpha1.Note{note}}, {Entity: host}},
			Relations: []*duskv1alpha1.Relation{{From: service.GetRef(), To: host.GetRef(), Type: "runs_on"}},
		},
	}
	provider := &completer{response: "Jellyfin runs on the NAS. [S1]"}
	return &answer.Service{
		Catalog: catalog, Completer: provider, Models: []string{"model-a", "model-b"},
		DefaultModel: "model-a", Provider: "provider.example",
	}, provider, catalog
}
