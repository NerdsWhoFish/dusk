package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/contextconfig"
	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// ContextBudget is the default ceiling `.dusk/context.md` replaces. Aliased,
// not restated: two ceilings disagreeing is what hid ADR-0069's budget setting
// reaching the truncation backstop and never the allocation.
const ContextBudget = contextconfig.DefaultBudget

// contextNotes bounds the note queries above what the budget could ever name,
// so ADR-0050's ranking decides what is shown rather than a query limit nobody
// would see hit. No line naming a note is shorter than twenty bytes.
const contextNotes = contextconfig.MaxBudget / 20

// noteSummary is how much of a note's opening line stands in for it when the
// whole note does not fit.
const noteSummary = 100

// sectionShare is the most of what is left that one section may take on the
// first pass. Binding against the remainder rather than the whole is what
// leaves something for the section below it, at any number of sections.
const sectionShare = 50

// overflowNames caps how many entries an overflow line names, because the room
// for that line is reserved whether or not it ever prints.
const overflowNames = 12

type contextInput struct {
	Root string `json:"root,omitempty" jsonschema:"the exact owner/name repository being worked in. The dusk-context hook resolves this from the checkout. Omit for an inventory of everything"`
}

// ContextPreview is the complete answer dusk_context gives an agent. The web
// UI consumes this same value, so its preview cannot drift into a second
// implementation of the session orientation policy.
type ContextPreview struct {
	Repository  string
	Declared    []string
	EntityCount int
	Budget      int
	Context     string
}

type contextPreviewError struct {
	code string
	err  error
}

func (e *contextPreviewError) Error() string { return e.err.Error() }
func (e *contextPreviewError) Unwrap() error { return e.err }

func previewError(code, operation string, err error) error {
	return &contextPreviewError{code: code, err: fmt.Errorf("%s: %w", operation, err)}
}

// duskContext tailors the catalog to the repository an agent is working in.
// ADR-0014 makes it a tool rather than only an instructions block, so every
// client can reach it and a hook is an accelerator rather than a requirement.
func (s *Server) duskContext(ctx context.Context, _ *sdk.CallToolRequest, in contextInput) (*sdk.CallToolResult, any, error) {
	preview, err := s.PreviewContext(ctx, in.Root)
	if err != nil {
		var previewFailure *contextPreviewError
		if errors.As(err, &previewFailure) {
			return failure(previewFailure.code, previewFailure.err), nil, nil
		}
		return failure("context_render_failed", err), nil, nil
	}

	// Repeated because a client given an output schema may render only the
	// structured half. Elsewhere the data carries the answer; here the prose
	// is it, so dropping the content block loses everything and still says ok.
	return success(preview.Context, map[string]any{
		"repository": preview.Repository, "declared": preview.Declared,
		"entity_count": preview.EntityCount, "context": preview.Context,
	}), nil, nil
}

// PreviewContext assembles the exact dusk_context payload without involving
// the MCP transport. It is the single read used by agents and by the browser.
func (s *Server) PreviewContext(ctx context.Context, root string) (ContextPreview, error) {
	repository, err := s.matchRepository(ctx, root)
	if err != nil {
		return ContextPreview{}, previewError("context_repository_resolution_failed", "resolve repository", err)
	}

	profile, err := s.contextProfile(ctx)
	if err != nil {
		return ContextPreview{}, previewError("context_profile_read_failed", "read context profile", err)
	}

	entities, err := s.opts.Catalog.List(ctx, "", "")
	if err != nil {
		return ContextPreview{}, previewError("catalog_read_failed", "list catalog", err)
	}

	vocabulary, err := s.opts.Catalog.Vocabulary(ctx, "")
	if err != nil {
		return ContextPreview{}, previewError("catalog_read_failed", "read vocabulary", err)
	}
	held := takeInventory(entities, vocabulary)
	held.kinds = orderKinds(held.kinds, profile.KindOrder)

	var declared []string
	if repository != "" {
		if declared, err = s.opts.Catalog.Declared(ctx, "", repository); err != nil {
			return ContextPreview{}, previewError("catalog_read_failed", "read repository declarations", err)
		}
	}

	here, elsewhere, err := s.pinned(ctx, repository)
	if err != nil {
		return ContextPreview{}, previewError("catalog_read_failed", "read pinned notes", err)
	}

	tail, err := s.tail(ctx, held, vocabulary)
	if err != nil {
		return ContextPreview{}, previewError("catalog_read_failed", "render context guidance", err)
	}

	reading, _ := contextSections(declared, here, elsewhere, held)
	reading, priority := profileSections(profile, reading)
	body := assemble(profile.Budget,
		contextHeader(root, repository, len(declared), held.total, profile.Instructions),
		tail, reading, priority)
	rendered := truncate(body, profile.Budget)

	return ContextPreview{
		Repository: repository, Declared: declared, EntityCount: held.total,
		Budget: profile.Budget, Context: rendered,
	}, nil
}

