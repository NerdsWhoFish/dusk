// Package mcp serves the catalog to agents over the Model Context Protocol.
//
// The tool set is deliberately small and each tool is fat, per ADR-0010: a
// handful of tools fits in an agent's context, and answering the common
// question should cost one call rather than five.
//
// Reads answer in markdown rather than nested JSON, and every answer carries
// refs that feed straight back into get.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/FetchHQ/dusk/internal/index"
)

// Catalog is the slice of the index the tools need, declared here so the tools
// do not depend on how the graph is stored.
type Catalog interface {
	Search(ctx context.Context, gitRef, query string, limit int) ([]index.SearchResult, error)
	Get(ctx context.Context, gitRef, entityRef string) (*duskv1alpha1.Entity, error)
	Neighbors(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Relation, error)
	Dependents(ctx context.Context, gitRef, entityRef string, maxDepth int) ([]index.Dependent, error)
	List(ctx context.Context, gitRef, kind string) ([]*duskv1alpha1.Entity, error)
}

// Syncs reports what the controller last did, so an agent can tell a stale
// answer from an absent one.
type Syncs interface {
	Status() []SyncStatus
}

// SyncStatus is one repository's last reconcile.
type SyncStatus struct {
	Repository string
	Commit     string
	Entities   int
	Relations  int
	Error      string
}

// Options are the server's dependencies.
type Options struct {
	Catalog Catalog
	Syncs   Syncs
	Version string
}

// instructions is the portable half of ADR-0014's context injection: an
// interaction manual, not a knowledge dump, because it is spent on every
// session before any work happens.
const instructions = `Dusk is a catalog of this operator's systems: services, hosts, datastores, and how they connect.

Use it before guessing at infrastructure. If a question mentions a service, a host, a cluster, or "how do I", search here first.

- search finds entities by any word in their name, kind, title, or description.
- get returns everything known about one entity, including what it connects to.
- neighbors walks the graph outward from an entity.
- changes reports what Dusk last read from git, which is how you tell a stale answer from a missing one.

Every result carries a ref of the form kind:namespace/name. Refs feed straight back into get.

The catalog only knows what repositories have declared in a dusk.md. An entity being absent means nobody has written it down, not that it does not exist.`

// Server is the MCP surface over the catalog.
type Server struct {
	opts Options
}

// New builds the MCP server.
func New(opts Options) *Server { return &Server{opts: opts} }

// Handler serves the streamable HTTP transport.
func (s *Server) Handler() http.Handler {
	return sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s.sdkServer() }, nil)
}

func (s *Server) sdkServer() *sdk.Server {
	server := sdk.NewServer(
		&sdk.Implementation{
			Name:    "dusk",
			Title:   "Dusk",
			Version: s.opts.Version,
		},
		&sdk.ServerOptions{Instructions: instructions},
	)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "search",
		Description: "Search the catalog for entities by any word in their name, kind, title, or description. The place to start.",
	}, s.search)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "get",
		Description: "Everything known about one entity, including its relations. Takes a ref of the form kind:namespace/name.",
	}, s.get)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "neighbors",
		Description: "Walk the graph outward from an entity to find what depends on it.",
	}, s.neighbors)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "changes",
		Description: "What Dusk last read from git, per repository. Use it to tell a stale answer from a missing one.",
	}, s.changes)

	return server
}

type searchInput struct {
	Query string `json:"query" jsonschema:"words to search for"`
	Kind  string `json:"kind,omitempty" jsonschema:"restrict to one kind, such as service or host"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum results, default 25"`
}

func (s *Server) search(ctx context.Context, _ *sdk.CallToolRequest, in searchInput) (*sdk.CallToolResult, any, error) {
	if strings.TrimSpace(in.Query) == "" {
		return text("A query is required. Try a service name, a hostname, or a word from a description."), nil, nil
	}

	results, err := s.opts.Catalog.Search(ctx, "", in.Query, in.Limit)
	if err != nil {
		return nil, nil, err
	}

	var out strings.Builder
	shown := 0
	for _, hit := range results {
		if in.Kind != "" && !strings.EqualFold(hit.Kind, in.Kind) {
			continue
		}
		shown++
		fmt.Fprintf(&out, "- **%s** `%s`\n", displayName(hit.Title, hit.Ref), hit.Ref)
		if snippet := strings.TrimSpace(hit.Snippet); snippet != "" {
			fmt.Fprintf(&out, "  %s\n", singleLine(snippet))
		}
	}

	if shown == 0 {
		return text(fmt.Sprintf("Nothing in the catalog matches %q.\n\nThe catalog only holds what repositories have declared in a dusk.md, so this may mean nobody has written it down yet. `changes` shows what has been read.", in.Query)), nil, nil
	}
	return text(fmt.Sprintf("%d result(s) for %q. Pass a ref to `get` for the full picture.\n\n%s", shown, in.Query, out.String())), nil, nil
}

type getInput struct {
	Ref string `json:"ref" jsonschema:"entity ref, of the form kind:namespace/name"`
}

