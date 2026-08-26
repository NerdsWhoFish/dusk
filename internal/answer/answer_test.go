package answer_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/answer"
	"github.com/NerdsWhoFish/dusk/internal/index"
)

type catalog struct {
	hits       map[string][]index.SearchResult
	graph      index.Graph
	visibility index.Visibility
	queries    []string
}

func (c *catalog) Search(_ context.Context, _ string, filter index.SearchFilter) ([]index.SearchResult, int, error) {
	c.queries = append(c.queries, filter.Query)
	hits := c.hits[filter.Query]
	return hits, len(hits), nil
}

func (c *catalog) Graph(_ context.Context, _ string, visibility index.Visibility) (index.Graph, error) {
	c.visibility = visibility
	return c.graph, nil
}

type scriptedCompleter struct {
	replies []answer.Completion
	calls   [][]answer.Message
	tools   [][]answer.ToolDefinition
	model   string
}

func (c *scriptedCompleter) Complete(_ context.Context, model string, messages []answer.Message, tools []answer.ToolDefinition) (answer.Completion, error) {
	c.model = model
	c.calls = append(c.calls, append([]answer.Message(nil), messages...))
	c.tools = append(c.tools, append([]answer.ToolDefinition(nil), tools...))
	if len(c.replies) == 0 {
		return answer.Completion{}, fmt.Errorf("no scripted reply")
	}
	reply := c.replies[0]
	c.replies = c.replies[1:]
	return reply, nil
}

func TestQuestionUsesConfiguredDefaultModel(t *testing.T) {
	service, provider, _ := fixture(
		tool("search", "search_estate", `{"query":"Jellyfin"}`),
		tool("read", "read_document", `{"id":"service:home/jellyfin"}`),
		answer.Completion{Content: "Jellyfin is the media server. [S1]"},
	)

	result, err := service.Ask(t.Context(), "", index.Unrestricted(), "What is Jellyfin?", "")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if provider.model != "model-a" || result.Model != "model-a" {
		t.Fatalf("model = %q, result model = %q", provider.model, result.Model)
	}
	if result.Answer != "Jellyfin is the media server. [S1]" {
		t.Errorf("answer = %q", result.Answer)
	}
	if len(result.Sources) != 1 || result.Sources[0].Ref != "service:home/jellyfin" {
		t.Fatalf("sources = %+v", result.Sources)
	}
	if len(result.Searches) != 1 || result.Searches[0] != "Jellyfin" {
		t.Fatalf("searches = %+v", result.Searches)
	}
}

func TestUnsupportedModelIsRejectedBeforeAProviderCall(t *testing.T) {
	service, provider, _ := fixture()
	_, err := service.Ask(t.Context(), "", index.Unrestricted(), "What is Jellyfin?", "not-allowed")
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("Ask error = %v", err)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("provider calls = %d", len(provider.calls))
	}
}

func TestADR0082_AgentMaySearchAgainAndDisclosesEveryDocumentRead(t *testing.T) {
	grafana := &duskv1alpha1.Entity{
		Ref: "service:stout/grafana-cloud", Kind: "service", Namespace: "stout",
		Name: "grafana-cloud", Title: "Grafana Cloud", Description: "The current OpenTelemetry backend.",
	}
	decision := &duskv1alpha1.Note{
		Id: ".dusk/decision-grafana.md", Kind: "decision",
		Body: "# Use Grafana Cloud\nTracing is live through Grafana Alloy.",
	}
	catalog := &catalog{
		hits: map[string][]index.SearchResult{
			"tracing": {{Type: "note", Ref: ".dusk/gotcha-old.md", Kind: "gotcha"}},
			"OpenTelemetry": {
				{Type: "entity", Ref: grafana.GetRef(), Kind: grafana.GetKind(), Title: grafana.GetTitle()},
				{Type: "note", Ref: decision.GetId(), Kind: decision.GetKind()},
			},
		},
		graph: index.Graph{Nodes: []index.GraphNode{{Entity: grafana, Notes: []*duskv1alpha1.Note{decision}}}},
	}
	provider := &scriptedCompleter{replies: []answer.Completion{
		tool("s1", "search_estate", `{"query":"tracing"}`),
		tool("s2", "search_estate", `{"query":"OpenTelemetry"}`),
		tool("r1", "read_document", `{"id":"service:stout/grafana-cloud"}`),
		tool("r2", "read_document", `{"id":".dusk/decision-grafana.md"}`),
		{Content: "Tracing uses Grafana Cloud and Alloy. [S1] [S2]"},
	}}
	service := &answer.Service{
		Catalog: catalog, Completer: provider, Models: []string{"model-a"}, DefaultModel: "model-a",
	}

	result, err := service.Ask(t.Context(), "", index.Unrestricted(), "What do you know about tracing?", "")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got := strings.Join(result.Searches, ","); got != "tracing,OpenTelemetry" {
		t.Errorf("searches = %q", got)
	}
	if len(result.Sources) != 2 || result.Sources[0].Ref != grafana.GetRef() || result.Sources[1].Ref != decision.GetId() {
		t.Fatalf("sources = %+v", result.Sources)
	}
	last := provider.calls[len(provider.calls)-1]
	joined := messages(last)
	if !strings.Contains(joined, "[S1] ENTITY service:stout/grafana-cloud") ||
		!strings.Contains(joined, "[S2] NOTE .dusk/decision-grafana.md") {
		t.Fatalf("read documents did not reach provider:\n%s", joined)
	}
}

