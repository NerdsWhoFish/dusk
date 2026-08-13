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

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"io/fs"

	"go.yaml.in/yaml/v3"

	"github.com/NerdsWhoFish/dusk/internal/page"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
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
	Integrity(ctx context.Context, gitRef string) ([]index.Problem, error)
	Drift(ctx context.Context, gitRef string) ([]index.Drift, error)
	Scopes(ctx context.Context) ([]index.Scope, error)
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

	// Home reads the declared portal page, and SetHome writes it. ADR-0013
	// made blocks queries so that curating a layout is writing queries, which
	// is work an agent is good at.
	Home(ctx context.Context) ([]byte, error)
	SetHome(ctx context.Context, token string, body []byte) (*write.Result, error)
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
- changes reports what Dusk last read from git and anything wrong with the result, which is how you tell a stale answer from a missing one and a confident answer from a correct one.
- drift reports where the catalog and reality disagree: declared and nowhere to be found, or running and written down nowhere.

- dusk_context returns this operator's inventory, tailored to the repository you are working in.

Every result carries a ref of the form kind:namespace/name. Refs feed straight back into get.

The catalog only knows what repositories have declared in a dusk.md, plus what an ingester observed by looking. An entity being absent means nobody has written it down, not that it does not exist.

**Call dusk_context once at the start of a session**, passing the directory you are working in. It tells you what this operator has before you need to ask, and it is the difference between answering from their catalog and answering from a guess.`

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

	// Freshness and soundness are one question, "how much should I trust what
	// you just told me", so they are one tool. ADR-0010 makes the size of the
	// tool list a product constraint: it is spent before any work happens.
	sdk.AddTool(server, &sdk.Tool{
		Name:        "changes",
		Description: "The state of the catalog: what Dusk last read from each repository, and anything wrong with the result. Use it to tell a stale answer from a missing one, and a confident answer from a correct one.",
	}, s.changes)

	// A fifth tool, and the only one that is a work queue rather than a
	// lookup. Folding it into changes would mix "can I trust this answer"
	// with "what should I go and do", which are different sessions.
	sdk.AddTool(server, &sdk.Tool{
		Name:        "drift",
		Description: "Where the catalog and reality disagree: declared but nowhere to be found, or running and written down nowhere. Ask before documenting an estate, and after something is decommissioned.",
	}, s.drift)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "dusk_context",
		Description: "What this operator's catalog knows, tailored to the repository you are working in. Call this once at the start of a session, before assuming anything about their infrastructure.",
	}, s.duskContext)

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

			// One tool for both halves rather than two: reading the page is how
			// an agent gets the proof token that lets it write one back, so
			// splitting them would make the read look optional.
			sdk.AddTool(server, &sdk.Tool{
				Name:        "page",
				Description: "Read or rewrite the homepage. Omit body to read it, which returns the current page and the proof token needed to change it. Pass body to replace it: markdown with YAML frontmatter declaring an ordered list of blocks, each a typed query. What you send is the whole page.",
			}, s.page)
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

// renderIntegrity appends what is wrong with the graph. It arrives unasked
// for, like the proof token does, because an agent that has to know to ask
// whether an answer is trustworthy will not ask.
func (s *Server) renderIntegrity(ctx context.Context, out *strings.Builder) error {
	problems, err := s.opts.Catalog.Integrity(ctx, "")
	if err != nil {
		return err
	}
	if len(problems) == 0 {
		out.WriteString("\nNothing is declared twice, and every relation and note points at something that exists.\n")
		return nil
	}

	fmt.Fprintf(out, "\n## %d problem(s) with the catalog itself\n\n", len(problems))
	for _, problem := range problems {
		fmt.Fprintf(out, "**`%s`** (%s)\n\n%s.\n", problem.Ref, problem.Kind, problem.Detail)
		for _, where := range problem.Where {
			fmt.Fprintf(out, "- %s\n", where)
		}
		out.WriteString("\n")
	}

	out.WriteString("These are not read failures. Each is a place the catalog answers confidently and may be wrong.\n")
	return nil
}

type driftInput struct{}

func (s *Server) drift(ctx context.Context, _ *sdk.CallToolRequest, _ driftInput) (*sdk.CallToolResult, any, error) {
	drifts, err := s.opts.Catalog.Drift(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	if len(drifts) == 0 {
		return text("Nothing has drifted. Everything declared is observed, and everything observed is declared.\n\nIf no ingester is configured there is nothing to compare against, and this answer means only that."), nil, nil
	}

	var missing, undeclared []index.Drift
	for _, drift := range drifts {
		if drift.Kind == index.DriftMissing {
			missing = append(missing, drift)
		} else {
			undeclared = append(undeclared, drift)
		}
	}

	var out strings.Builder
	out.WriteString("# Drift\n\n")

	if len(missing) > 0 {
		fmt.Fprintf(&out, "## %d declared and not found\n\n", len(missing))
		for _, drift := range missing {
			fmt.Fprintf(&out, "- `%s` %s, declared in %s\n",
				drift.Ref, displayName(drift.Title, ""), drift.Declared)
		}
		out.WriteString("\nEither these are gone and their declarations should be removed, or nothing is watching where they run.\n\n")
	}

	if len(undeclared) > 0 {
		fmt.Fprintf(&out, "## %d running and undeclared\n\n", len(undeclared))
		for _, drift := range undeclared {
			fmt.Fprintf(&out, "- `%s` %s, seen by %s\n",
				drift.Ref, displayName(drift.Title, ""), drift.Observed)
		}
		out.WriteString("\nThese exist and nobody has said what they are for. Declaring one is a `declare` call.\n")
	}

	return text(out.String()), nil, nil
}

type changesInput struct{}

func (s *Server) changes(ctx context.Context, _ *sdk.CallToolRequest, _ changesInput) (*sdk.CallToolResult, any, error) {
	var out strings.Builder

	// Soundness does not depend on sync status, so it is reported even where
	// there is none. Returning early here once meant a deployment without a
	// controller could never learn its catalog was broken.
	statuses := s.freshness(&out)

	var declaring, failing, quiet int
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

	if len(statuses) > 0 {
		fmt.Fprintf(&out, "\n%d repository(s) declare entities, %d failed, and %d contain no dusk.md.\n",
			declaring, failing, quiet)
	}

	if err := s.renderIntegrity(ctx, &out); err != nil {
		return nil, nil, err
	}
	return text(out.String()), nil, nil
}

// freshness writes the read-status half and returns what it found, so the
// caller can tell "nothing read" from "nothing to say about what was read".
func (s *Server) freshness(out *strings.Builder) []SyncStatus {
	if s.opts.Syncs == nil {
		out.WriteString("# The catalog\n\nSync status is not available in this deployment.\n")
		return nil
	}

	statuses := s.opts.Syncs.Status()
	if len(statuses) == 0 {
		out.WriteString("# The catalog\n\nDusk has not read any repository yet.\n")
		return nil
	}

	out.WriteString("# What Dusk last read\n\n")
	return statuses
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

// pageInput is one tool for both halves. No body reads; a body replaces.
type pageInput struct {
	Body  string `json:"body,omitempty" jsonschema:"the whole page as markdown with YAML frontmatter. Omit to read the current one instead of writing"`
	Proof string `json:"proof,omitempty" jsonschema:"the proof token from reading the page, required to change it"`
}

// page reads or rewrites the homepage.
//
// Reading issues a proof token like any other read, because a layout is worth
// looking at before replacing: what is sent becomes the whole page, and blocks
// are not merged with what was there.
func (s *Server) page(ctx context.Context, _ *sdk.CallToolRequest, in pageInput) (*sdk.CallToolResult, any, error) {
	if in.Body == "" {
		return s.readPage(ctx)
	}

	result, err := s.opts.Writer.SetHome(ctx, in.Proof, []byte(in.Body))
	if err != nil {
		return text(fmt.Sprintf("The page was not written.\n\n%s", err)), nil, nil
	}
	return text(fmt.Sprintf(
		"Rewrote `%s` in %s.\n\nCommit: %s\n\nThe homepage is now exactly what you sent.",
		result.Path, result.Repository, result.URL)), nil, nil
}

func (s *Server) readPage(ctx context.Context) (*sdk.CallToolResult, any, error) {
	body, err := s.opts.Writer.Home(ctx)
	if errors.Is(err, fs.ErrNotExist) {
		declared, _ := yaml.Marshal(page.Default())
		return text(fmt.Sprintf(
			"No page is declared, so Dusk serves its default. Writing one replaces it entirely.\n\n"+
				"The default, as it would be written:\n\n```yaml\n---\n%s---\n```\n\n%s",
			declared, s.issue(proof.FromPage, map[string]string{page.Path: ""}))), nil, nil
	}
	if err != nil {
		return text(fmt.Sprintf("The page could not be read.\n\n%s", err)), nil, nil
	}

	return text(fmt.Sprintf(
		"The homepage, as declared in `%s`:\n\n```markdown\n%s\n```\n\n%s",
		page.Path, body,
		s.issue(proof.FromPage, map[string]string{page.Path: duskmd.ContentHash(string(body))}))), nil, nil
}
