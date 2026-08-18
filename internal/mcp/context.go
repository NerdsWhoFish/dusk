package mcp

import (
	"context"
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

// ContextBudget caps the rendered context in bytes, standing in for ADR-0014's
// token ceiling. Pinning is free to whoever pins and costs every future
// session, so the limit is enforced rather than advised.
const ContextBudget = 8000

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

// duskContext tailors the catalog to the repository an agent is working in.
// ADR-0014 makes it a tool rather than only an instructions block, so every
// client can reach it and a hook is an accelerator rather than a requirement.
func (s *Server) duskContext(ctx context.Context, _ *sdk.CallToolRequest, in contextInput) (*sdk.CallToolResult, any, error) {
	repository, err := s.matchRepository(ctx, in.Root)
	if err != nil {
		return failure("context_repository_resolution_failed", err), nil, nil
	}

	profile, err := s.contextProfile(ctx)
	if err != nil {
		return failure("context_profile_read_failed", err), nil, nil
	}

	entities, err := s.opts.Catalog.List(ctx, "", "")
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}

	vocabulary, err := s.opts.Catalog.Vocabulary(ctx, "")
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}
	held := takeInventory(entities, vocabulary)
	held.kinds = orderKinds(held.kinds, profile.KindOrder)

	var declared []string
	if repository != "" {
		if declared, err = s.opts.Catalog.Declared(ctx, "", repository); err != nil {
			return failure("catalog_read_failed", err), nil, nil
		}
	}

	here, elsewhere, err := s.pinned(ctx, repository)
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}

	tail, err := s.tail(ctx, held)
	if err != nil {
		return failure("catalog_read_failed", err), nil, nil
	}

	reading, _ := contextSections(declared, here, elsewhere, held)
	reading, priority := profileSections(profile, reading)
	body := assemble(contextHeader(in.Root, repository, len(declared), held.total, profile.Instructions), tail, reading, priority)

	return success(truncate(body, profile.Budget), map[string]any{
		"repository": repository, "declared": declared, "entity_count": held.total,
	}), nil, nil
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
	all, err := s.opts.Catalog.Notes(ctx, "", index.NoteFilter{Pinned: true, Limit: contextNotes})
	if err != nil {
		return nil, nil, err
	}
	if repository == "" {
		return nil, all, nil
	}

	here, err = s.opts.Catalog.Notes(ctx, "", index.NoteFilter{
		Pinned: true, AboutRepository: repository, Limit: contextNotes,
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
		heading: "\n## Pinned, about this repository\n\n",
		items:   noteItems(here),
		overflow: func(dropped []item) string {
			return fmt.Sprintf("%d more pinned note(s) about this repository did not fit. "+
				"Ask `note` with this repository's refs for them.\n\n", len(dropped))
		},
	}

	owned := &section{
		heading: fmt.Sprintf("\n## What this repository declares (%d)\n\n", len(declared)),
		overflow: func(dropped []item) string {
			return fmt.Sprintf("%d more it declares are not listed. `search` finds them by name.\n\n", len(dropped))
		},
	}
	for _, ref := range declared {
		line := fmt.Sprintf("- `%s`\n", ref)
		owned.items = append(owned.items, item{name: ref, full: line, short: line})
	}

	notesElsewhere := &section{
		heading: "\n## Pinned, across the estate\n\n",
		items:   noteItems(elsewhere),
		overflow: func(dropped []item) string {
			return fmt.Sprintf("%d more pinned note(s) did not fit. Ask `note` for them.\n\n", len(dropped))
		},
	}

	// Names only, per ADR-0014: this is an interaction manual and an inventory,
	// not somebody's whole CLAUDE.md. A kind left out is named rather than
	// counted, because a count cannot say `service` (ADR-0057).
	inventory := &section{
		heading: fmt.Sprintf("\n## What this operator has (%d)\n\n", held.total),
		overflow: func(dropped []item) string {
			return fmt.Sprintf("Kinds not listed: %s. `search` finds anything by name.\n\n", names(dropped))
		},
	}
	for _, group := range held.kinds {
		inventory.items = append(inventory.items, item{
			name:  fmt.Sprintf("%s (%d)", group.kind, len(group.refs)),
			full:  fmt.Sprintf("**%s** (%d): %s\n\n", group.kind, len(group.refs), strings.Join(group.refs, ", ")),
			short: fmt.Sprintf("**%s** (%d): names not listed, `search` finds them\n\n", group.kind, len(group.refs)),
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
func (s *Server) tail(ctx context.Context, held estate) (string, error) {
	var out strings.Builder

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

// actionable is the one sentence saying the catalog does things as well as
// answering questions. Nothing else in the context mentions actions, so an
// agent that never happens to `get` an acting entity never learns `invoke`.
func (s *Server) actionable(held estate) string {
	if s.opts.Plugins == nil {
		return ""
	}

	var acts []string
	for _, group := range held.kinds {
		offered := s.opts.Plugins.Actions(group.kind)
		if slices.ContainsFunc(offered, func(action plugin.Action) bool { return action.Enabled }) {
			acts = append(acts, "`"+group.kind+"`")
		}
	}
	if len(acts) == 0 {
		return ""
	}
	return fmt.Sprintf("\nThe catalog acts as well as answers: %s carry actions. "+
		"`get` names what an entity takes and `invoke` runs it.\n", listed(acts))
}

// names is what an overflow line calls the entries it left out.
func names(dropped []item) string {
	called := make([]string, 0, len(dropped))
	for _, entry := range dropped {
		called = append(called, entry.name)
	}
	return listed(called)
}

// listed names things and counts whatever it did not name. Every line built
// here is one whose room is reserved before it exists, so its length is capped
// rather than left to how much a catalog happens to hold.
func listed(names []string) string {
	if len(names) <= overflowNames {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:overflowNames], ", "), len(names)-overflowNames)
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
func assemble(header, tail string, reading, priority []*section) string {
	budget := ContextBudget - len(header) - len(tail)
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
			full: renderNote(note),
			short: fmt.Sprintf("**%s** · `%s`: %s (not shown; ask `note` for it)\n\n",
				note.GetKind(), note.GetId(), firstLine(note.GetBody())),
		})
	}
	return items
}

// firstLine stands in for a title. ADR-0031 makes a note's id its path and
// gives it no title, so its opening line is the closest thing it has to one.
func firstLine(body string) string {
	line := strings.TrimSpace(body)
	if cut := strings.IndexByte(line, '\n'); cut >= 0 {
		line = strings.TrimSpace(line[:cut])
	}

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