func (s *Server) contextProfile(ctx context.Context) (contextconfig.Profile, error) {
	profile := contextconfig.Default()
	if s.opts.Writer == nil || s.opts.Writer.NoteDestination() == "" {
		return profile, nil
	}
	body, err := s.opts.Catalog.Context(ctx, "", s.opts.Writer.NoteDestination())
	if err != nil || len(body) == 0 {
		return profile, err
	}
	return contextconfig.Parse(body)
}

func contextHeader(root, repository string, declared, total int, instructions string) string {
	out := header(root, repository, declared, total)
	if instructions != "" {
		out += "\n## Operator instructions\n\n" + instructions + "\n"
	}
	return out
}

func orderKinds(groups []kindGroup, wanted []string) []kindGroup {
	if len(wanted) == 0 {
		return groups
	}
	rank := make(map[string]int, len(wanted))
	for i, kind := range wanted {
		rank[kind] = i
	}
	ordered := slices.Clone(groups)
	slices.SortStableFunc(ordered, func(a, b kindGroup) int {
		ar, aok := rank[a.kind]
		br, bok := rank[b.kind]
		switch {
		case aok && bok:
			return ar - br
		case aok:
			return -1
		case bok:
			return 1
		default:
			return 0
		}
	})
	return ordered
}

func profileSections(profile contextconfig.Profile, defaults []*section) (reading, priority []*section) {
	byName := map[string]*section{
		"repository-notes": defaults[0], "repository-entities": defaults[1],
		"estate-notes": defaults[2], "inventory": defaults[3],
	}
	if profile.Inventory == "counts" {
		for i := range defaults[3].items {
			defaults[3].items[i].full = defaults[3].items[i].short
		}
	}
	if len(profile.Sections) == 0 {
		reading = defaults
		priority = []*section{defaults[0], defaults[2], defaults[1], defaults[3]}
	} else {
		for _, name := range profile.Sections {
			reading = append(reading, byName[name])
		}
		priority = slices.Clone(reading)
	}
	if profile.Inventory == "off" {
		reading = slices.DeleteFunc(reading, func(section *section) bool { return section == defaults[3] })
		priority = slices.DeleteFunc(priority, func(section *section) bool { return section == defaults[3] })
	}
	return reading, priority
}

// estate is what the operator has, deduplicated, grouped by kind and ranked by
// what the kinds are for.
type estate struct {
	kinds []kindGroup
	total int
}

// kindGroup is one kind, what it is for, and the refs carrying it.
type kindGroup struct {
	kind string
	role vocab.Role
	refs []string
}

// takeInventory groups the catalog by kind and ranks the kinds by role. A ref
// arrives twice when two repositories declare it or an ingester observes what
// one of them declares, and printing it twice is a defect the reader sees.
func takeInventory(entities []*duskv1alpha1.Entity, vocabulary []vocab.Kind) estate {
	byKind := map[string][]string{}
	seen := make(map[string]bool, len(entities))
	for _, entity := range entities {
		ref := entity.GetRef()
		if seen[ref] {
			continue
		}
		seen[ref] = true
		byKind[entity.GetKind()] = append(byKind[entity.GetKind()], ref)
	}

	groups := make([]kindGroup, 0, len(byKind))
	for _, kind := range slices.Sorted(maps.Keys(byKind)) {
		groups = append(groups, kindGroup{
			kind: kind,
			role: vocab.RoleOf(vocab.Entity, kind, vocabulary),
			refs: byKind[kind],
		})
	}

	// Infrastructure is what somebody maintains and reference is a fact about
	// the world, so an orientation that opens with airports and never reaches
	// services is ordered backwards (ADR-0048, ADR-0057).
	slices.SortStableFunc(groups, func(a, b kindGroup) int {
		if rank := entityRank(a.role) - entityRank(b.role); rank != 0 {
			return rank
		}
		if len(a.refs) != len(b.refs) {
			return len(b.refs) - len(a.refs)
		}
		return strings.Compare(a.kind, b.kind)
	})
	return estate{kinds: groups, total: len(seen)}
}

