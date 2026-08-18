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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/structpb"

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
	GetFrom(ctx context.Context, gitRef, entityRef, repository string) (*duskv1alpha1.Entity, error)
	Neighbors(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Relation, error)
	Titles(ctx context.Context, gitRef string, refs []string) (map[string]string, error)
	Dependents(ctx context.Context, gitRef, entityRef string, maxDepth int) ([]index.Dependent, error)
	List(ctx context.Context, gitRef, kind string) ([]*duskv1alpha1.Entity, error)
	Declared(ctx context.Context, gitRef, repository string) ([]string, error)
	NotesFor(ctx context.Context, gitRef, entityRef string) ([]*duskv1alpha1.Note, error)
	Notes(ctx context.Context, gitRef string, filter index.NoteFilter) ([]*duskv1alpha1.Note, error)
	CountNotes(ctx context.Context, gitRef string, filter index.NoteFilter) (int, error)
	Integrity(ctx context.Context, gitRef string, v index.Visibility) ([]index.Problem, error)
	Drift(ctx context.Context, gitRef string, filter index.DriftFilter, v index.Visibility) ([]index.Drift, error)
	Scopes(ctx context.Context) ([]index.Scope, error)
	LastRead(ctx context.Context) (map[string]time.Time, error)
	Vocabulary(ctx context.Context, gitRef string) ([]vocab.Kind, error)
	ResolveRepository(ctx context.Context, candidate string) (string, error)
	Context(ctx context.Context, gitRef, repository string) ([]byte, error)
	Sources(ctx context.Context, gitRef, entityRef string) ([]index.EntitySource, error)
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

	// Relate declares an edge in the file of the entity it points from, which
	// is the only direction a repository may assert (ADR-0026).
	Relate(ctx context.Context, token string, relation write.Relation) (*write.Result, error)

	// NoteDestination is where notes go, and empty means nowhere. The tool is
	// still offered, because reading what is written down is a read; only the
	// write refuses, and the token offer stops naming `note`.
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

	// SessionTimeout closes idle streamable HTTP sessions. Zero uses 30 minutes.
	SessionTimeout time.Duration
}

// instructions is the portable half of ADR-0014's context injection: an
// interaction manual, not a knowledge dump, because it is spent on every
// session before any work happens.
const instructions = `Dusk is the internal developer platform, catalog and operational memory shared by one homelabber and their agents.

Call dusk_context once at the start of a session, passing the current owner/name repository when known. It carries what is pinned worth knowing, what this operator has, and the manual for every tool here, so it is the call that makes the rest usable.

Until then: search finds anything by a word in it, and its kind:namespace/name refs feed get. Knowledge of every sort is a note told apart only by kind, so a decision, an incident and a runbook all go through note rather than a tool of their own. Absence means nobody declared or observed something, never that it cannot exist.`

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
	timeout := s.opts.SessionTimeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	return sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return s.sdkServer() },
		&sdk.StreamableHTTPOptions{SessionTimeout: timeout},
	)
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

	sdk.AddTool(server, resultTool("search", "Search the catalog for entities by any word in their name, kind, title, or description. The place to start."), s.search)

	sdk.AddTool(server, resultTool("get", "Everything known about one entity, including its relations and what can be done to it. Takes a ref of the form kind:namespace/name, or `plugin:name` to read a plugin and see what it can be asked to do without naming an entity."), s.get)

	sdk.AddTool(server, resultTool("neighbors", "Walk the graph outward from an entity to find what depends on it."), s.neighbors)

	// Freshness and soundness are one question, "how much should I trust what
	// you just told me", so they are one tool. ADR-0010 makes the size of the
	// tool list a product constraint: it is spent before any work happens.
	sdk.AddTool(server, resultTool("changes", "The state of the catalog: what Dusk last read from each repository, and anything wrong with the result. Use it to tell a stale answer from a missing one, and a confident answer from a correct one."), s.changes)

	// A fifth tool, and the only one that is a work queue rather than a
	// lookup. Folding it into changes would mix "can I trust this answer"
	// with "what should I go and do", which are different sessions.
	sdk.AddTool(server, resultTool("drift", "What the catalog claims and reality does not support: declared where something is watching and not observed there, or a note pointing at a ref nothing holds. This is the maintenance queue, so ask after something is decommissioned. A row is not proof a thing is gone, and the answer says what else it can mean. Pass undeclared to also list what is running and written down nowhere."), s.drift)

	sdk.AddTool(server, resultTool("dusk_context", "What this operator's catalog knows, tailored to the repository you are working in: what they pinned worth knowing before you start, what the repository declares, and what else they have. Call this once at the start of a session, before assuming anything about their infrastructure."), s.duskContext)

	// One tool, however many plugins are installed. A plugin's capability is an
	// action, not a tool, so the surface does not grow with the marketplace
	// (ADR-0041).
	if s.opts.Plugins != nil {
		sdk.AddTool(server, resultTool("invoke", "Do something to an entity, from the actions get listed for it, or poll an asynchronous run by passing its plugin and handle. Anything that changes something needs the proof token from the read it names and a unique idempotency_key; reuse that key if a reply is lost. Anything destructive also needs confirm. Pass preview to see what would happen instead."), s.invoke)

		sdk.AddTool(server, resultTool("configure", "Read or set a plugin's configuration. Pass settings to change fields, which are merged over what is there; omit it to see the current values. Credentials are entered in Dusk's own interface and cannot be set here."), s.configure)
	}

	// Registered whether or not anything can be written, for the same reason
	// note is: knowing what kinds exist is what stops the next one being a
	// misspelling of one that does.
	sdk.AddTool(server, resultTool("kinds", "Read the catalog's vocabulary of kinds, or extend it. Every kind in use is listed with what it is for and how many things carry it. Pass mint with a namespace and a role to add one, against the proof token this read returns. Kinds are open, so declaring an unlisted one always works; minting is how a kind gets a meaning something acts on."), s.kinds)

	// Registered whether or not anything can be written: reading what has been
	// written down is a read, and a deployment with nowhere to put a new note
	// can still answer "what ideas do I have".
	sdk.AddTool(server, resultTool("note", "Read or record something worth keeping that is not a description of any one thing: a gotcha, a runbook, an idea, the reason something is the way it is. Pass a body to write one, attaching it to the entities it concerns, or nothing to read what is there, narrowed by kind, status and what it is about. An id on its own reads that one note and returns the token to replace it. An idea about nothing in particular needs no refs. Close one with status done or dropped."), s.note)

	if s.opts.Writer != nil && s.opts.Tokens != nil {
		sdk.AddTool(server, resultTool("declare", "Create, correct, decommission, reactivate, or remove an entity declaration. Fields are merged unless unset names them. Removing an included declaration is destructive and needs confirm. Requires the proof token from a read; use get with repository to select one side of a duplicate."), s.declare)

		// A separate tool from declare because it writes a different thing: an
		// edge belongs to the entity it points from, and only that entity's
		// repository may assert it (ADR-0026).
		sdk.AddTool(server, resultTool("relate", "Declare, correct, or withdraw one outbound relation. The relation lives in the file of the entity it points from, so the token comes from a read of that one. Removing an edge needs confirm. What it points at does not have to be in the catalog."), s.relate)

		// A deployment with nowhere to put notes still reads them, but the page
		// is both halves of one file and needs somewhere to write it.
		if s.opts.Writer.NoteDestination() != "" {
			// One tool for both halves rather than two: reading the page is how
			// an agent gets the proof token that lets it write one back, so
			// splitting them would make the read look optional.
			sdk.AddTool(server, resultTool("page", "Read or rewrite the homepage. Omit body to read it, which returns the current page and the proof token needed to change it. Pass body to replace it: markdown with YAML frontmatter declaring an ordered list of blocks, each a typed query. What you send is the whole page."), s.page)
		}
	}

	return server
}

