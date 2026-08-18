package mcp_test

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/timestamppb"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/mcp"
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

	return serve(t, mcp.New(mcp.Options{Catalog: idx, Syncs: syncs, Version: "test"})), idx
}

// serve stands the real HTTP surface up for a configured server.
func serve(t *testing.T, server *mcp.Server) *sdk.ClientSession {
	t.Helper()

	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(t.Context(),
		&sdk.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// newIndex opens an empty index, for a server configured by hand.
func newIndex(t *testing.T) *index.DB {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func seed(t *testing.T, idx *index.DB) {
	t.Helper()

	entities := []*duskv1alpha1.Entity{
		entity("host:home/nas", "host", "The NAS", "Four bays, holds the media library."),
		entity("service:home/jellyfin", "service", "Jellyfin", "Media server. Transcoding is disabled on purpose."),
	}
	relations := []*duskv1alpha1.Relation{{
		From: "service:home/jellyfin", To: "host:home/nas", Type: "runs_on",
		Provenance: &duskv1alpha1.Provenance{Source: "dusk.md", Version: "abc1234def"},
	}}

	put(t, idx, "example/homelab", entities, relations...)
}

// put declares entities into one repository and makes that the default view,
// which is the ref every MCP read runs against.
func put(t *testing.T, idx *index.DB, repository string, entities []*duskv1alpha1.Entity, relations ...*duskv1alpha1.Relation) {
	t.Helper()
	ctx := t.Context()

	declarations := make([]index.Declaration, 0, len(entities))
	for _, e := range entities {
		declarations = append(declarations, index.Declaration{Path: e.GetName() + "/dusk.md", Entity: e})
	}
	if err := idx.Put(ctx, repository, mainRef, declarations, relations, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.SetDefaultView(ctx, repository, mainRef); err != nil {
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
		if tool.OutputSchema == nil {
			t.Errorf("tool %q has no output schema for its structured result", tool.Name)
		}
	}

	// Six reads, plus note and kinds, which read as well as write. Adding one
	// is a decision, not a default, and the failure ADR-0010 names is thirty
	// tools nobody chose.
	want := "changes,drift,dusk_context,get,kinds,neighbors,note,search"
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

	// An entity keeps both, because a title is a name and a ref is an
	// identifier. A note has neither twice.
	t.Run("an entity hit carries its title and its ref", func(t *testing.T) {
		body := call(t, session, "search", map[string]any{"query": "jellyfin"})
		if !strings.Contains(body, "**Jellyfin** `service:home/jellyfin`") {
			t.Errorf("an entity hit lost its title or its ref:\n%s", body)
		}
	})
}

// A note has no title, so the renderer fell back to the ref and printed the
// path as the name and again as the ref. One identifier, printed once.
func TestANoteHitPrintsItsPathOnce(t *testing.T) {
	session, idx := connect(t, nil)
	ideas(t, idx)

	body := call(t, session, "search", map[string]any{"query": "transcoding"})

	if n := strings.Count(body, ".dusk/transcoding.md"); n != 1 {
		t.Errorf("the note's path appears %d times, want once:\n%s", n, body)
	}
	if !strings.Contains(body, "gotcha · `.dusk/transcoding.md`") {
		t.Errorf("a note hit lost its kind or its ref:\n%s", body)
	}
}

// ADR-0059: a filter narrows the query. Applied to a page a limit already cut,
// a kind invents the one answer the surface must never invent, because the
// product teaches agents that absence means undocumented.
func TestADR0059_AKindFilterNarrowsTheQuery(t *testing.T) {
	session, idx := connect(t, nil)

	// Thirty short services outrank one long host on the same word, so the host
	// falls past any limit smaller than the number of services.
	entities := make([]*duskv1alpha1.Entity, 0, 31)
	for i := range 30 {
		entities = append(entities, entity(
			fmt.Sprintf("service:home/svc%02d", i), "service",
			fmt.Sprintf("Service %02d", i), "shelf"))
	}
	entities = append(entities, entity("host:home/rack", "host", "The Rack",
		"Four bays. "+strings.Repeat("It holds the media library. ", 40)+" shelf"))
	put(t, idx, "example/homelab", entities)

	body := call(t, session, "search", map[string]any{"query": "shelf", "kind": "host"})

	if !strings.Contains(body, "host:home/rack") {
		t.Errorf("a host that matches was not returned:\n%s", body)
	}
	if strings.Contains(body, "Nothing in the catalog matches") {
		t.Errorf("a match past the raw limit was reported as an absence:\n%s", body)
	}
	if strings.Contains(body, "service:home/svc") {
		t.Errorf("the kind did not exclude the services:\n%s", body)
	}
}

// ADR-0059: a list says how many matched, not only how many it is showing. An
// agent shown three of forty and told "3 result(s)" believes it has seen them.
func TestADR0059_ASearchSaysHowManyItIsNotShowing(t *testing.T) {
	session, idx := connect(t, nil)

	entities := make([]*duskv1alpha1.Entity, 0, 12)
	for i := range 12 {
		entities = append(entities, entity(
			fmt.Sprintf("service:home/svc%02d", i), "service",
			fmt.Sprintf("Service %02d", i), "shelf"))
	}
	put(t, idx, "example/homelab", entities)

	t.Run("a cut page says what it matched", func(t *testing.T) {
		body := call(t, session, "search", map[string]any{"query": "shelf", "limit": 3})
		if !strings.Contains(body, "1-3 of 12 result(s)") {
			t.Errorf("search body does not say how many matched:\n%s", body)
		}
		if !strings.Contains(body, "`offset` 3") {
			t.Errorf("search body does not say how to see the rest:\n%s", body)
		}
	})

	t.Run("the offset it names answers with the next page", func(t *testing.T) {
		body := call(t, session, "search", map[string]any{"query": "shelf", "limit": 3, "offset": 3})
		if !strings.Contains(body, "4-6 of 12 result(s)") {
			t.Errorf("search body does not report the page it answered with:\n%s", body)
		}
		if strings.Contains(body, "Service 00") {
			t.Errorf("offset did not skip the first page:\n%s", body)
		}
	})

	// The last page is still "N of M", because it was reached by paging, but
	// naming a further offset would send the caller past the end.
	t.Run("the last page does not name an offset past the end", func(t *testing.T) {
		body := call(t, session, "search", map[string]any{"query": "shelf", "limit": 6, "offset": 6})
		if !strings.Contains(body, "7-12 of 12 result(s)") {
			t.Errorf("search body does not report the final page:\n%s", body)
		}
		if strings.Contains(body, "`offset` 12") {
			t.Errorf("search body points past the end of the result:\n%s", body)
		}
	})

	t.Run("a whole page does not pretend to be cut", func(t *testing.T) {
		body := call(t, session, "search", map[string]any{"query": "shelf", "limit": 50})
		if !strings.Contains(body, "12 result(s)") || strings.Contains(body, " of 12") {
			t.Errorf("search body should not say it cut anything:\n%s", body)
		}
	})

	// An empty answer that does not repeat the kind reads as "nothing called
	// that exists", which is a stronger claim than the one that was checked.
	t.Run("an empty answer names the kind it looked for", func(t *testing.T) {
		body := call(t, session, "search", map[string]any{"query": "shelf", "kind": "datastore"})
		if !strings.Contains(body, "datastore") {
			t.Errorf("empty answer does not say what it looked for:\n%s", body)
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

// ADR-0059: a ref in a list carries the name of what it points at. Twenty-two
// bare refs make "which of these is worth opening" cost twenty-two calls, which
// is what ADR-0010's "refs feed back into get" was meant to avoid.
func TestADR0059_ARefInAListCarriesItsName(t *testing.T) {
	session, idx := connect(t, nil)
	seed(t, idx)

	// A relation whose target nothing declares still renders, because ADR-0033
	// treats an unresolvable ref as drift rather than as an error.
	put(t, idx, "example/board",
		[]*duskv1alpha1.Entity{entity("card:board/rewire", "card", "Rewire the rack", "")},
		&duskv1alpha1.Relation{
			From: "host:home/nas", To: "card:board/rewire", Type: "tracked_by",
			Provenance: &duskv1alpha1.Provenance{Source: "dusk.md", Version: "abc1234def"},
		},
		&duskv1alpha1.Relation{
			From: "host:home/nas", To: "card:board/gone", Type: "tracked_by",
			Provenance: &duskv1alpha1.Provenance{Source: "dusk.md", Version: "abc1234def"},
		})

	for _, tool := range []string{"get", "neighbors"} {
		t.Run(tool, func(t *testing.T) {
			body := call(t, session, tool, map[string]any{"ref": "host:home/nas"})
			if !strings.Contains(body, "**Rewire the rack** `card:board/rewire`") {
				t.Errorf("%s does not name what the ref points at:\n%s", tool, body)
			}
			if !strings.Contains(body, "`card:board/gone`") {
				t.Errorf("%s dropped a ref nothing declares:\n%s", tool, body)
			}
		})
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

// ADR-0059: a list never reports an absence it has not verified. `neighbors`
// answered "No relations are declared for it" for a ref nothing declares, which
// is what a leaf entity answers, so a typo reads as "nothing depends on it".
func TestADR0059_NeighborsNeverInventsAnEmptyNeighbourhood(t *testing.T) {
	session, idx := connect(t, nil)
	seed(t, idx)

	t.Run("a ref nothing declares says so and names the call that finds it", func(t *testing.T) {
		body := call(t, session, "neighbors", map[string]any{"ref": "service:example/does-not-exist"})

		if strings.Contains(body, "No relations are declared for it") {
			t.Errorf("a ref nothing declares answered as a leaf entity:\n%s", body)
		}
		if !strings.Contains(body, "search") {
			t.Errorf("the answer does not name the call that finds the right ref:\n%s", body)
		}
	})

	t.Run("a declared leaf still says it has no relations", func(t *testing.T) {
		put(t, idx, "example/leaf",
			[]*duskv1alpha1.Entity{entity("host:home/alone", "host", "Alone", "Nothing points at it.")})

		body := call(t, session, "neighbors", map[string]any{"ref": "host:home/alone"})
		if !strings.Contains(body, "No relations are declared for it") {
			t.Errorf("a real leaf lost the answer that says it is one:\n%s", body)
		}
	})

	// ADR-0033 makes a ref nothing declares drift rather than an error, so what
	// points at one is real and dropping it would be the same lie the other way.
	t.Run("what points at an undeclared ref is still reported", func(t *testing.T) {
		put(t, idx, "example/edge",
			[]*duskv1alpha1.Entity{entity("service:home/gateway", "service", "Gateway", "")},
			&duskv1alpha1.Relation{
				From: "service:home/gateway", To: "service:example/vanished", Type: "depends_on",
				Provenance: &duskv1alpha1.Provenance{Source: "dusk.md", Version: "abc1234def"},
			})

		body := call(t, session, "neighbors", map[string]any{"ref": "service:example/vanished"})
		if !strings.Contains(body, "service:home/gateway") {
			t.Errorf("a relation pointing at an undeclared ref was dropped:\n%s", body)
		}
		if !strings.Contains(body, "search") {
			t.Errorf("the answer does not say the ref itself is undeclared:\n%s", body)
		}
	})
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
	if len(instructions) > 700 {
		t.Fatalf("instructions cost %d bytes on every session, want at most 700:\n%s", len(instructions), instructions)
	}
}

func TestIdleSessionsExpire(t *testing.T) {
	session := serve(t, mcp.New(mcp.Options{
		Catalog: newIndex(t), Version: "test", SessionTimeout: 20 * time.Millisecond,
	}))
	time.Sleep(80 * time.Millisecond)
	if _, err := session.ListTools(t.Context(), nil); err == nil {
		t.Fatal("an idle MCP session remained usable after its timeout")
	}
}

func TestRelationOutputIsBounded(t *testing.T) {
	session, idx := connect(t, nil)
	root := entity("service:home/root", "service", "Root", "Has an unreasonable number of edges.")
	relations := make([]*duskv1alpha1.Relation, 0, 150)
	for i := range 150 {
		relations = append(relations, &duskv1alpha1.Relation{
			From: root.GetRef(), To: fmt.Sprintf("service:home/dependency-%03d", i), Type: "depends_on",
			Provenance: &duskv1alpha1.Provenance{Source: "dusk.md", Version: "abc1234def"},
		})
	}
	put(t, idx, "example/homelab", []*duskv1alpha1.Entity{root}, relations...)

	result, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "get", Arguments: map[string]any{"ref": root.GetRef()},
	})
	if err != nil {
		t.Fatalf("CallTool(get): %v", err)
	}
	body := result.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(body, "50 more connection(s) were omitted") {
		t.Fatalf("bounded result did not disclose truncation:\n%s", body)
	}
	if strings.Contains(body, "dependency-149") {
		t.Fatal("get rendered relations beyond the bound")
	}
	if result.StructuredContent == nil {
		t.Fatal("get returned markdown without compact structured content")
	}
}

func joinSorted(names []string) string {
	return strings.Join(slices.Sorted(slices.Values(names)), ",")
}