// entityRank orders the entity roles, taking the order from vocab rather than
// restating it: Roles already lists them in rank order.
func entityRank(role vocab.Role) int {
	roles := vocab.Roles(vocab.Entity)
	if i := slices.Index(roles, role); i >= 0 {
		return i
	}
	return len(roles)
}

// pinned splits what somebody pinned into the notes about the repository being
// worked in and the rest. Two queries, because a note read from the index does
// not carry its refs and asking by repository is what tells the halves apart.
func (s *Server) pinned(ctx context.Context, repository string) (here, elsewhere []*duskv1alpha1.Note, err error) {
	all, err := s.opts.Catalog.Notes(ctx, "", index.NoteFilter{Pinned: new(true), Limit: contextNotes})
	if err != nil {
		return nil, nil, err
	}
	if repository == "" {
		return nil, all, nil
	}

	here, err = s.opts.Catalog.Notes(ctx, "", index.NoteFilter{
		Pinned: new(true), AboutRepository: repository, Limit: contextNotes,
	})
	if err != nil {
		return nil, nil, err
	}

	local := make(map[string]bool, len(here))
	for _, note := range here {
		local[note.GetId()] = true
	}
	for _, note := range all {
		if !local[note.GetId()] {
			elsewhere = append(elsewhere, note)
		}
	}
	return here, elsewhere, nil
}

// contextSections builds the answer's blocks, in reading order and again in
// the order they are paid for.
func contextSections(declared []string, here, elsewhere []*duskv1alpha1.Note, held estate) (reading, priority []*section) {
	notesHere := &section{
		heading: "\n## Pinned notes, about this repository\n\n",
		items:   repositoryNoteItems(here),
		overflow: func(dropped []item) string {
			return fmt.Sprintf("\n%d more pinned note(s) about this repository. `note` with `pinned: true` answers with every one:\n%s",
				len(dropped), names(dropped))
		},
	}

	owned := &section{
		heading: fmt.Sprintf("\n## What this repository declares (%d)\n\n", len(declared)),
		overflow: func(dropped []item) string {
			return fmt.Sprintf("\n%d more it declares. `search` finds any of them by name:\n%s",
				len(dropped), names(dropped))
		},
	}
	for _, ref := range declared {
		line := fmt.Sprintf("- `%s`\n", ref)
		owned.items = append(owned.items, item{name: "`" + ref + "`", full: line, short: line})
	}

	notesElsewhere := &section{
		heading: "\n## Pinned notes, across the estate\n\n",
		items:   noteItems(elsewhere),
		overflow: func(dropped []item) string {
			return fmt.Sprintf("\n%d more pinned note(s). `note` with `pinned: true` answers with every one:\n%s",
				len(dropped), names(dropped))
		},
	}

	// Names only, per ADR-0014: this is an interaction manual and an inventory,
	// not somebody's whole CLAUDE.md. A kind left out is named rather than
	// counted, because a count cannot say `service` (ADR-0057).
	inventory := &section{
		heading: fmt.Sprintf("\n## What this operator has (%d)\n\n", held.total),
		overflow: func(dropped []item) string {
			return fmt.Sprintf("\nOther kinds, and `kinds` lists the whole vocabulary:\n%s", names(dropped))
		},
	}
	for _, group := range held.kinds {
		var refs strings.Builder
		for _, ref := range group.refs {
			fmt.Fprintf(&refs, "  - `%s`\n", ref)
		}
		inventory.items = append(inventory.items, item{
			name:  fmt.Sprintf("%s (%d)", group.kind, len(group.refs)),
			full:  fmt.Sprintf("- **%s** (%d)\n%s", group.kind, len(group.refs), refs.String()),
			short: fmt.Sprintf("- **%s** (%d)\n", group.kind, len(group.refs)),
		})
	}

	// Written knowledge outranks enumerable fact: a ref left out is one
	// `search` away, and a gotcha left out is reachable by nothing, because
	// nobody asks for what they were never told exists (ADR-0050).
	return []*section{notesHere, owned, notesElsewhere, inventory},
		[]*section{notesHere, notesElsewhere, owned, inventory}
}

