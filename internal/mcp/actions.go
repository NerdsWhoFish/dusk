package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// Plugins is the slice of the plugin manager the agent surface needs, declared
// here so the tools do not depend on how a plugin is run.
type Plugins interface {
	Actions(kind string) []plugin.Action
	PluginActions(id string) []plugin.Action
	Invoke(ctx context.Context, request plugin.Request) (*plugin.Outcome, error)
	Preview(ctx context.Context, request plugin.Request) (*plugin.Outcome, error)
	Status(ctx context.Context, id, handle string) (*plugin.Outcome, error)
	Report() []plugin.Report

	// Settings and Configure are the non-sensitive half of a plugin's
	// configuration. Sensitive fields are never settable here: a secret passed
	// as a tool argument is a secret written into a transcript (ADR-0041).
	Settings(id, instance string) (map[string]any, []plugin.Field, string, error)
	Configure(ctx context.Context, id, instance string, settings map[string]any, expectedVersion string) error
}

type invokeInput struct {
	Ref     string         `json:"ref,omitempty" jsonschema:"the entity to act on, of the form kind:namespace/name. Omit only for an action that belongs to a plugin rather than to one thing"`
	Action  string         `json:"action,omitempty" jsonschema:"the action's name, as get reported it. Omit only when polling a handle"`
	Params  map[string]any `json:"params,omitempty" jsonschema:"arguments matching the action's declared schema"`
	Proof   string         `json:"proof,omitempty" jsonschema:"the proof token from the read the action names, required for anything that changes something"`
	Plugin  string         `json:"plugin,omitempty" jsonschema:"which plugin serves it, needed only when several offer the same action or when there is no ref"`
	Confirm bool           `json:"confirm,omitempty" jsonschema:"agree to this particular run, which anything destructive requires"`
	Preview bool           `json:"preview,omitempty" jsonschema:"say what would happen instead of doing it"`
	Handle  string         `json:"handle,omitempty" jsonschema:"an asynchronous handle returned by an earlier invocation. Pass it with plugin, and omit action, to poll that run"`
	Key     string         `json:"idempotency_key,omitempty" jsonschema:"a caller-chosen unique key required for a mutating run. Reuse the same key when retrying a request whose reply was lost"`
}

// invoke is the one tool a plugin's capability arrives through. Installing a
// tenth plugin adds no tools, which is what keeps ADR-0010's budget intact
// against an unbounded marketplace (ADR-0041).
// elicitor lets a plugin put a question to whoever is driving this session. A
// client without the capability answers unsupported rather than failing, so an
// action that can proceed without asking still runs (ADR-0046).
func elicitor(session *sdk.ServerSession) plugin.Elicitor {
	if !canElicit(session) {
		return nil
	}
	return func(ctx context.Context, ask plugin.Ask) (plugin.Answer, error) {
		waited, stop := context.WithTimeout(ctx, elicitPatience)
		defer stop()

		result, err := session.Elicit(waited, &sdk.ElicitParams{
			Message:         ask.Prompt,
			RequestedSchema: ask.Schema,
		})
		if err != nil {
			// A client that declares the capability and then does not answer is
			// indistinguishable from one that cannot, so the plugin is told the
			// same thing and decides for itself rather than waiting forever.
			if errors.Is(waited.Err(), context.DeadlineExceeded) {
				return plugin.Answer{Outcome: plugin.Unanswerable}, nil
			}
			return plugin.Answer{}, err
		}
		return plugin.Answer{Outcome: result.Action, Values: result.Content}, nil
	}
}

// elicitPatience bounds one question. ADR-0046 bounded how many times a plugin
// may ask, not how long an ask may take, so an unanswered one hung the whole
// invocation. A variable so a test need not wait on it.
var elicitPatience = 2 * time.Minute

// canElicit reports whether this client declared the capability. Answering nil
// rather than an erroring function keeps one path: no elicitor means the
// manager tells the plugin nobody can answer.
func canElicit(session *sdk.ServerSession) bool {
	if session == nil {
		return false
	}
	params := session.InitializeParams()
	return params != nil && params.Capabilities != nil && params.Capabilities.Elicitation != nil
}