func resultTool(name, description string) *sdk.Tool {
	return &sdk.Tool{
		Name: name, Description: description,
		OutputSchema: map[string]any{
			"type": "object", "required": []string{"status"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{"ok", "error"}},
				"code":   map[string]any{"type": "string"},
				"data":   map[string]any{},
			},
		},
	}
}

type issuedProof struct {
	markdown string
	token    string
}

func (p issuedProof) withData(data map[string]any) map[string]any {
	if p.token != "" {
		data["proof"] = p.token
	}
	return data
}

// issue mints a proof token for what a read returned and hands it over unasked,
// since read-before-write is a contract nobody discovers. Markdown and
// structured clients receive the same token so either can complete the write.
func (s *Server) issue(origin proof.Origin, seen map[string]string, spend string) issuedProof {
	if s.opts.Tokens == nil || s.opts.Writer == nil {
		return issuedProof{}
	}
	token := s.opts.Tokens.Issue(origin, seen)
	return issuedProof{
		markdown: fmt.Sprintf("\n---\nProof token `%s`. %s\n", token.ID, spend),
		token:    token.ID,
	}
}

// noteWrites reports whether `note` is a call this deployment takes. A read
// only deployment has no writer at all, and the offer is built before `issue`
// gets the chance to decide there is nothing to offer.
func (s *Server) noteWrites() bool {
	return s.opts.Writer != nil && s.opts.Writer.NoteDestination() != ""
}

type searchInput struct {
	Query  string `json:"query" jsonschema:"words to search for"`
	Kind   string `json:"kind,omitempty" jsonschema:"restrict to one kind, such as service or host"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum results, default 25"`
	Offset int    `json:"offset,omitempty" jsonschema:"skip this many hits before the page. Use it with limit to walk a result too large to answer at once"`
}