func (s *Server) get(ctx context.Context, _ *sdk.CallToolRequest, in getInput) (*sdk.CallToolResult, any, error) {
	entity, err := s.opts.Catalog.Get(ctx, "", in.Ref)
	// Only absence becomes a friendly answer. A storage failure reported as
	// "no such entity" would have the agent believe the thing does not exist.
	if errors.Is(err, index.ErrNotFound) {
		return text(fmt.Sprintf("No entity `%s` in the catalog. Try `search` for the name instead; refs are of the form kind:namespace/name.", in.Ref)), nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	relations, err := s.opts.Catalog.Neighbors(ctx, "", in.Ref)
	if err != nil {
		return nil, nil, err
	}
	return text(renderEntity(entity, relations)), nil, nil
}

type neighborsInput struct {
	Ref   string `json:"ref" jsonschema:"entity ref to walk out from"`
	Depth int    `json:"depth,omitempty" jsonschema:"how many hops to follow inbound, default 3"`
}

func (s *Server) neighbors(ctx context.Context, _ *sdk.CallToolRequest, in neighborsInput) (*sdk.CallToolResult, any, error) {
	depth := in.Depth
	if depth < 1 {
		depth = 3
	}

	relations, err := s.opts.Catalog.Neighbors(ctx, "", in.Ref)
	if err != nil {
		return nil, nil, err
	}
	dependents, err := s.opts.Catalog.Dependents(ctx, "", in.Ref, depth)
	if err != nil {
		return nil, nil, err
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# Around `%s`\n\n", in.Ref)
	out.WriteString(renderRelations(in.Ref, relations))

	if len(dependents) > 0 {
		fmt.Fprintf(&out, "\n## Reaches it within %d hop(s)\n\n", depth)
		for _, d := range dependents {
			fmt.Fprintf(&out, "- `%s` (%d hop(s) away)\n", d.Ref, d.Depth)
		}
		out.WriteString("\nThese break, or need checking, if it goes away.\n")
	}
	return text(out.String()), nil, nil
}

type changesInput struct{}

func (s *Server) changes(_ context.Context, _ *sdk.CallToolRequest, _ changesInput) (*sdk.CallToolResult, any, error) {
	if s.opts.Syncs == nil {
		return text("Sync status is not available in this deployment."), nil, nil
	}

	statuses := s.opts.Syncs.Status()
	if len(statuses) == 0 {
		return text("Dusk has not read any repository yet."), nil, nil
	}

	var declaring, failing, quiet int
	var out strings.Builder
	out.WriteString("# What Dusk last read\n\n")

	for _, status := range statuses {
		switch {
		case status.Error != "":
			failing++
			fmt.Fprintf(&out, "- **%s** failed: %s\n", status.Repository, singleLine(status.Error))
		case status.Entities > 0:
			declaring++
			fmt.Fprintf(&out, "- **%s** at `%s`: %d entities, %d relations\n",
				status.Repository, short(status.Commit), status.Entities, status.Relations)
		default:
			quiet++
		}
	}

	fmt.Fprintf(&out, "\n%d repository(s) declare entities, %d failed, and %d contain no dusk.md.\n",
		declaring, failing, quiet)
	return text(out.String()), nil, nil
}

func renderEntity(entity *duskv1alpha1.Entity, relations []*duskv1alpha1.Relation) string {
	var out strings.Builder

	fmt.Fprintf(&out, "# %s\n\n", displayName(entity.GetTitle(), entity.GetRef()))
	fmt.Fprintf(&out, "`%s` · kind **%s** · namespace **%s**\n\n", entity.GetRef(), entity.GetKind(), entity.GetNamespace())

	if description := strings.TrimSpace(entity.GetDescription()); description != "" {
		out.WriteString(description)
		out.WriteString("\n\n")
	}

	if fields := entity.GetAttributes().GetFields(); len(fields) > 0 {
		out.WriteString("## Attributes\n\n")
		for _, key := range sortedKeys(fields) {
			fmt.Fprintf(&out, "- **%s**: %s\n", key, fields[key].AsInterface())
		}
		out.WriteString("\n")
	}

	out.WriteString(renderRelations(entity.GetRef(), relations))

	if provenance := entity.GetProvenance(); provenance.GetVersion() != "" {
		fmt.Fprintf(&out, "\nDeclared in %s at `%s`.\n", provenance.GetSource(), short(provenance.GetVersion()))
	}
	return out.String()
}

func renderRelations(ref string, relations []*duskv1alpha1.Relation) string {
	if len(relations) == 0 {
		return "No relations are declared for it.\n"
	}

	var out strings.Builder
	out.WriteString("## Connections\n\n")
	for _, relation := range relations {
		if relation.GetFrom() == ref {
			fmt.Fprintf(&out, "- %s → `%s`\n", relation.GetType(), relation.GetTo())
			continue
		}
		fmt.Fprintf(&out, "- `%s` %s this\n", relation.GetFrom(), relation.GetType())
	}
	return out.String()
}

func text(body string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: body}}}
}

// displayName falls back to the ref, because an entity with no title still has
// to be nameable in a list.
func displayName(title, ref string) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	return ref
}

// sortedKeys keeps attribute order stable, because a map would otherwise
// reorder an entity's rendering on every call.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