func (s *Server) invoke(ctx context.Context, req *sdk.CallToolRequest, in invokeInput) (*sdk.CallToolResult, any, error) {
	if in.Handle != "" {
		if in.Plugin == "" {
			return failure("invalid_argument", errors.New("polling an action needs the plugin returned with its handle")), nil, nil
		}
		outcome, err := s.opts.Plugins.Status(ctx, in.Plugin, in.Handle)
		if err != nil {
			return failure("action_status_failed", err), nil, nil
		}
		return actionResult(outcome), nil, nil
	}
	request := plugin.Request{
		Ref: in.Ref, Action: in.Action, Params: in.Params, Proof: in.Proof,
		Plugin: in.Plugin, Confirm: in.Confirm, Preview: in.Preview,
		Actor:          "agent",
		Elicit:         elicitor(req.Session),
		IdempotencyKey: in.Key,
	}

	run := s.opts.Plugins.Invoke
	if in.Preview {
		run = s.opts.Plugins.Preview
	}

	outcome, err := run(ctx, request)
	switch {
	case errors.Is(err, plugin.ErrNeedsApproval):
		// An answer rather than an error: the agent is being asked, and can put
		// the question to whoever is watching.
		return success(err.Error(), map[string]any{"status": "confirmation_required"}), nil, nil
	case err != nil:
		return failure("action_failed", err), nil, nil
	}
	return actionResult(outcome), nil, nil
}

func actionResult(outcome *plugin.Outcome) *sdk.CallToolResult {
	result := success(renderOutcome(outcome), outcome)
	switch {
	case outcome.Unknown:
		result.IsError = true
		result.StructuredContent = structuredResult{Status: "error", Code: "mutation_outcome_unknown", Data: outcome}
	case outcome.Done && !outcome.OK:
		result.IsError = true
		result.StructuredContent = structuredResult{Status: "error", Code: "action_failed", Data: outcome}
	}
	return result
}

func verdictOf(outcome *plugin.Outcome) string {
	switch {
	case outcome.Previewed:
		return "preview"
	case outcome.Unknown:
		return "unknown"
	case !outcome.OK:
		return "failed"
	case !outcome.Done:
		return "started"
	}
	return "done"
}

