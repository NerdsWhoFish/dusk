package mcp

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

// ContextBudget caps the rendered context in bytes, standing in for ADR-0014's
// token ceiling. Pinning is free to whoever pins and costs every future
// session, so the limit is enforced rather than advised.
const ContextBudget = 8000

// contextNotes bounds the note queries above what the budget could ever name,
// so ADR-0050's ranking decides what is shown rather than a query limit nobody
// would see hit. No line naming a note is shorter than twenty bytes.
const contextNotes = ContextBudget / 20

// noteSummary is how much of a note's opening line stands in for it when the
// whole note does not fit.
const noteSummary = 100

// sectionShare is the most of the budget one section may take before every
// other section has had a turn. An operator who pins forty notes still gets an
// inventory, which is the other half of what ADR-0014 promises.
const sectionShare = 50

type contextInput struct {
	Root string `json:"root,omitempty" jsonschema:"the repository being worked in, as a path or owner/name. Omit for an inventory of everything"`
}

// duskContext tailors the catalog to the repository an agent is working in.
// ADR-0014 makes it a tool rather than only an instructions block, so every
// client can reach it and a hook is an accelerator rather than a requirement.
func (s *Server) duskContext(ctx context.Context, _ *sdk.CallToolRequest, in contextInput) (*sdk.CallToolResult, any, error) {
	repository, err := s.matchRepository(ctx, in.Root)
	if err != nil {
		return nil, nil, err
	}

	entities, err := s.opts.Catalog.List(ctx, "", "")
	if err != nil {
		return nil, nil, err
	}

	var declared []string
	if repository != "" {
		if declared, err = s.opts.Catalog.Declared(ctx, "", repository); err != nil {
			return nil, nil, err
		}
	}

	here, elsewhere, err := s.pinned(ctx, repository)
	if err != nil {
		return nil, nil, err
	}

	tail, err := s.tail(ctx)
	if err != nil {
		return nil, nil, err
	}

	reading, priority := contextSections(declared, here, elsewhere, entities)
	body := assemble(header(in.Root, repository, len(declared), len(entities)), tail, reading, priority)

	return text(truncate(body, ContextBudget)), nil, nil
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
func contextSections(declared []string, here, elsewhere []*duskv1alpha1.Note, entities []*duskv1alpha1.Entity) (reading, priority []*section) {
	notesHere := &section{
		heading: "\n## Pinned, about this repository\n\n",
		items:   noteItems(here),
		overflow: func(n int) string {
			return fmt.Sprintf("%d more pinned note(s) about this repository did not fit. "+
				"Ask `note` with this repository's refs for them.\n\n", n)
		},
	}

	owned := &section{
		heading: fmt.Sprintf("\n## What this repository declares (%d)\n\n", len(declared)),
		overflow: func(n int) string {
			return fmt.Sprintf("...and %d more it declares. `search` finds them by name.\n\n", n)
		},
	}
	for _, ref := range declared {
		line := fmt.Sprintf("- `%s`\n", ref)
		owned.items = append(owned.items, item{full: line, short: line})
	}

	notesElsewhere := &section{
		heading: "\n## Pinned, across the estate\n\n",
		items:   noteItems(elsewhere),
		overflow: func(n int) string {
			return fmt.Sprintf("%d more pinned note(s) did not fit. Ask `note` for them.\n\n", n)
		},
	}

	// Names only, per ADR-0014: this is an interaction manual and an inventory,
	// not somebody's whole CLAUDE.md.
	byKind := map[string][]string{}
	for _, entity := range entities {
		byKind[entity.GetKind()] = append(byKind[entity.GetKind()], entity.GetRef())
	}
	inventory := &section{
		heading: fmt.Sprintf("\n## What this operator has (%d)\n\n", len(entities)),
		overflow: func(n int) string {
			return fmt.Sprintf("%d more kind(s) not listed. `search` finds anything by name.\n\n", n)
		},
	}
	for _, kind := range sortedKeysOf(byKind) {
		refs := byKind[kind]
		inventory.items = append(inventory.items, item{
			full:  fmt.Sprintf("**%s** (%d): %s\n\n", kind, len(refs), strings.Join(refs, ", ")),
			short: fmt.Sprintf("**%s** (%d): names not listed, `search` finds them\n\n", kind, len(refs)),
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

// tail is the fixed closing: how much reality supports, and what silence means.
// Both are reserved before anything is spent, because the sentence that stops
// an agent reading absence as proof is the worst one to lose to a cut.
func (s *Server) tail(ctx context.Context) (string, error) {
	var out strings.Builder

	drifts, err := s.opts.Catalog.Drift(ctx, "", index.DriftFilter{})
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

// item is one entry in a section: what it says in full, and the shorter form
// that still names it when the budget cannot afford the full one.
type item struct {
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
	overflow func(n int) string

	body string
}

// reserve is the room a section needs to report a total loss, set aside before
// anything is spent so that even the last section can say it exists.
func (s *section) reserve() int {
	if len(s.items) == 0 {
		return 0
	}
	return len(s.overflow(len(s.items)))
}

// wants is what the section would spend if nothing constrained it.
func (s *section) wants() int {
	if len(s.items) == 0 {
		return 0
	}
	want := len(s.heading)
	for _, entry := range s.items {
		want += len(entry.full)
	}
	return want
}

// render writes what budget allows and returns what it spent. The overflow line
// is not counted, because reserve already set that room aside.
func (s *section) render(budget int) int {
	if len(s.items) == 0 {
		return 0
	}

	var out strings.Builder
	spent := 0
	if len(s.heading) <= budget {
		out.WriteString(s.heading)
		spent = len(s.heading)
	}

	dropped := 0
	for _, entry := range s.items {
		switch {
		case spent+len(entry.full) <= budget:
			out.WriteString(entry.full)
			spent += len(entry.full)
		case spent+len(entry.short) <= budget:
			out.WriteString(entry.short)
			spent += len(entry.short)
		default:
			dropped++
		}
	}
	if dropped > 0 {
		out.WriteString(s.overflow(dropped))
	}

	s.body = out.String()
	return spent
}

// assemble spends the budget in priority order and writes the result in reading
// order. The two orders differ on purpose: what is worth most is not what
// belongs at the top.
func assemble(header, tail string, reading, priority []*section) string {
	budget := ContextBudget - len(header) - len(tail)
	for _, block := range priority {
		budget -= block.reserve()
	}
	budget = max(budget, 0)

	allowance := make([]int, len(priority))
	remaining := budget
	// The share caps the first pass so that nothing starves what follows it,
	// and the second pass hands on whatever the capped sections did not want,
	// so a cap costs nothing when only one section has anything to say.
	for _, ceiling := range []int{budget * sectionShare / 100, budget} {
		for i, block := range priority {
			take := min(min(block.wants(), ceiling)-allowance[i], remaining)
			if take <= 0 {
				continue
			}
			allowance[i] += take
			remaining -= take
		}
	}
	for i, block := range priority {
		block.render(allowance[i])
	}

	var out strings.Builder
	out.WriteString(header)
	for _, block := range reading {
		out.WriteString(block.body)
	}
	out.WriteString(tail)
	return out.String()
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

	scopes, err := s.opts.Catalog.Scopes(ctx)
	if err != nil {
		return "", err
	}

	cleaned := strings.Trim(path.Clean(root), "/")
	for _, scope := range scopes {
		if index.IsObserved(scope.Repository) {
			continue
		}
		if cleaned == scope.Repository || strings.HasSuffix(cleaned, "/"+scope.Repository) {
			return scope.Repository, nil
		}
	}
	return "", nil
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

func sortedKeysOf(byKind map[string][]string) []string {
	return slices.Sorted(maps.Keys(byKind))
}

func displayRoot(root string) string {
	cleaned := strings.Trim(path.Clean(root), "/")
	if cleaned == "" {
		return root
	}
	parts := strings.Split(cleaned, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return cleaned
}
