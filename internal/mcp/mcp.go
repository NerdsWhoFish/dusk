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
	"github.com/FetchHQ/dusk/internal/write"
	"github.com/FetchHQ/dusk/pkg/proof"
)

// Catalog is the slice of the index the tools need, declared here so the tools
// do not depend on how the graph is stored.
type Catalog interface {
	Search(ctx context.Context, gitRef, query string, limit int) ([]index.SearchResult, error)
	Get(ctx context.Context, gitRef, entityRef string) (*duskv1alpha1.Entity, error)
	Neighbors(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Relation, error)
	Dependents(ctx context.Context, gitRef, entityRef string, maxDepth int) ([]index.Dependent, error)
	List(ctx context.Context, gitRef, kind string) ([]*duskv1alpha1.Entity, error)
	NotesFor(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Note, error)
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

// Declarer performs writes. A nil one leaves the surface read-only, which
// ADR-0005 treats as a supported posture rather than a degraded one.
type Declarer interface {
	Declare(ctx context.Context, token string, declaration write.Declaration) (*write.Result, error)
	Record(ctx context.Context, token string, note write.Note) (*write.Result, error)

	// NoteDestination is where notes go, and empty means nowhere, in which case
	// the note tool is not offered at all.
	NoteDestination() string
}

// Options are the server's dependencies.
type Options struct {
	Catalog Catalog
	Syncs   Syncs
	Version string

	// Tokens issues the proof a write must present. Without it the read tools
	// still answer, but nothing can be written.
	Tokens *proof.Store
	Writer Declarer
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

	if s.opts.Writer != nil && s.opts.Tokens != nil {
		sdk.AddTool(server, &sdk.Tool{
			Name:        "declare",
			Description: "Create or update an entity. Requires the proof token from a read, which is why every read returns one.",
		}, s.declare)

		// A deployment with nowhere to put notes does not offer the tool at
		// all, which is a clearer answer than one that always refuses.
		if s.opts.Writer.NoteDestination() != "" {
			sdk.AddTool(server, &sdk.Tool{
				Name:        "note",
				Description: "Record something worth keeping that is not a description of any one thing: a gotcha, a runbook, the reason something is the way it is. Attach it to the entities it concerns. Omit id to write a new one.",
			}, s.note)
		}
	}

	return server
}

// issue mints a proof token for what a read returned and renders the line that
// tells an agent how to use it. Read-before-write is an unusual contract, so
// the token has to arrive unasked or nobody discovers it.
func (s *Server) issue(origin proof.Origin, seen map[string]string) string {
	if s.opts.Tokens == nil || s.opts.Writer == nil {
		return ""
	}
	token := s.opts.Tokens.Issue(origin, seen)
	tools := "`declare`"
	if s.opts.Writer.NoteDestination() != "" {
		tools = "`declare` or `note`"
	}
	return fmt.Sprintf("\n---\nProof token `%s`. Pass it to %s to write any of the above.\n", token.ID, tools)
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
	seen := map[string]string{}
	for _, hit := range results {
		if in.Kind != "" && !strings.EqualFold(hit.Kind, in.Kind) {
			continue
		}
		shown++
		seen[hit.Ref] = hit.Version
		fmt.Fprintf(&out, "- **%s** `%s`\n", displayName(hit.Title, hit.Ref), hit.Ref)
		if snippet := strings.TrimSpace(hit.Snippet); snippet != "" {
			fmt.Fprintf(&out, "  %s\n", singleLine(snippet))
		}
	}

	if shown == 0 {
		// A search that found nothing is exactly what authorizes creating, so
		// it still issues a token: the absence is the evidence.
		return text(fmt.Sprintf("Nothing in the catalog matches %q.\n\nThe catalog only holds what repositories have declared in a dusk.md, so this may mean nobody has written it down yet. `changes` shows what has been read.%s",
			in.Query, s.issue(proof.FromSearch, nil))), nil, nil
	}
	return text(fmt.Sprintf("%d result(s) for %q. Pass a ref to `get` for the full picture.\n\n%s%s",
		shown, in.Query, out.String(), s.issue(proof.FromSearch, seen))), nil, nil
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

	notes, err := s.opts.Catalog.NotesFor(ctx, "", in.Ref)
	if err != nil {
		return nil, nil, err
	}

	// The notes go in the token too, so replacing one needs proof of the read
	// that surfaced it, the same as changing the entity does.
	seen := map[string]string{in.Ref: entity.GetProvenance().GetVersion()}
	for _, note := range notes {
		seen[note.GetId()] = note.GetContentHash()
	}

	return text(renderEntity(entity, relations) + renderNotes(notes) + s.issue(proof.FromGet, seen)), nil, nil
}

// renderNotes lists what is attached to an entity. Notes are the half of the
// catalog a human wrote deliberately, so they come out whole rather than as
// links an agent would have to spend another call on.
func renderNotes(notes []*duskv1alpha1.Note) string {
	if len(notes) == 0 {
		return ""
	}

	var out strings.Builder
	out.WriteString("## Notes\n\n")
	for _, note := range notes {
		pin := ""
		if note.GetPinned() {
			pin = " · pinned"
		}
		fmt.Fprintf(&out, "**%s**%s · `%s`\n\n%s\n\n", note.GetKind(), pin, note.GetId(), strings.TrimSpace(note.GetBody()))
	}
	return out.String()
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

	// A traversal names refs without reading their contents, so the token it
	// issues covers only the entity it was asked about.
	seen := map[string]string{}
	if entity, err := s.opts.Catalog.Get(ctx, "", in.Ref); err == nil {
		seen[in.Ref] = entity.GetProvenance().GetVersion()
	}
	return text(out.String() + s.issue(proof.FromNeighbors, seen)), nil, nil
}

type declareInput struct {
	Ref         string            `json:"ref" jsonschema:"entity ref, of the form kind:namespace/name"`
	Proof       string            `json:"proof" jsonschema:"the proof token from the read that found it"`
	Title       string            `json:"title,omitempty" jsonschema:"human facing name"`
	Description string            `json:"description,omitempty" jsonschema:"markdown prose describing it, replacing what is there"`
	Attributes  map[string]string `json:"attributes,omitempty" jsonschema:"attributes to set, merged with the existing ones"`
	Repository  string            `json:"repository,omitempty" jsonschema:"owner/name to declare a new entity in, required only when creating"`
}

func (s *Server) declare(ctx context.Context, _ *sdk.CallToolRequest, in declareInput) (*sdk.CallToolResult, any, error) {
	result, err := s.opts.Writer.Declare(ctx, in.Proof, write.Declaration{
		Ref:         in.Ref,
		Title:       in.Title,
		Description: in.Description,
		Attributes:  in.Attributes,
		Repository:  in.Repository,
	})
	// A refused write is an answer, not a transport failure: the agent has to
	// read the reason and act on it.
	if err != nil {
		return text(fmt.Sprintf("The write was not made.\n\n%s", err)), nil, nil
	}

	verb := "Updated"
	if result.Created {
		verb = "Created"
	}
	return text(fmt.Sprintf(
		"%s `%s` in %s at `%s`.\n\nCommit: %s\n\nIt reaches the catalog on the next reconcile, which the push already triggered.",
		verb, result.Ref, result.Repository, result.Path, result.URL)), nil, nil
}

// noteInput leaves kind and body optional because an update merges over what
// the file says, so restating them would be a chance to get one wrong. A new
// note needs both, enforced by the write path rather than the schema.
type noteInput struct {
	Kind   string   `json:"kind,omitempty" jsonschema:"what sort of note: gotcha, runbook, howto, decision, incident, or todo. Required for a new note"`
	Body   string   `json:"body,omitempty" jsonschema:"the note itself, as markdown. Required for a new note; replaces the whole body when updating"`
	Refs   []string `json:"refs,omitempty" jsonschema:"entity refs this note is about, of the form kind:namespace/name"`
	Id     string   `json:"id,omitempty" jsonschema:"the path of an existing note to replace; omit to write a new one"`
	Proof  string   `json:"proof,omitempty" jsonschema:"the proof token from the read that found it, required only when replacing"`
	Pinned bool     `json:"pinned,omitempty" jsonschema:"keep this note at the top of what it attaches to"`
}

func (s *Server) note(ctx context.Context, _ *sdk.CallToolRequest, in noteInput) (*sdk.CallToolResult, any, error) {
	result, err := s.opts.Writer.Record(ctx, in.Proof, write.Note{
		Id:     in.Id,
		Kind:   in.Kind,
		Refs:   in.Refs,
		Body:   in.Body,
		Pinned: in.Pinned,
	})
	if err != nil {
		return text(fmt.Sprintf("The note was not written.\n\n%s", err)), nil, nil
	}

	verb := "Updated"
	if result.Created {
		verb = "Wrote"
	}
	return text(fmt.Sprintf(
		"%s the note at `%s` in %s.\n\nCommit: %s\n\nIts id is `%s`. Pass that as `id` to replace it rather than writing a second one.",
		verb, result.Path, result.Repository, result.URL, result.Path)), nil, nil
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