// header is the fixed opening, which is never spent and never cut.
func header(root, repository string, declared, total int) string {
	var out strings.Builder

	switch {
	case root == "":
		out.WriteString("# This operator's catalog\n")
	case repository == "":
		fmt.Fprintf(&out, "# %s is not in the catalog\n\n", displayRoot(root))
		out.WriteString("Nothing here declares a `dusk.md`, so Dusk knows nothing about this repository.\n" +
			"Adding one at the root opts it in, and is how a repository joins the catalog.\n")
	default:
		fmt.Fprintf(&out, "# %s in the catalog\n", repository)
	}

	if repository != "" && declared == 0 {
		out.WriteString("\nIt is in the catalog and declares no entities of its own.\n")
	}
	if total == 0 {
		out.WriteString("\nThe catalog is empty.\n")
	}
	return out.String()
}

// tail is the fixed closing: what can be done, how much reality supports, and
// what silence means. All are reserved before anything is spent, because the
// sentence that stops an agent reading absence as proof is the worst to lose.
func (s *Server) tail(ctx context.Context, held estate, vocabulary []vocab.Kind) (string, error) {
	var out strings.Builder

	out.WriteString(s.manual(vocabulary))
	out.WriteString(s.actionable(held))

	drifts, err := s.opts.Catalog.Drift(ctx, "", index.DriftFilter{}, s.viewer())
	if err != nil {
		return "", err
	}
	if len(drifts) > 0 {
		var notes int
		for _, drift := range drifts {
			if drift.Kind == index.DriftNoteRef {
				notes++
			}
		}
		fmt.Fprintf(&out, "\n%d thing(s) the catalog claims are not supported by reality, %d of them notes pointing at nothing. Call `drift` for the list.\n",
			len(drifts), notes)
	}

	out.WriteString("\nAsk `search` before assuming something is not here. " +
		"The catalog only knows what a repository wrote down, so absence means nobody documented it, not that it does not exist.\n")
	return out.String(), nil
}

// manual is ADR-0014's "interaction manual" half, which had decayed into
// sentences scattered through the other sections. One block, because a fact
// restated per item is one a reader skips and pays for on every line.
func (s *Server) manual(vocabulary []vocab.Kind) string {
	var out strings.Builder

	out.WriteString("\n## Working with this catalog\n\n" +
		"Refs are `kind:namespace/name` and feed straight back into `get`, which also takes `plugin:<name>`. " +
		"A note's id is its path, `.dusk/<kind>-<hash>.md`. " +
		"A note listed above as a single line was named rather than printed: pass its id to `note` as `id` to read it whole. " +
		"Every read of something writable returns a `proof` token, and the write that follows requires it.\n\n" +
		"| Call | For |\n| --- | --- |\n" +
		"| `search` | Find anything by name or any word in it. Start here, and before concluding something is absent |\n" +
		"| `get` | One entity whole: description, attributes, relations, attached notes, and the actions it offers |\n" +
		"| `neighbors` | What points at a thing, and so what breaks if it goes away |\n" +
		"| `note` | Read or write knowledge. Filters on `kind`, `status`, `ref` and `pinned`; pages with `limit` and `offset` |\n" +
		"| `changes` | What Dusk last read from each repository, for telling a stale answer from a missing one |\n" +
		"| `drift` | Where the catalog and reality disagree |\n" +
		"| `kinds` | The vocabulary, so the next kind is not a misspelling of one that exists |\n")

	// Every tool this deployment registered and no other, on the registration
	// conditions themselves: naming an absent tool misdirects, and omitting a
	// registered one hides it (ADR-0057, ADR-0077).
	if s.opts.Writer != nil && s.opts.Tokens != nil {
		out.WriteString("| `declare`, `relate` | Write an entity, or one outbound edge between two |\n")
		if s.opts.Writer.NoteDestination() != "" {
			out.WriteString("| `page` | Read or rewrite the homepage, as an ordered list of typed queries |\n")
		}
	}
	if s.opts.Repositories != nil && s.opts.Tokens != nil {
		out.WriteString("| `repository` | Read, create, or rewrite a repository's root `dusk.md` |\n")
	}
	if s.opts.Plugins != nil {
		out.WriteString("| `plugin` | What the integrations here observe, and what they can be told to do |\n" +
			"| `invoke` | Run an action that `get` or `plugin` offered |\n" +
			"| `configure` | Read or set a plugin's non-sensitive configuration |\n")
	}

	out.WriteString("\n**Knowledge is kept as notes, and the kind is the only thing that distinguishes one sort from another.** " +
		"There is no separate tool for any of them: an architecture decision is `note(kind: \"decision\")`, " +
		"an outage writeup is `note(kind: \"incident\")`. A kind showing zero has simply never been written.\n\n")
	for _, role := range vocab.Roles(vocab.Note) {
		kinds := vocab.WithRole(vocabulary, role)
		if len(kinds) == 0 {
			continue
		}
		named := make([]string, 0, len(kinds))
		for _, kind := range kinds {
			named = append(named, fmt.Sprintf("`%s` (%d)", kind.Name, kind.Count))
		}
		fmt.Fprintf(&out, "- **%s**: %s\n", role, strings.Join(named, ", "))
	}

	return out.String()
}

