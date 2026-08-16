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
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"io/fs"

	"go.yaml.in/yaml/v3"

	"github.com/NerdsWhoFish/dusk/internal/page"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// Catalog is the slice of the index the tools need, declared here so the tools
// do not depend on how the graph is stored.
type Catalog interface {
	Search(ctx context.Context, gitRef string, filter index.SearchFilter) ([]index.SearchResult, int, error)
	Get(ctx context.Context, gitRef, entityRef string) (*duskv1alpha1.Entity, error)
	Neighbors(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Relation, error)
	Dependents(ctx context.Context, gitRef, entityRef string, maxDepth int) ([]index.Dependent, error)
	List(ctx context.Context, gitRef, kind string) ([]*duskv1alpha1.Entity, error)
	Declared(ctx context.Context, gitRef, repository string) ([]string, error)
	NotesFor(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Note, error)
	Notes(ctx context.Context, gitRef string, filter index.NoteFilter) ([]*duskv1alpha1.Note, error)
	Integrity(ctx context.Context, gitRef string, v index.Visibility) ([]index.Problem, error)
	Drift(ctx context.Context, gitRef string, filter index.DriftFilter, v index.Visibility) ([]index.Drift, error)
	Scopes(ctx context.Context) ([]index.Scope, error)
	LastRead(ctx context.Context) (map[string]time.Time, error)
	Vocabulary(ctx context.Context, gitRef string) ([]vocab.Kind, error)
}

// Syncs reports what the controller last did, so an agent can tell a stale
// answer from an absent one.
type Syncs interface {
	Status() []SyncStatus

	// NextSweep is when the poll floor next reads every repository, and zero
	// when nothing is scheduled.
	NextSweep() time.Time
}