func (s *Server) search(ctx context.Context, _ *sdk.CallToolRequest, in searchInput) (*sdk.CallToolResult, any, error) {
	if strings.TrimSpace(in.Query) == "" {
		return failure("invalid_argument", errors.New("a query is required; try a service name, a hostname, or a word from a description")), nil, nil
	}

	results, total, err := s.opts.Catalog.Search(ctx, "", index.SearchFilter{
		Query: in.Query, Kind: in.Kind, Limit: in.Limit, Offset: in.Offset,
	})
	if err != nil {
		return failure("catalog_search_failed", err), nil, nil
	}

	var out strings.Builder
	seen := map[string]string{}
	for _, hit := range results {
		seen[hit.Ref] = hit.Version
		fmt.Fprintf(&out, "- %s%s\n", noteMark(hit), named(hit.Title, hit.Ref))
		if snippet := strings.TrimSpace(hit.Snippet); snippet != "" {
			fmt.Fprintf(&out, "  %s\n", singleLine(snippet))
		}
	}

	if len(results) == 0 {
		// A search that found nothing is exactly what authorizes creating, so
		// it still issues a token: the absence is the evidence. It covers
		// nothing, so offering it to write "any of the above" offers nothing.
		issued := s.issue(proof.FromSearch, nil,
			"Pass it to `declare` to create what this search did not find.")
		return success(fmt.Sprintf("Nothing in the catalog matches %q%s.\n\nThe catalog only holds what repositories have declared in a dusk.md, so this may mean nobody has written it down yet. `changes` shows what has been read.%s",
			in.Query, ofKind(in.Kind), issued.markdown), issued.withData(map[string]any{
			"query": in.Query, "kind": in.Kind, "results": []index.SearchResult{}, "total": 0,
		})), nil, nil
	}
	issued := s.issue(proof.FromSearch, seen,
		fmt.Sprintf("Pass it to %s to write any of the above. It also authorizes creating what this search did not find.",
			s.catalogWrites()))
	page := map[string]any{
		"query": in.Query, "kind": in.Kind, "results": results, "total": total,
		"limit": len(results), "offset": in.Offset,
	}
	if in.Offset+len(results) < total {
		page["next_offset"] = in.Offset + len(results)
	}
	return success(fmt.Sprintf("%s Pass a ref to `get` for the full picture.\n\n%s%s",
		searchHeadline(len(results), total, in.Offset, in.Query, in.Kind),
		out.String(), issued.markdown), issued.withData(page)), nil, nil
}

// catalogWrites is what a token covering catalog content can be spent on. A
// deployment with nowhere to put a note offers no `note` tool, so naming it
// would name a call that does not exist.
func (s *Server) catalogWrites() string {
	if s.noteWrites() {
		return "`declare`, `relate` or `note`"
	}
	return "`declare` or `relate`"
}

// searchHeadline says how many matched as well as how many are shown, which
// ADR-0059 makes the difference between a short answer and a wrong one.
func searchHeadline(shown, total, offset int, query, kind string) string {
	if offset == 0 && shown >= total {
		return fmt.Sprintf("%d result(s) for %q%s.", shown, query, ofKind(kind))
	}

	said := fmt.Sprintf("%d-%d of %d result(s) for %q%s, highest ranked first.",
		offset+1, offset+shown, total, query, ofKind(kind))
	if offset+shown < total {
		said += fmt.Sprintf(" Ask again with `offset` %d for the next page.", offset+shown)
	}
	return said
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
	Ref        string `json:"ref" jsonschema:"entity ref, of the form kind:namespace/name, or plugin:name to read a plugin"`
	Repository string `json:"repository,omitempty" jsonschema:"read the declaration from this exact owner/name repository when integrity reports the ref more than once"`
	Titles     bool   `json:"titles,omitempty" jsonschema:"list the notes attached to it by kind, id and opening line instead of printing any of them whole, for finding out what is attached without reading it"`
}

// getPlugin answers for a plugin, listing what it can be asked to do.
func (s *Server) getPlugin(id string) (*sdk.CallToolResult, any, error) {
	if s.opts.Plugins == nil {
		return success("Plugins are not enabled here, so there is nothing to read.", map[string]any{"plugins": []plugin.Report{}}), nil, nil
	}

	var installed []string
	for _, report := range s.opts.Plugins.Report() {
		installed = append(installed, report.ID)
		if report.ID != id {
			continue
		}

		var out strings.Builder
		fmt.Fprintf(&out, "# %s\n\nPlugin, version %s, %s.\n", report.ID, report.Version, runningWord(report))

		declared := s.opts.Plugins.PluginActions(id)
		if actions := renderPluginActions(declared); actions != "" {
			out.WriteString(actions)
		} else {
			fmt.Fprintf(&out, "\n%s", nothingToRun(declared))
		}
		return success(out.String(), map[string]any{"plugin": report, "actions": declared}), nil, nil
	}

	slices.Sort(installed)
	return success(fmt.Sprintf("No plugin `%s` is installed. These are: %s.",
		id, strings.Join(installed, ", ")), map[string]any{"plugin": nil, "installed": installed}), nil, nil
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

	entity, err := s.opts.Catalog.GetFrom(ctx, "", in.Ref, in.Repository)
	// Only absence becomes a friendly answer. A storage failure reported as
	// "no such entity" would have the agent believe the thing does not exist.
	if errors.Is(err, index.ErrNotFound) {
		return success(noSuchEntity(in.Ref), map[string]any{"ref": in.Ref, "found": false}), nil, nil
	}
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}

	relations, err := s.opts.Catalog.Neighbors(ctx, "", in.Ref)
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}
	relations, omittedRelations := boundRelations(relations)

	notes, err := s.opts.Catalog.NotesFor(ctx, "", in.Ref)
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}

	titles, err := s.opts.Catalog.Titles(ctx, "", relatedRefs(in.Ref, relations))
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}
	sources, err := s.opts.Catalog.Sources(ctx, "", in.Ref)
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}

	// The notes go in the token too, so replacing one needs proof of the read
	// that surfaced it, the same as changing the entity does.
	seen := map[string]string{in.Ref: entity.GetProvenance().GetVersion()}
	for _, note := range notes {
		seen[note.GetId()] = note.GetContentHash()
	}

	// What can be done to a thing is part of the picture of that thing, and get
	// is deliberately fat for exactly this reason (ADR-0041).
	issued := s.issue(proof.FromGet, seen, changeThis(in.Ref, len(notes) > 0 && s.noteWrites()))
	return success(renderEntity(entity, relations, omittedRelations, titles, sources)+
		renderNotes(notes, len(notes), 0, getNotesBudget(in.Titles))+s.actionsFor(entity.GetKind())+
		issued.markdown, issued.withData(map[string]any{
		"entity": entity, "relations": relations, "relations_omitted": omittedRelations,
		"notes": notes, "sources": sources, "actions": s.entityActions(entity.GetKind()),
	})), nil, nil
}

