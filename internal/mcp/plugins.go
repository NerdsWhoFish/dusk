package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/internal/plugin"
)

// rosterNames caps how many kinds or actions one roster line names before it
// defers to the page. The roster is for choosing which plugin to open.
const rosterNames = 6

type pluginInput struct {
	Name string `json:"name,omitempty" jsonschema:"which plugin to read whole, with every action's parameter schema. Omit to list every installed one"`
}

// summary is one plugin on the roster: what it puts in the catalog, and what it
// can be told to do.
type summary struct {
	ID       string   `json:"id"`
	Version  string   `json:"version"`
	Running  bool     `json:"running"`
	State    string   `json:"state"`
	Emits    []string `json:"emits,omitempty"`
	Runs     []string `json:"runs,omitempty"`
	Declared int      `json:"declared_actions"`
}

// plugins answers the question no ref can name. ADR-0041 kept
// capability off the tool list, where agents look first (ADR-0077).
func (s *Server) plugins(_ context.Context, _ *sdk.CallToolRequest, in pluginInput) (*sdk.CallToolResult, any, error) {
	if in.Name != "" {
		return s.pluginPage(in.Name)
	}

	reports := s.opts.Plugins.Report()
	if len(reports) == 0 {
		return success("No plugins are installed, so Dusk answers about this estate and does nothing to it. "+
			"Installing one is done in Dusk's own interface.", map[string]any{"plugins": []summary{}}), nil, nil
	}

	roster := make([]summary, 0, len(reports))
	for _, report := range reports {
		roster = append(roster, s.summarize(report))
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# Integrations (%d)\n\n", len(roster))
	for _, entry := range roster {
		out.WriteString(rosterLine(entry))
	}
	out.WriteString("\nRead one whole with `plugin`, naming it, which prints every action with its parameter schema and how it is invoked. " +
		"`invoke` runs one and `configure` sets one up.\n")

	return success(out.String(), map[string]any{"plugins": roster}), nil, nil
}

func (s *Server) summarize(report plugin.Report) summary {
	declared := s.opts.Plugins.PluginActions(report.ID)

	entry := summary{
		ID: report.ID, Version: report.Version,
		Running:  report.Running,
		State:    runningWord(report),
		Emits:    s.opts.Plugins.Emits(report.ID),
		Declared: len(declared),
	}
	for _, action := range enabled(declared) {
		entry.Runs = append(entry.Runs, action.Name)
	}
	return entry
}

func rosterLine(entry summary) string {
	var out strings.Builder
	fmt.Fprintf(&out, "- **%s** %s, %s.", entry.ID, entry.Version, entry.State)

	if len(entry.Emits) > 0 {
		fmt.Fprintf(&out, " Puts %s in the catalog.", andList(capped(quoted(entry.Emits), rosterNames)))
	}
	switch {
	case len(entry.Runs) > 0:
		fmt.Fprintf(&out, " Runs %s.", andList(capped(quoted(entry.Runs), rosterNames)))
	case entry.Declared > 0:
		// ADR-0015 makes enabling deliberate, so this is fixable by whoever is
		// reading and "declares none" is not.
		fmt.Fprintf(&out, " %d declared action(s), none enabled.", entry.Declared)
	default:
		out.WriteString(" Nothing to run.")
	}
	return out.String() + "\n"
}

// capped names what it left out rather than cutting silently (ADR-0059). Takes
// names already rendered: quoting after would make "2 more" look like an
// identifier the reader can go and ask for.
func capped(names []string, limit int) []string {
	if len(names) <= limit {
		return names
	}
	return append(slices.Clone(names[:limit]), fmt.Sprintf("%d more", len(names)-limit))
}

// pluginPage is one plugin whole. Reached as `plugin` with a name and as
// `get plugin:<name>`, against one renderer so the two cannot drift.
func (s *Server) pluginPage(id string) (*sdk.CallToolResult, any, error) {
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

		emits := s.opts.Plugins.Emits(id)
		if len(emits) > 0 {
			fmt.Fprintf(&out, "\nIt puts %s in the catalog, which is what `search` and `get` then answer with.\n",
				andList(quoted(emits)))
		}

		declared := s.opts.Plugins.PluginActions(id)
		if actions := renderPluginActions(declared); actions != "" {
			out.WriteString(actions)
		} else {
			fmt.Fprintf(&out, "\n%s", nothingToRun(declared))
		}
		return success(out.String(), map[string]any{"plugin": report, "emits": emits, "actions": declared}), nil, nil
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