// SyncStatus is one repository's last reconcile.
type SyncStatus struct {
	Repository string
	Commit     string
	Entities   int
	Relations  int
	Error      string

	// At is the last successful read and Attempted the last try of any kind.
	// They differ exactly when the last read failed, which is the difference
	// between an answer that is old and one that is old and not being fixed.
	At        time.Time
	Attempted time.Time
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

	// Vocabulary reads the minted kinds as committed, which is what a mint has
	// to prove it saw, and MintKind adds one.
	Vocabulary(ctx context.Context) (*write.Minted, error)
	MintKind(ctx context.Context, token string, kind vocab.Kind) (*write.Result, error)
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

	// Plugins is what an entity can have done to it. Optional: a deployment
	// with none offers no invoke tool rather than one that always refuses.
	Plugins Plugins

	// Now is what ages a read time. Injected so a test can assert on a phrase
	// rather than on whatever the machine's clock said.
	Now func() time.Time
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
- drift reports where the catalog and reality disagree: declared where something is watching and not observed there, or running and written down nowhere. It reports what it can check, never what it cannot see, so a row is a thing to look into rather than a thing to delete.
- invoke does something to an entity. What can be done is part of what get returns, so read a thing to learn what you can do to it. Anything that changes something needs the proof token from the read it names; anything destructive needs confirm; pass preview to see what would happen first.

- note reads and writes what has been written down. An **idea** is a note of kind idea: something somebody wants to keep, attached to what it is about or to nothing at all. Ask for them with note and no body, narrowed by kind, status and ref, and close one with status done or dropped.

- kinds lists the vocabulary: every kind in use, what it is for, and how many things carry it. Read it before inventing a kind, because kinds are open and nothing will stop you creating a second spelling of one that exists. Minting one with a role is what makes something act on it.

- dusk_context returns this operator's inventory and the notes they pinned, tailored to the repository you are working in. A note it names but does not print is one to ask note for.

Every result carries a ref of the form kind:namespace/name. Refs feed straight back into get.

The catalog only knows what repositories have declared in a dusk.md, plus what an ingester observed by looking. An entity being absent means nobody has written it down, not that it does not exist.

**Call dusk_context once at the start of a session**, passing the directory you are working in. It tells you what this operator has before you need to ask, and it is the difference between answering from their catalog and answering from a guess.`

// Server is the MCP surface over the catalog.
type Server struct {
	opts Options
}

// New builds the MCP server.
func New(opts Options) *Server { return &Server{opts: opts} }

func (s *Server) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

// viewer is what this surface may see. One shared bearer token carries no
// identity, so it is the whole catalog until there is a per-caller credential
// to derive one from (ADR-0012, ADR-0036).
func (s *Server) viewer() index.Visibility { return index.Unrestricted() }

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
		Description: "Everything known about one entity, including its relations and what can be done to it. Takes a ref of the form kind:namespace/name, or `plugin:name` to read a plugin and see what it can be asked to do without naming an entity.",
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
		Description: "What the catalog claims and reality does not support: declared where something is watching and not observed there, or a note pointing at a ref nothing holds. This is the maintenance queue, so ask after something is decommissioned. A row is not proof a thing is gone, and the answer says what else it can mean. Pass undeclared to also list what is running and written down nowhere.",
	}, s.drift)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "dusk_context",
		Description: "What this operator's catalog knows, tailored to the repository you are working in: what they pinned worth knowing before you start, what the repository declares, and what else they have. Call this once at the start of a session, before assuming anything about their infrastructure.",
	}, s.duskContext)

	// One tool, however many plugins are installed. A plugin's capability is an
	// action, not a tool, so the surface does not grow with the marketplace
	// (ADR-0041).
	if s.opts.Plugins != nil {
		sdk.AddTool(server, &sdk.Tool{
			Name:        "invoke",
			Description: "Do something to an entity, from the actions get listed for it. Anything that changes something needs the proof token from the read it names, and anything destructive needs confirm. Pass preview to see what would happen instead.",
		}, s.invoke)

		sdk.AddTool(server, &sdk.Tool{
			Name:        "configure",
			Description: "Read or set a plugin's configuration. Pass settings to change fields, which are merged over what is there; omit it to see the current values. Credentials are entered in Dusk's own interface and cannot be set here.",
		}, s.configure)
	}

	// Registered whether or not anything can be written, for the same reason
	// note is: knowing what kinds exist is what stops the next one being a
	// misspelling of one that does.
	sdk.AddTool(server, &sdk.Tool{
		Name:        "kinds",
		Description: "Read the catalog's vocabulary of kinds, or extend it. Every kind in use is listed with what it is for and how many things carry it. Pass mint with a namespace and a role to add one, against the proof token this read returns. Kinds are open, so declaring an unlisted one always works; minting is how a kind gets a meaning something acts on.",
	}, s.kinds)

	// Registered whether or not anything can be written: reading what has been
	// written down is a read, and a deployment with nowhere to put a new note
	// can still answer "what ideas do I have".
	sdk.AddTool(server, &sdk.Tool{
		Name:        "note",
		Description: "Read or record something worth keeping that is not a description of any one thing: a gotcha, a runbook, an idea, the reason something is the way it is. Pass a body to write one, attaching it to the entities it concerns, or nothing to read what is there, narrowed by kind, status and what it is about. An id on its own reads that one note and returns the token to replace it. An idea about nothing in particular needs no refs. Close one with status done or dropped.",
	}, s.note)

	if s.opts.Writer != nil && s.opts.Tokens != nil {
		sdk.AddTool(server, &sdk.Tool{
			Name:        "declare",
			Description: "Create or update an entity. Requires the proof token from a read, which is why every read returns one.",
		}, s.declare)

		// A deployment with nowhere to put notes still reads them, but the page
		// is both halves of one file and needs somewhere to write it.
		if s.opts.Writer.NoteDestination() != "" {
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

	results, total, err := s.opts.Catalog.Search(ctx, "", index.SearchFilter{
		Query: in.Query, Kind: in.Kind, Limit: in.Limit,
	})
	if err != nil {
		return nil, nil, err
	}

	var out strings.Builder
	seen := map[string]string{}
	for _, hit := range results {
		seen[hit.Ref] = hit.Version
		fmt.Fprintf(&out, "- %s**%s** `%s`\n", noteMark(hit), displayName(hit.Title, hit.Ref), hit.Ref)
		if snippet := strings.TrimSpace(hit.Snippet); snippet != "" {
			fmt.Fprintf(&out, "  %s\n", singleLine(snippet))
		}
	}

	if len(results) == 0 {
		// A search that found nothing is exactly what authorizes creating, so
		// it still issues a token: the absence is the evidence.
		return text(fmt.Sprintf("Nothing in the catalog matches %q%s.\n\nThe catalog only holds what repositories have declared in a dusk.md, so this may mean nobody has written it down yet. `changes` shows what has been read.%s",
			in.Query, ofKind(in.Kind), s.issue(proof.FromSearch, nil))), nil, nil
	}
	return text(fmt.Sprintf("%s for %q%s. Pass a ref to `get` for the full picture.\n\n%s%s",
		showing(len(results), total), in.Query, ofKind(in.Kind),
		out.String(), s.issue(proof.FromSearch, seen))), nil, nil
}

// showing says how many a list holds and how many matched, which ADR-0059
// makes the difference between a short answer and a wrong one.
func showing(shown, total int) string {
	if shown >= total {
		return fmt.Sprintf("%d result(s)", shown)
	}
	return fmt.Sprintf("%d of %d result(s), the highest ranked. Raise `limit` for more", shown, total)
}

// ofKind names the kind a search was narrowed to, so an empty answer says what
// it looked for rather than only that it found nothing.
func ofKind(kind string) string {
	if kind == "" {
		return ""
	}
	return " of kind " + kind
}

// noteMark labels a note hit with its kind. A note's ref is a file path, so
// without this a gotcha and a todo are indistinguishable in a result list, and
// ADR-0010 asks kind to affect rendering as well as ranking.
func noteMark(hit index.SearchResult) string {
	if hit.Type != "note" || hit.Kind == "" {
		return ""
	}
	return hit.Kind + " · "
}

type getInput struct {
	Ref string `json:"ref" jsonschema:"entity ref, of the form kind:namespace/name, or plugin:name to read a plugin"`
}

// getPlugin answers for a plugin, listing what it can be asked to do.
func (s *Server) getPlugin(id string) (*sdk.CallToolResult, any, error) {
	if s.opts.Plugins == nil {
		return text("Plugins are not enabled here, so there is nothing to read."), nil, nil
	}

	var installed []string
	for _, report := range s.opts.Plugins.Report() {
		installed = append(installed, report.ID)
		if report.ID != id {
			continue
		}

		var out strings.Builder
		fmt.Fprintf(&out, "# %s\n\nPlugin, version %s, %s.\n", report.ID, report.Version, runningWord(report))
		if actions := renderActions(s.opts.Plugins.PluginActions(id)); actions != "" {
			// renderActions already says to use invoke. What it cannot say is
			// that these take no ref, since it does not know it is a plugin.
			fmt.Fprintf(&out, "\n%s", strings.TrimSuffix(actions, "Run one with `invoke`.\n"))
			out.WriteString("Run one with `invoke`, naming this plugin and no ref.\n")
		} else {
			out.WriteString("\nIt offers nothing that can be run without an entity. Anything it does is listed on the things it observed.\n")
		}
		return text(out.String()), nil, nil
	}

	slices.Sort(installed)
	return text(fmt.Sprintf("No plugin `%s` is installed. These are: %s.",
		id, strings.Join(installed, ", "))), nil, nil
}

// runningWord says whether a plugin is up, which decides whether an action on
// it can be run at all. A plugin that is down says which kind of down, because
// "wait" and "somebody has to look at this" are different answers.
func runningWord(report plugin.Report) string {
	if !report.Running {
		return downWord(report.Process)
	}
	if report.Failing() {
		return "running but failing"
	}
	return "running"
}

// downWord describes a plugin that is not answering.
func downWord(process *plugin.Process) string {
	switch {
	case process == nil:
		return "not running"
	case process.Phase == plugin.PhaseRestarting:
		return fmt.Sprintf("not running, being started again after it exited (%s)", process.Exit)
	case process.Phase == plugin.PhaseFailed:
		return fmt.Sprintf("not running, and no longer being restarted after %d attempts (%s)",
			process.Attempts, process.Exit)
	default:
		return "not running"
	}
}

func (s *Server) get(ctx context.Context, _ *sdk.CallToolRequest, in getInput) (*sdk.CallToolResult, any, error) {
	// A bare `plugin:name` cannot collide with an entity ref, which always
	// carries a namespace. It is the only way to find an action about the
	// plugin rather than about one thing, since no entity lists those.
	if id, ok := strings.CutPrefix(in.Ref, "plugin:"); ok && !strings.Contains(id, "/") {
		return s.getPlugin(id)
	}

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

	// What can be done to a thing is part of the picture of that thing, and get
	// is deliberately fat for exactly this reason (ADR-0041).
	actions := ""
	if s.opts.Plugins != nil {
		actions = renderActions(s.opts.Plugins.Actions(entity.GetKind()))
	}

	return text(renderEntity(entity, relations) + renderNotes(notes) + actions + s.issue(proof.FromGet, seen)), nil, nil
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
		out.WriteString(renderNote(note))
	}
	return out.String()
}

// renderNote is one note whole. A closed one is still shown, because "I already
// had that idea and dropped it" is the answer somebody most needs and least
// expects.
func renderNote(note *duskv1alpha1.Note) string {
	marks := ""
	if note.GetPinned() {
		marks += " · pinned"
	}
	if status := note.GetStatus(); status != "" && status != duskmd.StatusOpen {
		marks += " · " + status
	}
	return fmt.Sprintf("**%s**%s · `%s`\n\n%s\n\n",
		note.GetKind(), marks, note.GetId(), strings.TrimSpace(note.GetBody()))
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
	if result.Proposed {
		return text(proposal(result)), nil, nil
	}

	verb := "Updated"
	if result.Created {
		verb = "Created"
	}

	// A kind already in the catalog resolves and says nothing, so this only
	// speaks when the declaration is what introduced the kind.
	kind, _, _ := strings.Cut(in.Ref, ":")
	warning := s.warnAboutKind(ctx, vocab.Entity, kind)

	return text(fmt.Sprintf(
		"%s `%s` in %s at `%s`.\n\nCommit: %s\n\nIt reaches the catalog on the next reconcile, which the push already triggered.%s",
		verb, result.Ref, result.Repository, result.Path, result.URL, warning)), nil, nil
}

// nothingToWrite reports that a call asked for notes rather than changing one.
// With no id, kind and status are read filters. With one they are changes, so
// an id alone is a read: the call a refused write against a note names.
func nothingToWrite(in noteInput) bool {
	if in.Id == "" {
		return strings.TrimSpace(in.Body) == ""
	}
	return strings.TrimSpace(in.Body) == "" && strings.TrimSpace(in.Kind) == "" &&
		strings.TrimSpace(in.Status) == "" && in.Refs == nil && !in.Pinned
}

// noteInput leaves kind and body optional because an update merges over what
// the file says, so restating them would be a chance to get one wrong. A new
// note needs both, enforced by the write path rather than the schema.
type noteInput struct {
	Kind   string   `json:"kind,omitempty" jsonschema:"what sort of note: gotcha, runbook, howto, decision, incident, todo, or idea. Required for a new note, and a filter when reading"`
	Body   string   `json:"body,omitempty" jsonschema:"the note itself, as markdown. Required for a new note; replaces the whole body when updating"`
	Refs   []string `json:"refs,omitempty" jsonschema:"entity refs this note is about, of the form kind:namespace/name. An idea about nothing in particular needs none"`
	Id     string   `json:"id,omitempty" jsonschema:"the path of an existing note. Alone it reads that note and returns the token to replace it; with something to change it replaces it. Omit to write a new one"`
	Proof  string   `json:"proof,omitempty" jsonschema:"the proof token from the read that found it, required only when replacing"`
	Pinned bool     `json:"pinned,omitempty" jsonschema:"keep this note at the top of what it attaches to"`

	Status string `json:"status,omitempty" jsonschema:"for a note that is work: open, done or dropped. A filter when reading, and what closes one when writing"`
	Ref    string `json:"ref,omitempty" jsonschema:"when reading, limit to notes about this entity"`
	Limit  int    `json:"limit,omitempty" jsonschema:"when reading, how many to return"`
}

// readNotes answers what has been written down, narrowed by kind, status and
// what it is about. The token it issues is what closing one of them needs, so
// asking "my open ideas" and then finishing one is two calls, not four.
func (s *Server) readNotes(ctx context.Context, in noteInput) (*sdk.CallToolResult, any, error) {
	notes, err := s.opts.Catalog.Notes(ctx, "", index.NoteFilter{
		Id: in.Id, Kind: in.Kind, Status: in.Status, Ref: in.Ref, Limit: in.Limit,
	})
	if err != nil {
		return nil, nil, err
	}

	seen := make(map[string]string, len(notes))
	for _, note := range notes {
		seen[note.GetId()] = note.GetContentHash()
	}

	if len(notes) == 0 {
		return text(describeNothing(in) + s.issue(proof.FromNote, seen)), nil, nil
	}
	return text(renderNotes(notes) + s.issue(proof.FromNote, seen)), nil, nil
}

func describeNothing(in noteInput) string {
	if in.Id != "" {
		return fmt.Sprintf("No note at `%s`. `search` finds one by what it says, and a note's id is its path.\n", in.Id)
	}

	said := "No notes"
	if in.Status != "" {
		said += " that are " + in.Status
	}
	if in.Kind != "" {
		said += " of kind " + in.Kind
	}
	if in.Ref != "" {
		said += " about `" + in.Ref + "`"
	}
	return said + ". Write one by passing a kind and a body.\n"
}

// note reads and writes, for the same reason page does: the read is what yields
// the proof token the write needs. Passing nothing to write with asks what is
// there, with an id or without.
func (s *Server) note(ctx context.Context, _ *sdk.CallToolRequest, in noteInput) (*sdk.CallToolResult, any, error) {
	if nothingToWrite(in) {
		return s.readNotes(ctx, in)
	}

	// Reading works with nowhere to write, so the refusal belongs here rather
	// than in whether the tool exists at all.
	if s.opts.Writer == nil || s.opts.Writer.NoteDestination() == "" {
		return text(write.ErrNoConfigRepository.Error()), nil, nil
	}

	result, err := s.opts.Writer.Record(ctx, in.Proof, write.Note{
		Id:     in.Id,
		Kind:   in.Kind,
		Refs:   in.Refs,
		Body:   in.Body,
		Pinned: in.Pinned,
		Status: in.Status,
	})
	if err != nil {
		return text(fmt.Sprintf("The note was not written.\n\n%s", err)), nil, nil
	}
	if result.Proposed {
		return text(proposal(result)), nil, nil
	}
	if result.Existing {
		return text(renderExisting(result)), nil, nil
	}

	verb := "Updated"
	if result.Created {
		verb = "Wrote"
	}
	return text(fmt.Sprintf(
		"%s the note at `%s` in %s.\n\nCommit: %s\n\nIts id is `%s`. Pass that as `id` to replace it rather than writing a second one.%s",
		verb, result.Path, result.Repository, result.URL, result.Path,
		renderSimilar(result.Similar)+s.warnAboutKind(ctx, vocab.Note, in.Kind))), nil, nil
}

// renderIntegrity appends what is wrong with the graph. It arrives unasked
// for, like the proof token does, because an agent that has to know to ask
// whether an answer is trustworthy will not ask.
func (s *Server) renderIntegrity(ctx context.Context, out *strings.Builder) error {
	problems, err := s.opts.Catalog.Integrity(ctx, "", s.viewer())
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

type changesInput struct{}

func (s *Server) changes(ctx context.Context, _ *sdk.CallToolRequest, _ changesInput) (*sdk.CallToolResult, any, error) {
	var out strings.Builder
	now := s.now()

	// The durable half of every time in this answer. Asked once, because both
	// repositories and ingesters are dated from it.
	lastRead, err := s.opts.Catalog.LastRead(ctx)
	if err != nil {
		return nil, nil, err
	}

	// Soundness does not depend on sync status, so it is reported even where
	// there is none. Returning early here once meant a deployment without a
	// controller could never learn its catalog was broken.
	statuses := s.freshness(&out)
	renderReads(&out, now, statuses, lastRead)
	if len(statuses) > 0 {
		renderSweep(&out, now, s.opts.Syncs.NextSweep())
	}

	if err := s.renderIntegrity(ctx, &out); err != nil {
		return nil, nil, err
	}

	// An observation nothing refreshes is a stale answer with no marker on it,
	// so how much to trust the catalog has to include whether its plugins work.
	if s.opts.Plugins != nil {
		out.WriteString(renderPlugins(s.opts.Plugins.Report(), now, lastRead))
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
	if result.Proposed {
		return text(proposal(result)), nil, nil
	}
	return text(fmt.Sprintf(
		"Rewrote `%s` in %s.\n\nCommit: %s\n\nThe homepage is now exactly what you sent.",
		result.Path, result.Repository, result.URL)), nil, nil
}

func (s *Server) readPage(ctx context.Context) (*sdk.CallToolResult, any, error) {
	body, err := s.opts.Writer.Home(ctx)
	if errors.Is(err, fs.ErrNotExist) {
		declared, _ := yaml.Marshal(page.Default())
		// Nothing seen, because there is nothing there: this read is the one
		// that witnesses the absence declaring the first page needs.
		return text(fmt.Sprintf(
			"No page is declared, so Dusk serves its default. Writing one replaces it entirely.\n\n"+
				"The default, as it would be written:\n\n```yaml\n---\n%s---\n```\n\n%s",
			declared, s.issue(proof.FromPage, nil))), nil, nil
	}
	if err != nil {
		return text(fmt.Sprintf("The page could not be read.\n\n%s", err)), nil, nil
	}

	return text(fmt.Sprintf(
		"The homepage, as declared in `%s`:\n\n```markdown\n%s\n```\n\n%s",
		page.Path, body,
		s.issue(proof.FromPage, map[string]string{page.Path: duskmd.ContentHash(string(body))}))), nil, nil
}