func (s *Server) actionsFor(kind string) string {
	if s.opts.Plugins == nil {
		return ""
	}
	return renderActions(s.opts.Plugins.Actions(kind))
}

func (s *Server) entityActions(kind string) []plugin.Action {
	if s.opts.Plugins == nil {
		return nil
	}
	return enabled(s.opts.Plugins.Actions(kind))
}

// Attached notes are never limited, so every one of them matched. What varies
// is how much of each arrives, and no budget names them all.
func getNotesBudget(titlesOnly bool) int {
	if titlesOnly {
		return 0
	}
	return notesBudget
}

// changeThis is what a get's token can be spent on: the entity, an edge out of
// it, and any note the read returned with it, all of which the token covers.
func changeThis(ref string, withNotes bool) string {
	said := fmt.Sprintf("Pass it to `declare` to change `%s`, or to `relate` to connect it to something.", ref)
	if withNotes {
		said += " It also writes one of its notes, through `note` with that note's `id`."
	}
	return said
}

// notesBudget is how many bytes of notes one answer prints whole before the
// rest arrive named. ADR-0059 bounds in bytes rather than in notes, because the
// cost is bytes: ten runbooks printed whole were 67,836 characters.
const notesBudget = 12000

// renderNotes lists notes under a line saying how many matched and how many
// arrived whole, printing them whole while they fit budget and naming the rest.
// A note an agent knows exists is one it can ask for (ADR-0059).
func renderNotes(notes []*duskv1alpha1.Note, total, offset, budget int) string {
	if len(notes) == 0 {
		return ""
	}

	var body strings.Builder
	whole := 0
	for _, note := range notes {
		if body.Len() < budget {
			body.WriteString(renderNote(note))
			whole++
			continue
		}
		body.WriteString(nameNote(note))
	}

	return "\n## Notes\n\n" + notesHeading(len(notes), total, offset, whole) + body.String()
}

// notesHeading says how many matched, which of them arrived and how many of
// those arrived whole. The paging advice is only reachable from a read that has
// a limit: attached notes are never limited, so shown always equals total.
func notesHeading(shown, total, offset, whole int) string {
	said := fmt.Sprintf("%d note(s).", total)
	if shown < total {
		// Naming the next offset rather than advising a larger limit, which
		// re-sends this page and so cannot get past a caller's size cap.
		said = fmt.Sprintf("%d-%d of %d note(s), newest first. Ask again with `offset` %d for the next page, or narrow by `kind`, `status`, `pinned` or `ref`.",
			offset+1, offset+shown, total, offset+shown)
	}

	switch {
	case whole == 0:
		said += " Each is named by kind, id and opening line; pass an `id` to `note` for one whole."
	case whole < shown:
		said += fmt.Sprintf(" %d printed whole; the rest are named by kind, id and opening line, and pass an `id` to `note` for one of those whole.", whole)
	}
	return said + "\n\n"
}

// nameNote stands a note in for itself when the whole of it will not fit: its
// kind, its id, and the opening line ADR-0031 leaves as the closest thing a
// note has to a title.
func nameNote(note *duskv1alpha1.Note) string {
	return fmt.Sprintf("**%s** · `%s`: %s (not shown)\n\n",
		note.GetKind(), note.GetId(), firstLine(note.GetBody()))
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

// noSuchEntity is the answer to a ref nothing declares, naming the read that
// finds the right one.
func noSuchEntity(ref string) string {
	return fmt.Sprintf("No entity `%s` in the catalog. Try `search` for the name instead; refs are of the form kind:namespace/name.", ref)
}

func (s *Server) neighbors(ctx context.Context, _ *sdk.CallToolRequest, in neighborsInput) (*sdk.CallToolResult, any, error) {
	depth := in.Depth
	if depth < 1 {
		depth = 3
	}

	// The subject is resolved first: "no relations are declared for it" is what
	// a leaf answers, so saying it about a ref nothing declares is the absence
	// ADR-0059 forbids inventing, and it is read as "nothing depends on this".
	entity, err := s.opts.Catalog.Get(ctx, "", in.Ref)
	if errors.Is(err, index.ErrNotFound) {
		return s.undeclared(ctx, in.Ref)
	}
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}

	relations, err := s.opts.Catalog.Neighbors(ctx, "", in.Ref)
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}
	relations, omittedRelations := boundRelations(relations)
	dependents, err := s.opts.Catalog.Dependents(ctx, "", in.Ref, depth)
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}
	dependents, omittedDependents := boundDependents(dependents)

	named := relatedRefs(in.Ref, relations)
	for _, d := range dependents {
		named = append(named, d.Ref)
	}
	titles, err := s.opts.Catalog.Titles(ctx, "", named)
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# Around `%s`\n\n", in.Ref)
	out.WriteString(renderRelations(in.Ref, relations, omittedRelations, titles))

	if len(dependents) > 0 {
		fmt.Fprintf(&out, "\n## Reaches it within %d hop(s)\n\n", depth)
		for _, d := range dependents {
			fmt.Fprintf(&out, "- %s`%s` (%d hop(s) away)\n", nameOf(titles, d.Ref), d.Ref, d.Depth)
		}
		if omittedDependents > 0 {
			fmt.Fprintf(&out, "\n%d more dependent(s) were omitted. Narrow the starting entity or reduce `depth` before acting on this result.\n", omittedDependents)
		}
		out.WriteString("\nThese break, or need checking, if it goes away.\n")
	}

	// A traversal names refs without reading their contents, so the token it
	// issues covers only the entity it was asked about.
	seen := map[string]string{in.Ref: entity.GetProvenance().GetVersion()}
	issued := s.issue(proof.FromNeighbors, seen,
		fmt.Sprintf("Pass it to `declare` or `relate` to change `%s`, which is the only ref this walk read.", in.Ref))
	return success(out.String()+issued.markdown, issued.withData(map[string]any{
		"ref": in.Ref, "relations": relations, "relations_omitted": omittedRelations,
		"dependents": dependents, "dependents_omitted": omittedDependents,
	})), nil, nil
}

