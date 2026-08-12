package mcp_test

import (
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/timestamppb"

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/FetchHQ/dusk/internal/index"
	"github.com/FetchHQ/dusk/internal/mcp"
)

const mainRef = "refs/heads/main"

type fakeSyncs struct{ statuses []mcp.SyncStatus }

func (f fakeSyncs) Status() []mcp.SyncStatus { return f.statuses }

// connect stands the real HTTP surface up and returns a client session, so the
// tests exercise the wire an agent actually uses rather than the handlers.
func connect(t *testing.T, syncs mcp.Syncs) (*sdk.ClientSession, *index.DB) {
	t.Helper()

	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	server := mcp.New(mcp.Options{Catalog: idx, Syncs: syncs, Version: "test"})
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(t.Context(),
		&sdk.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, idx
}

func seed(t *testing.T, idx *index.DB) {
	t.Helper()
	ctx := t.Context()

	entities := []*duskv1alpha1.Entity{
		entity("host:home/nas", "host", "The NAS", "Four bays, holds the media library."),
		entity("service:home/jellyfin", "service", "Jellyfin", "Media server. Transcoding is disabled on purpose."),
	}
	relations := []*duskv1alpha1.Relation{{
		From: "service:home/jellyfin", To: "host:home/nas", Type: "runs_on",
		Provenance: &duskv1alpha1.Provenance{Source: "dusk.md", Version: "abc1234def"},
	}}

	if err := idx.Put(ctx, "example/homelab", mainRef, entities, relations); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(ctx, "example/homelab", mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}
}

func entity(ref, kind, title, description string) *duskv1alpha1.Entity {
	namespace, name, _ := strings.Cut(strings.SplitN(ref, ":", 2)[1], "/")
	return &duskv1alpha1.Entity{
		Ref: ref, Kind: kind, Namespace: namespace, Name: name,
		Title: title, Description: description,
		Provenance: &duskv1alpha1.Provenance{
			Source:     "dusk.md",
			Version:    "abc1234def",
			ObservedAt: timestamppb.New(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)),
		},
	}
}

func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) string {
	t.Helper()
	result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}

	var body strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(*sdk.TextContent); ok {
			body.WriteString(textContent.Text)
		}
	}
	if body.Len() == 0 {
		t.Fatalf("CallTool(%s) returned no text", name)
	}
	return body.String()
}

// The tool list is spent on every session before any work happens, so its size
// is a product constraint rather than an implementation detail (ADR-0010).
func TestToolSurfaceIsSmall(t *testing.T) {
	session, _ := connect(t, nil)

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("tool %q has no description, so an agent cannot tell when to use it", tool.Name)
		}
	}

	want := "changes,get,neighbors,search"
	if got := joinSorted(names); got != want {
		t.Errorf("tools = %s, want %s", got, want)
	}
}

func TestSearch(t *testing.T) {
	session, idx := connect(t, nil)
	seed(t, idx)

	t.Run("a word in a description finds the entity", func(t *testing.T) {
		body := call(t, session, "search", map[string]any{"query": "transcoding"})
		if !strings.Contains(body, "service:home/jellyfin") {
			t.Errorf("search body = %q, want the jellyfin ref", body)
		}
	})

	// Every read has to hand back refs that feed into get, or an agent has to
	// guess at identity (ADR-0010).
	t.Run("results carry refs that feed back into get", func(t *testing.T) {
		body := call(t, session, "search", map[string]any{"query": "nas"})
		ref := "host:home/nas"
		if !strings.Contains(body, ref) {
			t.Fatalf("search body = %q, want a ref", body)
		}
		if got := call(t, session, "get", map[string]any{"ref": ref}); !strings.Contains(got, "Four bays") {
			t.Errorf("the ref search returned did not resolve through get: %q", got)
		}
	})

	t.Run("kind narrows the results", func(t *testing.T) {
		body := call(t, session, "search", map[string]any{"query": "media", "kind": "host"})
		if strings.Contains(body, "service:home/jellyfin") {
			t.Errorf("kind filter did not exclude the service: %q", body)
		}
	})

	// An empty catalog and a missing entity are different answers, and an agent
	// that cannot tell them apart will invent one.
	t.Run("no match explains itself rather than returning nothing", func(t *testing.T) {
		body := call(t, session, "search", map[string]any{"query": "nonexistentthing"})
		if !strings.Contains(body, "dusk.md") {
			t.Errorf("empty result = %q, want it to explain that nothing declared it", body)
		}
	})

	t.Run("punctuation cannot break the query", func(t *testing.T) {
		for _, query := range []string{`"unbalanced`, "kind:service", "a AND (b"} {
			call(t, session, "search", map[string]any{"query": query})
		}
	})
}

func TestGet(t *testing.T) {
	session, idx := connect(t, nil)
	seed(t, idx)

	body := call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"})

	// get is deliberately fat: the common question should cost one call.
	for _, want := range []string{"Jellyfin", "Transcoding is disabled", "host:home/nas", "runs_on", "abc1234"} {
		if !strings.Contains(body, want) {
			t.Errorf("get body missing %q:\n%s", want, body)
		}
	}
}

func TestGetOnAMissingEntityIsActionable(t *testing.T) {
	session, idx := connect(t, nil)
	seed(t, idx)

	body := call(t, session, "get", map[string]any{"ref": "service:home/absent"})
	if !strings.Contains(body, "search") {
		t.Errorf("missing entity = %q, want it to point at the call that fixes it", body)
	}
}

func TestNeighbors(t *testing.T) {
	session, idx := connect(t, nil)
	seed(t, idx)

	body := call(t, session, "neighbors", map[string]any{"ref": "host:home/nas"})
	if !strings.Contains(body, "service:home/jellyfin") {
		t.Errorf("neighbors body = %q, want the dependent service", body)
	}
}

func TestChanges(t *testing.T) {
	session, _ := connect(t, fakeSyncs{statuses: []mcp.SyncStatus{
		{Repository: "example/homelab", Commit: "abc1234def", Entities: 2, Relations: 1},
		{Repository: "example/quiet"},
		{Repository: "example/broken", Error: "read dusk.md: boom"},
	}})

	body := call(t, session, "changes", nil)

	for _, want := range []string{"example/homelab", "abc1234", "example/broken", "boom"} {
		if !strings.Contains(body, want) {
			t.Errorf("changes body missing %q:\n%s", want, body)
		}
	}
	// A repository with no dusk.md is not a failure and must not read as one.
	if !strings.Contains(body, "1 failed") {
		t.Errorf("changes body should separate failures from quiet repositories:\n%s", body)
	}
}

// The instructions field is sent before the client has told the server anything
// about itself, so it has to be useful without knowing where it is.
func TestInstructionsAreServed(t *testing.T) {
	session, _ := connect(t, nil)

	instructions := session.InitializeResult().Instructions
	if instructions == "" {
		t.Fatal("no instructions were sent")
	}
	for _, want := range []string{"search", "get", "kind:namespace/name"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("instructions missing %q", want)
		}
	}
}

func joinSorted(names []string) string {
	return strings.Join(slices.Sorted(slices.Values(names)), ",")
}