// actionable says the catalog does things as well as answering questions, and
// names what does them. Nothing else in the context mentions actions, so an
// agent that never happens to `get` an acting entity never learns `invoke`.
func (s *Server) actionable(held estate) string {
	if s.opts.Plugins == nil {
		return ""
	}

	var out strings.Builder

	var acts []string
	for _, group := range held.kinds {
		offered := s.opts.Plugins.Actions(group.kind)
		if slices.ContainsFunc(offered, func(action plugin.Action) bool { return action.Enabled }) {
			acts = append(acts, "`"+group.kind+"`")
		}
	}
	if len(acts) > 0 {
		fmt.Fprintf(&out, "\nThe catalog acts as well as answers. `get` names what an entity takes and `invoke` runs it, "+
			"and these kinds carry actions:\n%s", listed(acts))
	}

	// Independent of the sentence above, which walks entity kinds and so never
	// reaches a plugin that only observes or whose actions name no kind.
	if roster := s.installedPlugins(); len(roster) > 0 {
		fmt.Fprintf(&out, "\nThese integrations are installed, and `plugin` reads one whole:\n%s", listed(roster))
	}

	return out.String()
}

// contextPluginNames caps a roster line harder than the tool does, because this
// one is in the tail, which is reserved before any section is paid.
const contextPluginNames = 3

// installedPlugins is one line per plugin: what it observes and what it runs.
// ADR-0076 named only those whose actions attached to no kind, which left a
// purely observing plugin mentioned nowhere at all (ADR-0077).
func (s *Server) installedPlugins() []string {
	var roster []string
	for _, report := range s.opts.Plugins.Report() {
		var does []string
		if emits := s.opts.Plugins.Emits(report.ID); len(emits) > 0 {
			does = append(does, "observes "+andList(capped(quoted(emits), contextPluginNames)))
		}
		if runs := runnableNames(s.opts.Plugins.PluginActions(report.ID)); len(runs) > 0 {
			does = append(does, "runs "+andList(capped(quoted(runs), contextPluginNames)))
		}
		roster = append(roster, strings.TrimSpace("`"+report.ID+"` "+strings.Join(does, ", and ")))
	}
	slices.Sort(roster)
	return roster
}

func runnableNames(actions []plugin.Action) []string {
	var names []string
	for _, action := range enabled(actions) {
		names = append(names, action.Name)
	}
	return names
}

// names is what an overflow line calls the entries it left out.
func names(dropped []item) string {
	called := make([]string, 0, len(dropped))
	for _, entry := range dropped {
		called = append(called, entry.name)
	}
	return listed(called)
}