// undeclared answers a walk around a ref nothing declares. What points at one
// is still real, because ADR-0033 makes an unresolvable ref drift rather than
// an error, so it is reported under an answer that says the subject is absent.
func (s *Server) undeclared(ctx context.Context, ref string) (*sdk.CallToolResult, any, error) {
	relations, err := s.opts.Catalog.Neighbors(ctx, "", ref)
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}
	relations, omittedRelations := boundRelations(relations)
	if len(relations) == 0 {
		return success(noSuchEntity(ref), map[string]any{"ref": ref, "found": false, "relations": relations}), nil, nil
	}

	titles, err := s.opts.Catalog.Titles(ctx, "", relatedRefs(ref, relations))
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}
	return success(fmt.Sprintf("%s\n\nSomething points at it anyway, which `drift` reports as a ref nothing holds:\n\n%s",
		noSuchEntity(ref), renderRelations(ref, relations, omittedRelations, titles)), map[string]any{
		"ref": ref, "found": false, "relations": relations, "relations_omitted": omittedRelations,
	}), nil, nil
}

type declareInput struct {
	Ref            string            `json:"ref" jsonschema:"entity ref, of the form kind:namespace/name"`
	Proof          string            `json:"proof" jsonschema:"the proof token from the read that found it"`
	Title          string            `json:"title,omitempty" jsonschema:"human facing name"`
	Description    string            `json:"description,omitempty" jsonschema:"markdown prose describing it, replacing what is there"`
	Attributes     map[string]string `json:"attributes,omitempty" jsonschema:"attributes to set, merged with the existing ones"`
	ObservedAs     []string          `json:"observed_as,omitempty" jsonschema:"replace the refs observation plugins use for this declaration; an explicit empty list clears them"`
	Unset          []string          `json:"unset,omitempty" jsonschema:"fields to remove: title, description, observed_as, or attributes.<name>"`
	Decommissioned *bool             `json:"decommissioned,omitempty" jsonschema:"true keeps this as retired history and clears missing drift; false reactivates it; omit to leave lifecycle alone"`
	Repository     string            `json:"repository,omitempty" jsonschema:"owner/name for a new entity, or the exact declaring repository to change when the ref is duplicated"`
	Remove         bool              `json:"remove,omitempty" jsonschema:"delete the included declaration file instead of keeping it as decommissioned history"`
	Confirm        bool              `json:"confirm,omitempty" jsonschema:"required with remove because the entity and its outbound relations disappear from the catalog"`
}

func (s *Server) declare(ctx context.Context, _ *sdk.CallToolRequest, in declareInput) (*sdk.CallToolResult, any, error) {
	result, err := s.opts.Writer.Declare(ctx, in.Proof, write.Declaration{
		Ref:            in.Ref,
		Title:          in.Title,
		Description:    in.Description,
		Attributes:     in.Attributes,
		ObservedAs:     in.ObservedAs,
		Unset:          in.Unset,
		Decommissioned: in.Decommissioned,
		Repository:     in.Repository,
		Remove:         in.Remove,
		Confirm:        in.Confirm,
	})
	// A refused write is an answer, not a transport failure: the agent has to
	// read the reason and act on it.
	if err != nil {
		return failure("declaration_write_failed", fmt.Errorf("the write was not made: %w", err)), nil, nil
	}
	if result.Proposed {
		return success(proposal(result), result), nil, nil
	}
	if result.Existing {
		return success(fmt.Sprintf("`%s` already has the requested declaration state in %s at `%s`, so nothing was written.",
			result.Ref, result.Repository, result.Path), result), nil, nil
	}

	verb := "Updated"
	if result.Created {
		verb = "Created"
	} else if result.Removed {
		verb = "Removed"
	}

	// A kind already in the catalog resolves and says nothing, so this only
	// speaks when the declaration is what introduced the kind.
	kind, _, _ := strings.Cut(in.Ref, ":")
	warning := s.warnAboutKind(ctx, vocab.Entity, kind)

	return success(fmt.Sprintf(
		"%s `%s` in %s at `%s`.\n\nCommit: %s\n\nIt reaches the catalog on the next reconcile, which the push already triggered.%s",
		verb, result.Ref, result.Repository, result.Path, result.URL, warning), result), nil, nil
}