// detailValue renders one detail. A plugin's detail is arbitrary structure, and
// Go's default formatting turns it into `map[k:v]`, which is neither JSON nor
// prose and cannot be parsed by whoever reads it.
func detailValue(value any) string {
	switch value.(type) {
	case nil, bool, string, float64, int, int64:
		return fmt.Sprintf("%v", value)
	}

	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

func renderOutcome(outcome *plugin.Outcome) string {
	var out strings.Builder

	fmt.Fprintf(&out, "**%s** %s", outcome.Action, verdictOf(outcome))
	if outcome.Ref != "" {
		fmt.Fprintf(&out, " on `%s`", outcome.Ref)
	}
	fmt.Fprintf(&out, ", by %s.\n", outcome.Plugin)

	if outcome.Message != "" {
		fmt.Fprintf(&out, "\n%s\n", outcome.Message)
	}
	if outcome.Handle != "" {
		fmt.Fprintf(&out, "\nIt is still running. Poll it with `invoke(plugin: %q, handle: %q)`.\n", outcome.Plugin, outcome.Handle)
	}
	if len(outcome.Detail) > 0 {
		out.WriteString("\n")
		for _, key := range sortedKeys(outcome.Detail) {
			rendered := detailValue(outcome.Detail[key])
			if strings.Contains(rendered, "\n") {
				fmt.Fprintf(&out, "- %s:\n\n```json\n%s\n```\n\n", key, rendered)
				continue
			}
			fmt.Fprintf(&out, "- %s: %s\n", key, rendered)
		}
	}
	if len(outcome.Changed) > 0 {
		fmt.Fprintf(&out, "\nChanged: %s\n", strings.Join(outcome.Changed, ", "))
	}

	if len(outcome.Steps) > 0 {
		out.WriteString("\n## Then\n\n")
		for _, step := range outcome.Steps {
			ran := "ran"
			if !step.OK {
				ran = "did not run"
			}
			fmt.Fprintf(&out, "- **%s** %s: %s\n", step.Action, ran, step.Message)
		}
	}
	return out.String()
}

// renderActions is what can be done to a thing, folded into the answer about
// that thing. An agent that has read an entity already knows what it can do to
// it, which is the whole of ADR-0041's discovery story.
func renderActions(actions []plugin.Action) string {
	offered := enabled(actions)
	if len(offered) == 0 {
		return ""
	}
	return renderActionList(offered) + "\nRun one with `invoke`.\n"
}

// renderPluginActions is the same list on a plugin's own page, where no ref has
// been named yet. "Naming this plugin and no ref" is wrong for every
// entity-scoped action, which invoke then refuses for applying to a kind.
func renderPluginActions(actions []plugin.Action) string {
	offered := enabled(actions)
	if len(offered) == 0 {
		return ""
	}

	var out strings.Builder
	out.WriteString(renderActionList(offered))
	out.WriteString("\n")
	if free := quoted(pluginScoped(offered)); len(free) > 0 {
		fmt.Fprintf(&out, "Run %s with `invoke`, naming this plugin and no ref.\n", andList(free))
	}
	out.WriteString(entityScopedRuns(offered))
	return out.String()
}

// entityScopedRuns says how to invoke what is about an entity, grouped by what
// each applies to, which is the fact invoke uses when it refuses one that
// arrived with no ref.
func entityScopedRuns(offered []plugin.Action) string {
	var applies []string
	named := map[string][]string{}
	for _, action := range offered {
		if !action.EntityScoped() {
			continue
		}
		kinds := strings.Join(action.Kinds, " or ")
		if _, seen := named[kinds]; !seen {
			applies = append(applies, kinds)
		}
		named[kinds] = append(named[kinds], "`"+action.Name+"`")
	}

	var out strings.Builder
	for _, kinds := range applies {
		fmt.Fprintf(&out, "Run %s with `invoke`, naming the ref of a %s.\n", andList(named[kinds]), kinds)
	}
	return out.String()
}

// nothingToRun says which kind of nothing: actions nobody enabled are fixable
// by whoever is reading, and a plugin declaring none is not.
func nothingToRun(actions []plugin.Action) string {
	if len(actions) == 0 {
		return "It declares no actions, so there is nothing to run. Anything it does is what it observes.\n"
	}
	return fmt.Sprintf("It declares %d action(s) and none of them is enabled, so there is nothing to run. "+
		"Enabling one is a deliberate act, on the plugin's page in Dusk.\n", len(actions))
}

// enabled is what has been turned on. ADR-0015 makes that a deliberate act, so
// installing a plugin never hands over what it declares.
func enabled(actions []plugin.Action) []plugin.Action {
	offered := make([]plugin.Action, 0, len(actions))
	for _, action := range actions {
		if action.Enabled {
			offered = append(offered, action)
		}
	}
	return offered
}

// pluginScoped names the actions that are about the plugin rather than about
// one thing, which are the only ones invoke takes with no ref.
func pluginScoped(offered []plugin.Action) []string {
	var names []string
	for _, action := range offered {
		if !action.EntityScoped() {
			names = append(names, action.Name)
		}
	}
	return names
}

func renderActionList(offered []plugin.Action) string {
	var out strings.Builder
	out.WriteString("\n## Actions\n\n")
	for _, action := range offered {
		fmt.Fprintf(&out, "- **%s** (%s, via %s): %s", action.Name, action.Class, action.Plugin, action.Description)
		if action.Approval == plugin.ApprovalConfirm {
			out.WriteString(" Needs `confirm`.")
		}
		if action.ProofFrom != "" && action.Class != plugin.ClassReadOnly {
			fmt.Fprintf(&out, " Needs the proof token from `%s`.", action.ProofFrom)
		}
		if len(action.Params) > 0 {
			out.WriteString(" Its complete parameter schema follows.")
		}
		out.WriteString("\n")
		if len(action.Params) > 0 {
			fmt.Fprintf(&out, "\n```json\n%s\n```\n", parameterSchema(action.Params))
		}
	}
	return out.String()
}

// parameterSchema preserves the plugin's JSON Schema rather than flattening it
// to names. Types, descriptions, enums, defaults and nested constraints are all
// part of the action contract an agent must satisfy.
func parameterSchema(schema map[string]any) string {
	encoded, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// renderPlugins reports plugin health, which was a UI-only answer while the
// surface meant to be primary could not see a broken plugin.
func renderPlugins(reports []plugin.Report, now time.Time, lastRead map[string]time.Time) string {
	if len(reports) == 0 {
		return ""
	}

	var out strings.Builder
	out.WriteString("\n## Plugins\n\n")
	for _, report := range reports {
		switch {
		case !report.Running:
			fmt.Fprintf(&out, "- **%s** %s is installed and %s.\n",
				report.ID, report.Version, downWord(report.Process))
		case report.Failing():
			fmt.Fprintf(&out, "- **%s** %s is running and failing on every configuration.\n",
				report.ID, report.Version)
		default:
			fmt.Fprintf(&out, "- **%s** %s is running%s.\n",
				report.ID, report.Version, cameBack(report.Process))
		}

		for _, health := range report.Health {
			fmt.Fprintf(&out, "  - %s\n", renderHealth(health, now, lastRead))
		}
	}
	return out.String()
}

// renderHealth dates one instance's observations and, when it is failing, says
// how long for and when it tries again. ADR-0011 keeps what a failing ingester
// last saw, so the age of that is the answer to how much to trust it.
func renderHealth(health plugin.Health, now time.Time, lastRead map[string]time.Time) string {
	named := health.Instance
	if named == "" {
		named = "its own configuration"
	}

	observed := "has never observed anything"
	if at, seen := lastRead[health.Scope]; seen {
		observed = "last observed " + stamp(now, at)
	}
	if health.Problem == "" {
		return fmt.Sprintf("`%s` %s", named, observed)
	}
	return fmt.Sprintf("`%s` %s, and %s failed, most recently %s: %s. Next attempt %s",
		named, observed, failing(health.Failures), stamp(now, health.At),
		singleLine(health.Problem), due(now, health.Next))
}

// failing phrases how long an instance has been broken, which is what separates
// a blip from something that has been serving the same answer for a week.
func failing(failures int) string {
	if failures < 2 {
		return "its last run"
	}
	return fmt.Sprintf("%d runs in a row have", failures)
}

// cameBack notes a plugin that is up now and has not been all along, because
// `changes` answers "how much should I trust this" and an observation from
// before a crash is older than the timestamp on it suggests.
func cameBack(process *plugin.Process) string {
	if process == nil || process.Restarts == 0 {
		return ""
	}
	return fmt.Sprintf(" (restarted %d times, last at %s)",
		process.Restarts, process.Since.Format(time.RFC3339))
}

type configureInput struct {
	Plugin   string         `json:"plugin" jsonschema:"which plugin to configure"`
	Settings map[string]any `json:"settings,omitempty" jsonschema:"the fields to set, merged over what is already there. Omit to read the current values"`
	Instance string         `json:"instance,omitempty" jsonschema:"which of the plugin's configurations, omitted for its own"`
	Version  string         `json:"version,omitempty" jsonschema:"the configuration version returned by the read, required when changing settings"`
	Proof    string         `json:"proof,omitempty" jsonschema:"the proof token returned by reading this configuration, required when changing settings"`
}

// configure sets a plugin's non-sensitive configuration. A sensitive field is
// refused rather than quietly dropped: passing a secret as a tool argument
// writes it into the transcript, and encrypting it unwrites nothing (ADR-0041).
func (s *Server) configure(ctx context.Context, _ *sdk.CallToolRequest, in configureInput) (*sdk.CallToolResult, any, error) {
	current, fields, version, err := s.opts.Plugins.Settings(in.Plugin, in.Instance)
	if err != nil {
		return failure("plugin_configuration_read_failed", err), nil, nil
	}

	if len(in.Settings) == 0 {
		return success(renderSettings(in.Plugin, in.Instance, current, fields, version)+s.configurationProof(in.Plugin, in.Instance, version), map[string]any{
			"plugin": in.Plugin, "instance": in.Instance, "settings": current, "fields": fields, "version": version,
		}), nil, nil
	}
	if s.opts.Tokens == nil {
		return failure("configuration_writes_disabled", errors.New("plugin configuration writes are disabled because no proof store is configured")), nil, nil
	}
	if err := s.opts.Tokens.AuthorizeUpdateFrom(in.Proof, proof.Configuration(in.Plugin, in.Instance), version); err != nil {
		return failure("stale_or_invalid_proof", err), nil, nil
	}

	if problem := configurationProblem(in.Plugin, in.Settings, fields); problem != "" {
		return failure("invalid_argument", errors.New(problem)), nil, nil
	}

	// Merged rather than replaced: an agent setting one field should not clear
	// the rest, which is what sending a whole form does.
	merged := maps.Clone(current)
	if merged == nil {
		merged = map[string]any{}
	}
	maps.Copy(merged, in.Settings)

	if err := s.opts.Plugins.Configure(ctx, in.Plugin, in.Instance, merged, in.Version); err != nil {
		return failure("plugin_configuration_write_failed", err), nil, nil
	}
	updated, fields, nextVersion, err := s.opts.Plugins.Settings(in.Plugin, in.Instance)
	if err != nil {
		return failure("plugin_configuration_read_failed", err), nil, nil
	}
	return success(renderSettings(in.Plugin, in.Instance, updated, fields, nextVersion)+s.configurationProof(in.Plugin, in.Instance, nextVersion), map[string]any{
		"plugin": in.Plugin, "instance": in.Instance, "settings": updated, "fields": fields, "version": nextVersion,
	}), nil, nil
}

func configurationProblem(id string, settings map[string]any, fields []plugin.Field) string {
	known := map[string]plugin.Field{}
	for _, field := range fields {
		known[field.Name] = field
	}

	var refused, unknown []string
	for name := range settings {
		field, ok := known[name]
		if !ok {
			unknown = append(unknown, name)
		} else if field.Sensitive {
			refused = append(refused, name)
		}
	}
	slices.Sort(refused)
	slices.Sort(unknown)
	if len(refused) > 0 {
		return fmt.Sprintf("%s is entered in Dusk's own interface and nowhere else, because a secret passed as a tool argument is a secret written into this transcript. Everything else about %s can be set here.",
			strings.Join(refused, " and "), id)
	}
	if len(unknown) > 0 {
		return fmt.Sprintf("%s declares no field named %s. It declares %s.",
			id, strings.Join(unknown, " or "), strings.Join(fieldNames(fields), ", "))
	}
	return ""
}

func (s *Server) configurationProof(id, instance, version string) string {
	if s.opts.Tokens == nil {
		return ""
	}
	subject := proof.Configuration(id, instance)
	token := s.opts.Tokens.Issue(proof.FromConfigure, map[string]string{subject.Ref: version})
	return fmt.Sprintf("\n---\nConfiguration version `%s`. Proof token `%s`. Pass both back to change these settings.\n", version, token.ID)
}

func renderSettings(id, instance string, settings map[string]any, fields []plugin.Field, version string) string {
	var out strings.Builder

	named := id
	if instance != "" {
		named = id + ", " + instance
	}
	fmt.Fprintf(&out, "# %s\n\n", named)

	for _, field := range fields {
		switch {
		case field.Sensitive:
			fmt.Fprintf(&out, "- **%s**: entered in Dusk's own interface, never here\n", field.Name)
		default:
			fmt.Fprintf(&out, "- **%s** (%s): %v\n", field.Name, field.Type, settings[field.Name])
		}
	}
	return out.String()
}

func fieldNames(fields []plugin.Field) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}
	return names
}