// listed renders things as a markdown list, capped because its room is
// reserved whether or not it prints. Callers name the call answering with the
// rest: a remainder nothing can ask for is the defect this line fixes.
func listed(names []string) string {
	shown := names
	if len(shown) > overflowNames {
		shown = shown[:overflowNames]
	}

	var out strings.Builder
	for _, name := range shown {
		out.WriteString("\n- ")
		out.WriteString(name)
	}
	if hidden := len(names) - len(shown); hidden > 0 {
		fmt.Fprintf(&out, "\n- and %d more", hidden)
	}
	return out.String() + "\n"
}

// item is one entry in a section: what it says in full, the shorter form that
// still names it when the budget cannot afford the full one, and what an
// overflow line calls it when it does not fit at all.
type item struct {
	name  string
	full  string
	short string
}

// section is one block of the answer. Sections are given budget in priority
// order and written in reading order, which is what lets a pinned note outrank
// an inventory printed above it (ADR-0050).
type section struct {
	heading string
	items   []item

	// overflow names what did not fit. A section with no way to say it dropped
	// something is a section that drops it silently.
	overflow func(dropped []item) string

	body string
}

// reserve is the room a section needs to say it exists and to report a total
// loss: its heading and its overflow line, both set aside before anything is
// spent so an overflow line never prints under no heading (ADR-0057).
func (s *section) reserve() int {
	if len(s.items) == 0 {
		return 0
	}
	return len(s.heading) + len(s.overflow(worstDrop(s.items)))
}

// worstDrop is the drop set producing the longest overflow line. An overflow
// names a bounded number of entries, so the most it can cost is that many of
// the longest names with every remaining item counted behind them.
func worstDrop(items []item) []item {
	worst := slices.Clone(items)
	slices.SortFunc(worst, func(a, b item) int { return len(b.name) - len(a.name) })
	return worst
}

// render writes what budget allows and returns what it spent. Neither the
// heading nor the overflow line is counted, because reserve already set that
// room aside.
func (s *section) render(budget int) int {
	if len(s.items) == 0 {
		return 0
	}

	var out strings.Builder
	out.WriteString(s.heading)

	spent := 0
	var dropped []item
	for _, entry := range s.items {
		switch {
		case spent+len(entry.full) <= budget:
			out.WriteString(entry.full)
			spent += len(entry.full)
		case spent+len(entry.short) <= budget:
			out.WriteString(entry.short)
			spent += len(entry.short)
		default:
			dropped = append(dropped, entry)
		}
	}
	if len(dropped) > 0 {
		out.WriteString(s.overflow(dropped))
	}

	s.body = out.String()
	return spent
}

// grow re-renders the section against more room and reports the extra it spent.
// Greedy packing is not quite monotonic, since a full entry that only fits at
// the larger budget can crowd out two shorts, so a worse render is undone.
func (s *section) grow(spent, room int) int {
	if room <= spent {
		return 0
	}
	if grown := s.render(room); grown >= spent {
		return grown - spent
	}
	s.render(spent)
	return 0
}

// assemble spends the budget in priority order and writes the result in reading
// order. The two orders differ on purpose: what is worth most is not what
// belongs at the top.
func assemble(ceiling int, header, tail string, reading, priority []*section) string {
	budget := ceiling - len(header) - len(tail)
	for _, block := range priority {
		budget -= block.reserve()
	}
	spend(max(budget, 0), priority)

	var out strings.Builder
	out.WriteString(header)
	for _, block := range reading {
		out.WriteString(block.body)
	}
	out.WriteString(tail)
	return out.String()
}

// spend hands the budget out in priority order, charging each section what it
// wrote rather than what it was offered. A section granted room for whole notes
// and printing one-line shorts holds budget it will never use (ADR-0057).
func spend(budget int, priority []*section) {
	// Rendered before anything is spent, so a section that never wins any room
	// still carries the heading and overflow line reserve set aside for it.
	for _, block := range priority {
		block.render(0)
	}

	spent := make([]int, len(priority))
	remaining := budget
	// The first pass caps a section at a share of what is left when its turn
	// comes, so there is always something for the section below it. The second
	// hands on everything the first did not use.
	for _, share := range []int{sectionShare, 100} {
		for i, block := range priority {
			extra := block.grow(spent[i], spent[i]+remaining*share/100)
			spent[i] += extra
			remaining -= extra
		}
	}
}