// nothingToWrite reports that a call asked for notes rather than changing one.
// With no id, kind and status are read filters. With one they are changes, so
// an id alone is a read: the call a refused write against a note names.
func nothingToWrite(in noteInput) bool {
	if in.Id == "" {
		return strings.TrimSpace(in.Body) == ""
	}
	return strings.TrimSpace(in.Body) == "" && strings.TrimSpace(in.Kind) == "" &&
		strings.TrimSpace(in.Status) == "" && in.Refs == nil && in.Pinned == nil
}

// noteInput leaves kind and body optional because an update merges over what
// the file says, so restating them would be a chance to get one wrong. A new
// note needs both, enforced by the write path rather than the schema.
type noteInput struct {
	Kind  string   `json:"kind,omitempty" jsonschema:"what sort of note: gotcha, runbook, howto, decision, incident, todo, or idea. Required for a new note, and a filter when reading"`
	Body  string   `json:"body,omitempty" jsonschema:"the note itself, as markdown. Required for a new note; replaces the whole body when updating"`
	Refs  []string `json:"refs,omitempty" jsonschema:"entity refs this note is about, of the form kind:namespace/name. An idea about nothing in particular needs none"`
	Id    string   `json:"id,omitempty" jsonschema:"the path of an existing note. Alone it reads that note and returns the token to replace it; with something to change it replaces it. Omit to write a new one"`
	Proof string   `json:"proof,omitempty" jsonschema:"the proof token from the read that found it, required only when replacing"`

	// A pointer because the schema needs three states, not two: leaving the
	// field out is how an update says nothing about pinning at all.
	Pinned *bool `json:"pinned,omitempty" jsonschema:"when reading, true for only the pinned and false for only the unpinned. When writing, true pins and false unpins. Leave it out for every note, or to change nothing"`

	Status string `json:"status,omitempty" jsonschema:"for a note that is work: open, done or dropped. A filter when reading, and what closes one when writing"`
	Ref    string `json:"ref,omitempty" jsonschema:"when reading, limit to notes about this entity"`
	Limit  int    `json:"limit,omitempty" jsonschema:"when reading, how many to return"`
	Offset int    `json:"offset,omitempty" jsonschema:"when reading, skip this many before the page. Use it with limit to walk a result too large to answer at once"`
}

// defaultNoteLimit bounds the query above what notesBudget could ever name, so
// the budget decides what is shown rather than a row limit nobody sees hit. The
// same reasoning as contextNotes: a cut at ten notes read as "there are ten".
const defaultNoteLimit = 100

// readNotes answers what has been written down, narrowed by kind, status and
// what it is about. The token it issues is what closing one of them needs, so
// asking "my open ideas" and then finishing one is two calls, not four.
func (s *Server) readNotes(ctx context.Context, in noteInput) (*sdk.CallToolResult, any, error) {
	filter := index.NoteFilter{
		Id: in.Id, Kind: in.Kind, Status: in.Status, Ref: in.Ref,
		Pinned: in.Pinned, Limit: in.Limit, Offset: in.Offset,
	}
	if filter.Limit <= 0 {
		filter.Limit = defaultNoteLimit
	}

	notes, err := s.opts.Catalog.Notes(ctx, "", filter)
	if err != nil {
		return failure("note_read_failed", err), nil, nil
	}
	total, err := s.opts.Catalog.CountNotes(ctx, "", filter)
	if err != nil {
		return failure("note_read_failed", err), nil, nil
	}

	seen := make(map[string]string, len(notes))
	for _, note := range notes {
		seen[note.GetId()] = note.GetContentHash()
	}

	// A read that matched nothing issues no token: it covers nothing, and a new
	// note needs none, so the token would be a key offered to no door.
	page := map[string]any{
		"notes": notes, "total": total, "limit": filter.Limit, "offset": filter.Offset,
	}
	if filter.Offset+len(notes) < total {
		page["next_offset"] = filter.Offset + len(notes)
	}

	if len(notes) == 0 {
		return success(describeNothing(in), page), nil, nil
	}
	issued := s.issue(proof.FromNote, seen,
		"Pass it to `note` with the `id` of one of the above to replace or close it.")
	return success(renderNotes(notes, total, filter.Offset, notesBudget)+issued.markdown,
		issued.withData(page)), nil, nil
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
	return said + ". Write one by passing a kind and a body, which needs no proof token: a note that does not exist cannot be overwritten.\n"
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
		return failure("note_writes_disabled", write.ErrNoConfigRepository), nil, nil
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
		return failure("note_write_failed", fmt.Errorf("the note was not written: %w", err)), nil, nil
	}
	if result.Proposed {
		return success(proposal(result), result), nil, nil
	}
	if result.Existing {
		return success(renderExisting(result), result), nil, nil
	}

	verb := "Updated"
	if result.Created {
		verb = "Wrote"
	}
	return success(fmt.Sprintf(
		"%s the note at `%s` in %s.\n\nCommit: %s\n\nIts id is `%s`. Pass that as `id` to replace it rather than writing a second one.%s",
		verb, result.Path, result.Repository, result.URL, result.Path,
		renderSimilar(result.Similar)+s.warnAboutKind(ctx, vocab.Note, in.Kind)), result), nil, nil
}