func TestADR0082_HiddenSearchResultsAndDocumentsNeverReachProvider(t *testing.T) {
	visible := &duskv1alpha1.Entity{
		Ref: "service:visible/app", Kind: "service", Namespace: "visible", Name: "app", Title: "Visible App",
	}
	catalog := &catalog{
		hits: map[string][]index.SearchResult{
			"payroll": {{Type: "entity", Ref: "service:secret/payroll", Kind: "service", Title: "Payroll"}},
		},
		graph: index.Graph{Nodes: []index.GraphNode{{Entity: visible}}},
	}
	provider := &scriptedCompleter{replies: []answer.Completion{
		tool("s1", "search_estate", `{"query":"payroll"}`),
		tool("r1", "read_document", `{"id":"service:secret/payroll"}`),
		{Content: "The visible estate did not provide a payroll document."},
	}}
	service := &answer.Service{Catalog: catalog, Completer: provider, Models: []string{"model-a"}, DefaultModel: "model-a"}
	visibility := index.Visibility{Repositories: []string{"example/visible"}}

	result, err := service.Ask(t.Context(), "", visibility, "What is payroll?", "")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(result.Sources) != 0 {
		t.Fatalf("hidden source was disclosed: %+v", result.Sources)
	}
	searchOutput := messages(provider.calls[1])
	if strings.Contains(searchOutput, "Payroll") || strings.Contains(searchOutput, "catalog_matches_before_visibility") ||
		strings.Contains(messages(provider.calls[2]), "Kind: service") {
		t.Fatal("hidden catalog content reached the provider")
	}
	if len(catalog.visibility.Repositories) != 1 || catalog.visibility.Repositories[0] != "example/visible" {
		t.Fatalf("visibility = %+v", catalog.visibility)
	}
}

func TestADR0082_OnlyReadOnlyEstateToolsAreOffered(t *testing.T) {
	service, provider, _ := fixture(
		tool("s1", "search_estate", `{"query":"missing"}`),
		answer.Completion{Content: "Nothing matched the visible estate."},
	)
	if _, err := service.Ask(t.Context(), "", index.Unrestricted(), "What is missing?", ""); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(provider.tools) == 0 || len(provider.tools[0]) != 2 {
		t.Fatalf("tools = %+v", provider.tools)
	}
	if provider.tools[0][0].Function.Name != "search_estate" || provider.tools[0][1].Function.Name != "read_document" {
		t.Fatalf("unexpected tools = %+v", provider.tools[0])
	}
}

func TestAnswerWithoutResearchIsSentBackToUseTheTools(t *testing.T) {
	service, provider, _ := fixture(
		answer.Completion{Content: "I think it is missing."},
		tool("s1", "search_estate", `{"query":"missing"}`),
		answer.Completion{Content: "Nothing matched the visible estate."},
	)
	if _, err := service.Ask(t.Context(), "", index.Unrestricted(), "What is missing?", ""); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(provider.calls) != 3 {
		t.Fatalf("provider calls = %d", len(provider.calls))
	}
	if !strings.Contains(messages(provider.calls[1]), "answered without searching") {
		t.Fatal("provider was not told to research before answering")
	}
}

func fixture(replies ...answer.Completion) (*answer.Service, *scriptedCompleter, *catalog) {
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
		hits: map[string][]index.SearchResult{
			"Jellyfin": {{Type: "entity", Ref: service.GetRef(), Kind: service.GetKind(), Title: service.GetTitle()}},
		},
		graph: index.Graph{
			Nodes:     []index.GraphNode{{Entity: service, Notes: []*duskv1alpha1.Note{note}}, {Entity: host}},
			Relations: []*duskv1alpha1.Relation{{From: service.GetRef(), To: host.GetRef(), Type: "runs_on"}},
		},
	}
	provider := &scriptedCompleter{replies: replies}
	return &answer.Service{
		Catalog: catalog, Completer: provider, Models: []string{"model-a", "model-b"},
		DefaultModel: "model-a", Provider: "provider.example",
	}, provider, catalog
}

func tool(id, name, arguments string) answer.Completion {
	return answer.Completion{ToolCalls: []answer.ToolCall{{
		ID: id, Type: "function", Function: answer.FunctionCall{Name: name, Arguments: arguments},
	}}}
}

func messages(messages []answer.Message) string {
	var out strings.Builder
	for _, message := range messages {
		out.WriteString(message.Content)
		out.WriteByte('\n')
	}
	return out.String()
}