// noteItems renders notes for a budget: whole while they fit, and named by
// kind, id and opening line when they do not. A note that vanishes teaches
// nothing, and one an agent knows exists is one it can ask for.
func noteItems(notes []*duskv1alpha1.Note) []item {
	items := make([]item, 0, len(notes))
	for _, note := range notes {
		items = append(items, item{
			// Backticked, because an overflow line naming it is the id an agent
			// pastes straight back into `note`.
			name:  "`" + note.GetId() + "`",
			full:  demoteHeadings(renderNote(note)),
			short: fmt.Sprintf("- **%s** `%s`: %s\n", note.GetKind(), note.GetId(), firstLine(note.GetBody())),
		})
	}
	return items
}

// repositoryNoteItems keeps local pinned knowledge cheap and discoverable.
// The title is the payload and the nested call is the lossless path to the
// complete note, so every local pin costs two short list lines at every budget.
func repositoryNoteItems(notes []*duskv1alpha1.Note) []item {
	items := make([]item, 0, len(notes))
	for _, note := range notes {
		line := fmt.Sprintf("- %s\n    - read: `note({ id: %q })`\n", firstLine(note.GetBody()), note.GetId())
		items = append(items, item{
			name: "`" + note.GetId() + "`", full: line, short: line,
		})
	}
	return items
}

// demoteHeadings sinks a note's headings below the section it prints under, so
// one long note does not read as several sections. Context only: `note` renders
// a body an agent can write back, and a shifted heading would be committed.
func demoteHeadings(body string) string {
	var out strings.Builder
	fenced := false

	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			fenced = !fenced
		case !fenced && strings.HasPrefix(trimmed, "#"):
			// An ATX heading needs a space after its hashes, and there has to
			// be room left under h6.
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			if level+contextHeadingDepth <= 6 && strings.HasPrefix(trimmed[level:], " ") {
				line = strings.Repeat("#", contextHeadingDepth) + trimmed
			}
		}
		out.WriteString(line)
		out.WriteString("\n")
	}

	return strings.TrimSuffix(out.String(), "\n")
}

// contextHeadingDepth is how far a note's headings move: its `#` lands below
// the `##` its section heading uses.
const contextHeadingDepth = 2

// firstLine stands in for a title, because ADR-0031 gives a note none. The
// heading markers come off: the line lands inside a list item, where a stray
// `#` reads as broken markdown rather than as structure.
func firstLine(body string) string {
	line := strings.TrimSpace(body)
	if cut := strings.IndexByte(line, '\n'); cut >= 0 {
		line = strings.TrimSpace(line[:cut])
	}
	line = strings.TrimSpace(strings.TrimLeft(line, "#"))

	runes := []rune(line)
	if len(runes) <= noteSummary {
		return line
	}
	return strings.TrimSpace(string(runes[:noteSummary])) + "..."
}

// matchRepository maps a filesystem root or slug to a repository the catalog
// knows. Matching on the trailing owner/name is what lets an agent pass the
// absolute path it actually has.
func (s *Server) matchRepository(ctx context.Context, root string) (string, error) {
	if root == "" {
		return "", nil
	}
	if s.opts.Catalog == nil {
		return "", nil
	}

	return s.opts.Catalog.ResolveRepository(ctx, strings.TrimSpace(root))
}

// truncate is the backstop under ADR-0050's allocation, which is meant to keep
// the answer inside the budget by itself. A silently shortened context degrades
// every answer with nothing to connect the degradation to.
func truncate(body string, budget int) string {
	if len(body) <= budget {
		return body
	}

	cut := body[:budget]
	if last := strings.LastIndex(cut, "\n"); last > 0 {
		cut = cut[:last]
	}
	return cut + fmt.Sprintf("\n\n---\nTruncated at %d bytes. Use `search` and `get` for anything not listed above.\n", budget)
}

func displayRoot(root string) string {
	cleaned := strings.Trim(root, "/")
	if cleaned == "" {
		return root
	}
	parts := strings.Split(cleaned, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return cleaned
}