// renderIntegrity appends what is wrong with the graph. It arrives unasked
// for, like the proof token does, because an agent that has to know to ask
// whether an answer is trustworthy will not ask.
func (s *Server) renderIntegrity(ctx context.Context, out *strings.Builder) ([]index.Problem, error) {
	problems, err := s.opts.Catalog.Integrity(ctx, "", s.viewer())
	if err != nil {
		return nil, err
	}
	if len(problems) == 0 {
		out.WriteString("\nNothing is declared twice, and every relation and note points at something that exists.\n")
		return problems, nil
	}

	fmt.Fprintf(out, "\n## %d problem(s) with the catalog itself\n\n", len(problems))
	for _, problem := range problems {
		fmt.Fprintf(out, "**`%s`** (%s)\n\n%s.\n", problem.Ref, problem.Kind, problem.Detail)
		for _, where := range problem.Where {
			fmt.Fprintf(out, "- %s\n", where)
		}
		renderRepair(out, problem)
		out.WriteString("\n")
	}

	out.WriteString("These are not read failures. Each is a place the catalog answers confidently and may be wrong; the repair lines name the guarded reads and writes that resolve it.\n")
	return problems, nil
}

func renderRepair(out *strings.Builder, problem index.Problem) {
	switch problem.Kind {
	case index.ProblemDuplicate:
		out.WriteString("\nRepair: read each copy with `get(ref, repository)`, choose the declaration to keep, then use that copy's proof with `declare(repository, remove: true, confirm: true)` on every unwanted copy.\n")
	case index.ProblemDanglingRelation:
		out.WriteString("\nRepair: if the target is real, declare it. If the edge is wrong, read its source with `get`, then pass that proof to `relate` with the exact `from`, `type`, and `to` shown above plus `remove: true, confirm: true`.\n")
	}
}

type changesInput struct{}

func (s *Server) changes(ctx context.Context, _ *sdk.CallToolRequest, _ changesInput) (*sdk.CallToolResult, any, error) {
	var out strings.Builder
	now := s.now()

	// The durable half of every time in this answer. Asked once, because both
	// repositories and ingesters are dated from it.
	lastRead, err := s.opts.Catalog.LastRead(ctx)
	if err != nil {
		return failure("catalog_status_failed", err), nil, nil
	}

	// Soundness does not depend on sync status, so it is reported even where
	// there is none. Returning early here once meant a deployment without a
	// controller could never learn its catalog was broken.
	statuses := s.freshness(&out)
	renderReads(&out, now, statuses, lastRead)
	if len(statuses) > 0 {
		renderSweep(&out, now, s.opts.Syncs.NextSweep())
	}

	problems, err := s.renderIntegrity(ctx, &out)
	if err != nil {
		return failure("catalog_integrity_failed", err), nil, nil
	}

	// An observation nothing refreshes is a stale answer with no marker on it,
	// so how much to trust the catalog has to include whether its plugins work.
	if s.opts.Plugins != nil {
		out.WriteString(renderPlugins(s.opts.Plugins.Report(), now, lastRead))
	}
	data := map[string]any{"repositories": statuses, "last_read": lastRead, "integrity": problems}
	if s.opts.Plugins != nil {
		data["plugins"] = s.opts.Plugins.Report()
	}
	return success(out.String(), data), nil, nil
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

func renderEntity(entity *duskv1alpha1.Entity, relations []*duskv1alpha1.Relation, omittedRelations int, titles map[string]string, sources []index.EntitySource) string {
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
			fmt.Fprintf(&out, "- **%s**: %s\n", key, attributeValue(fields[key]))
		}
		out.WriteString("\n")
	}

	out.WriteString(renderRelations(entity.GetRef(), relations, omittedRelations, titles))

	if len(sources) > 0 {
		out.WriteString("\n## Provenance\n\n")
		for _, source := range sources {
			if source.Observed {
				fmt.Fprintf(&out, "- Observed by **%s** from `%s` at `%s`.\n", source.Source, source.Repository, short(source.Version))
				continue
			}
			fmt.Fprintf(&out, "- Declared in `%s` at `%s` from `%s`.\n", source.Repository, source.Path, short(source.Version))
		}
		if !slices.ContainsFunc(sources, func(source index.EntitySource) bool { return !source.Observed }) {
			out.WriteString("\nNo repository declares this entity; it is observed only.\n")
		}
	}
	return out.String()
}

// attributeValue renders one attribute: a scalar as prose, anything composite
// as the JSON it already is. `%s` against a structpb value printed
// `%!s(float64=125)`, and `[To Do In Progress]` is a different answer.
func attributeValue(value *structpb.Value) string {
	switch kind := value.GetKind().(type) {
	case *structpb.Value_StringValue:
		return kind.StringValue
	case *structpb.Value_NumberValue:
		return strconv.FormatFloat(kind.NumberValue, 'f', -1, 64)
	case *structpb.Value_BoolValue:
		return strconv.FormatBool(kind.BoolValue)
	case nil, *structpb.Value_NullValue:
		return "none"
	}

	// encoding/json rather than protojson, whose output is deliberately
	// unstable: it varies its spacing between runs.
	encoded, err := json.Marshal(value.AsInterface())
	if err != nil {
		return fmt.Sprintf("%v", value.AsInterface())
	}
	return string(encoded)
}

const relationResultLimit = 100

func boundRelations(relations []*duskv1alpha1.Relation) ([]*duskv1alpha1.Relation, int) {
	if len(relations) <= relationResultLimit {
		return relations, 0
	}
	return relations[:relationResultLimit], len(relations) - relationResultLimit
}

func boundDependents(dependents []index.Dependent) ([]index.Dependent, int) {
	if len(dependents) <= relationResultLimit {
		return dependents, 0
	}
	return dependents[:relationResultLimit], len(dependents) - relationResultLimit
}

func renderRelations(ref string, relations []*duskv1alpha1.Relation, omitted int, titles map[string]string) string {
	if len(relations) == 0 {
		return "No relations are declared for it.\n"
	}

	var out strings.Builder
	out.WriteString("## Connections\n\n")
	for _, relation := range relations {
		if relation.GetFrom() == ref {
			fmt.Fprintf(&out, "- %s → %s`%s`\n",
				relation.GetType(), nameOf(titles, relation.GetTo()), relation.GetTo())
			continue
		}
		fmt.Fprintf(&out, "- %s`%s` %s this\n",
			nameOf(titles, relation.GetFrom()), relation.GetFrom(), relation.GetType())
	}
	if omitted > 0 {
		fmt.Fprintf(&out, "\n%d more connection(s) were omitted to keep this result bounded. Use a more specific entity before acting on the list.\n", omitted)
	}
	return out.String()
}

// nameOf prefixes a ref with the title of what it points at. A ref nothing
// declares has none, and neither does an entity that never carried one.
func nameOf(titles map[string]string, ref string) string {
	if title := strings.TrimSpace(titles[ref]); title != "" {
		return "**" + title + "** "
	}
	return "**external or undeclared** "
}

// relatedRefs is every ref a relation names other than the one being read,
// which is what one Titles call has to cover.
func relatedRefs(ref string, relations []*duskv1alpha1.Relation) []string {
	refs := make([]string, 0, len(relations))
	for _, relation := range relations {
		if other := relation.GetFrom(); other != ref {
			refs = append(refs, other)
		}
		if other := relation.GetTo(); other != ref {
			refs = append(refs, other)
		}
	}
	return refs
}

func text(body string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: body}}}
}

type structuredResult struct {
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
	Data   any    `json:"data,omitempty"`
}

func success(body string, data any) *sdk.CallToolResult {
	result := text(body)
	result.StructuredContent = structuredResult{Status: "ok", Data: data}
	return result
}

func failure(code string, err error) *sdk.CallToolResult {
	return failureText(code, err.Error(), err)
}

func failureText(code, body string, err error) *sdk.CallToolResult {
	result := text(body)
	result.IsError = true
	result.StructuredContent = structuredResult{
		Status: "error",
		Code:   code,
		Data:   map[string]string{"message": err.Error()},
	}
	return result
}

// displayName falls back to the ref, because an entity with no title still has
// to be nameable in a list.
func displayName(title, ref string) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	return ref
}

// named is a hit's title beside its ref, or the ref alone. A note has no title
// (ADR-0031), so falling back to one printed its path as the name and again as
// the ref.
func named(title, ref string) string {
	if strings.TrimSpace(title) == "" {
		return "`" + ref + "`"
	}
	return fmt.Sprintf("**%s** `%s`", title, ref)
}

// sortedKeys keeps attribute order stable, because a map would otherwise
// reorder an entity's rendering on every call.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// andList joins names the way a sentence does, so an answer reads as prose
// rather than as a comma-separated field.
func andList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func quoted(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, "`"+name+"`")
	}
	return out
}

// shortSha is how much of a commit git itself shows.
const shortSha = 7

// short abbreviates a commit and only a commit. Provenance carries whatever
// identifies a version, which for an observation is a git ref, and taking the
// first seven characters of `refs/dusk/observed` named nothing at all.
func short(version string) string {
	if len(version) <= shortSha || !hex(version) {
		return version
	}
	return version[:shortSha]
}

func hex(value string) bool {
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
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
		return failure("page_write_failed", fmt.Errorf("the page was not written: %w", err)), nil, nil
	}
	if result.Proposed {
		return success(proposal(result), result), nil, nil
	}
	return success(fmt.Sprintf(
		"Rewrote `%s` in %s.\n\nCommit: %s\n\nThe homepage is now exactly what you sent.",
		result.Path, result.Repository, result.URL), result), nil, nil
}

func (s *Server) readPage(ctx context.Context) (*sdk.CallToolResult, any, error) {
	body, err := s.opts.Writer.Home(ctx)
	if errors.Is(err, fs.ErrNotExist) {
		declared, _ := yaml.Marshal(page.Default())
		// Nothing seen, because there is nothing there: this read is the one
		// that witnesses the absence declaring the first page needs.
		issued := s.issue(proof.FromPage, nil,
			"Pass it to `page` with a body to declare the first one.")
		return success(fmt.Sprintf(
			"No page is declared, so Dusk serves its default. Writing one replaces it entirely.\n\n"+
				"The default, as it would be written:\n\n```yaml\n---\n%s---\n```\n\n%s",
			declared, issued.markdown), issued.withData(map[string]any{"declared": false, "body": string(declared)})), nil, nil
	}
	if err != nil {
		return failure("page_read_failed", fmt.Errorf("the page could not be read: %w", err)), nil, nil
	}

	issued := s.issue(proof.FromPage, map[string]string{page.Path: duskmd.ContentHash(string(body))},
		"Pass it to `page` with a body to replace the whole of it.")
	return success(fmt.Sprintf(
		"The homepage, as declared in `%s`:\n\n```markdown\n%s\n```\n\n%s",
		page.Path, body, issued.markdown), issued.withData(map[string]any{
		"declared": true, "path": page.Path, "body": string(body),
	})), nil, nil
}
