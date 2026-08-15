package plugin

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"google.golang.org/protobuf/types/known/structpb"
)

// Class names what an action does, in the words the UI and an agent both read.
const (
	ClassReadOnly    = "read_only"
	ClassMutating    = "mutating"
	ClassDestructive = "destructive"
)

// Approval is what a caller must do before an action runs. Class drives the
// default so an install does not hand-configure every action (ADR-0015).
const (
	// ApprovalAutomatic means enabling the action was the decision.
	ApprovalAutomatic = "automatic"

	// ApprovalConfirm means this particular run has to be agreed to, with the
	// preview, or the fact that there is none, put in front of whoever agrees.
	ApprovalConfirm = "confirm"
)

// Action is one thing that can be done, resolved to the plugin serving it.
type Action struct {
	Plugin      string `json:"plugin"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Class       string `json:"class"`

	// Params is the action's JSON Schema, passed through rather than
	// interpreted: Dusk renders a form from it and an agent fills it in.
	Params map[string]any `json:"params,omitempty"`

	ProofFrom string   `json:"proof_from,omitempty"`
	Async     bool     `json:"async"`
	Kinds     []string `json:"kinds,omitempty"`

	// Then names what this action may ask Dusk to run after it.
	Then []string `json:"then,omitempty"`

	// Enabled is the deliberate act ADR-0015 requires. Declaring an action does
	// not grant it: installing a plugin must not silently hand over capability.
	Enabled bool `json:"enabled"`

	// Approval covers the whole chain, so a harmless first step that leads to a
	// destructive one is confirmed like the destructive one.
	Approval string `json:"approval"`
}

// EntityScoped reports whether this action is about one thing in the catalog.
// An action naming no kinds is about the plugin itself, which is how something
// gets created that the catalog does not know about yet.
func (a Action) EntityScoped() bool { return len(a.Kinds) > 0 }

func classOf(class duskv1alpha1.ActionClass) string {
	switch class {
	case duskv1alpha1.ActionClass_ACTION_CLASS_READ_ONLY:
		return ClassReadOnly
	case duskv1alpha1.ActionClass_ACTION_CLASS_DESTRUCTIVE:
		return ClassDestructive
	case duskv1alpha1.ActionClass_ACTION_CLASS_MUTATING, duskv1alpha1.ActionClass_ACTION_CLASS_UNSPECIFIED:
		return ClassMutating
	}
	return ClassMutating
}

// mutates reports whether running this needs proof that the caller read what it
// is about to change.
func mutates(class string) bool { return class != ClassReadOnly }

// actionsOf reads what a running plugin declares, with enablement applied from
// the record and approval defaulted from class.
func actionsOf(running *Running, enabled []string) []Action {
	declared := running.Describe.GetActions()
	actions := make([]Action, 0, len(declared))

	for _, descriptor := range declared {
		action := Action{
			Plugin:      running.ID,
			Name:        descriptor.GetName(),
			Description: descriptor.GetDescription(),
			Class:       classOf(descriptor.GetClass()),
			ProofFrom:   descriptor.GetProofFrom(),
			Async:       descriptor.GetAsync(),
			Kinds:       descriptor.GetAppliesToKinds(),
			Enabled:     slices.Contains(enabled, descriptor.GetName()),
		}
		if schema := descriptor.GetParamsSchema(); schema != nil {
			action.Params = schema.AsMap()
		}
		for _, next := range descriptor.GetThen() {
			action.Then = append(action.Then, next.GetAction())
		}
		actions = append(actions, action)
	}

	for i := range actions {
		actions[i].Approval = approvalFor(actions[i], actions)
	}
	return actions
}

// approvalFor takes the strongest class anywhere in an action's declared chain.
// A composition is only as safe as its most destructive step, so approving one
// has to mean approving every class in it rather than only the first.
func approvalFor(action Action, all []Action) string {
	seen := map[string]bool{}

	var strongest func(Action) string
	strongest = func(current Action) string {
		if seen[current.Name] {
			return current.Class
		}
		seen[current.Name] = true

		class := current.Class
		for _, name := range current.Then {
			for _, candidate := range all {
				if candidate.Name != name {
					continue
				}
				if strongest(candidate) == ClassDestructive {
					class = ClassDestructive
				}
			}
		}
		return class
	}

	if strongest(action) == ClassDestructive {
		return ApprovalConfirm
	}
	return ApprovalAutomatic
}

// Actions returns what running plugins offer for an entity kind.
func (m *Manager) Actions(kind string) []Action {
	var offered []Action
	for _, running := range m.snapshot() {
		for _, action := range m.actionsFor(running) {
			if action.EntityScoped() && slices.Contains(action.Kinds, kind) {
				offered = append(offered, action)
			}
		}
	}

	slices.SortFunc(offered, func(a, b Action) int {
		return strings.Compare(a.Plugin+"/"+a.Name, b.Plugin+"/"+b.Name)
	})
	return offered
}

// PluginActions returns everything one plugin offers, entity-scoped or not, so
// its own page can show what it can do with nothing selected.
func (m *Manager) PluginActions(id string) []Action {
	m.mu.Lock()
	running, ok := m.running[id]
	m.mu.Unlock()

	if !ok {
		return nil
	}
	return m.actionsFor(running)
}

func (m *Manager) actionsFor(running *Running) []Action {
	var enabled []string
	if record, err := m.Store.Read(running.ID); err == nil {
		enabled = record.Enabled
	}
	return actionsOf(running, enabled)
}

func (m *Manager) snapshot() []*Running {
	m.mu.Lock()
	defer m.mu.Unlock()

	running := make([]*Running, 0, len(m.running))
	for _, plugin := range m.running {
		running = append(running, plugin)
	}
	slices.SortFunc(running, func(a, b *Running) int { return strings.Compare(a.ID, b.ID) })
	return running
}

// Report is what one installed plugin is doing, answered from what is running
// here rather than from the marketplace, so asking costs no network.
type Report struct {
	ID      string   `json:"id"`
	Version string   `json:"version"`
	Running bool     `json:"running"`
	Health  []Health `json:"health,omitempty"`
	Actions []Action `json:"actions,omitempty"`

	// Process is why it is not running, when it is not. An agent told only
	// "not running" cannot tell a plugin nobody installed properly from one
	// that is thirty seconds into coming back.
	Process *Process `json:"process,omitempty"`
}

// Failing reports whether every one of this plugin's configurations errored on
// its last run, which is the difference between one unreachable source and a
// plugin that is broken.
func (r Report) Failing() bool {
	if len(r.Health) == 0 {
		return false
	}
	for _, health := range r.Health {
		if health.Problem == "" {
			return false
		}
	}
	return true
}

// Report answers plugin health without reaching the marketplace, so the agent
// surface can see a broken plugin rather than that being a UI-only answer.
func (m *Manager) Report() []Report {
	installed, err := m.Store.List()
	if err != nil {
		m.log().Error("could not read installed plugins", "error", err)
		return nil
	}

	m.mu.Lock()
	running := maps.Clone(m.running)
	m.mu.Unlock()

	reports := make([]Report, 0, len(installed))
	for _, record := range installed {
		report := Report{ID: record.ID, Version: record.Version}

		m.mu.Lock()
		report.Process = m.process(record.ID)
		if live, ok := running[record.ID]; ok {
			report.Running = serving(report.Process)
			report.Version = live.Version
			report.Actions = actionsOf(live, record.Enabled)
			report.Health = m.healthOf(record.ID)
		}
		m.mu.Unlock()

		reports = append(reports, report)
	}

	slices.SortFunc(reports, func(a, b Report) int { return strings.Compare(a.ID, b.ID) })
	return reports
}

// Enable turns an action on or off. Denied by default is only meaningful if
// turning one on is a separate, recorded decision (ADR-0015).
func (m *Manager) Enable(id, name string, on bool) error {
	record, err := m.Store.Read(id)
	if err != nil {
		return fmt.Errorf("plugin: %q is not installed", id)
	}

	described, running := m.Describe(id)
	if !running {
		return fmt.Errorf("plugin: %s is not running, so what it can do is not known", id)
	}
	if !declares(described, name) {
		return fmt.Errorf("plugin: %s declares no action named %q", id, name)
	}

	already := slices.Contains(record.Enabled, name)
	switch {
	case on && !already:
		record.Enabled = append(record.Enabled, name)
		slices.Sort(record.Enabled)
	case !on && already:
		record.Enabled = slices.DeleteFunc(record.Enabled, func(candidate string) bool { return candidate == name })
	default:
		return nil
	}
	return m.Store.Write(*record)
}

func declares(described *duskv1alpha1.DescribeResponse, name string) bool {
	for _, action := range described.GetActions() {
		if action.GetName() == name {
			return true
		}
	}
	return false
}

// chosen is one action resolved to the plugin, the instance and the entity it
// will run against.
type chosen struct {
	action   Action
	running  *Running
	instance string
	entity   *duskv1alpha1.Entity
}

// scope is the rotation name whose observations this instance owns, and
// therefore what is asked to look again after something changes.
func (c chosen) scope() string {
	return (&Instance{Running: c.running, instance: c.instance}).Name()
}

// resolve finds which plugin serves an action against a ref. The plugin that
// observed the entity wins: an action offered on a kind several plugins emit
// would otherwise be routed by whichever one sorted first.
func (m *Manager) resolve(ctx context.Context, request Request) (*chosen, error) {
	if request.Ref == "" {
		return m.resolvePluginScoped(request)
	}
	if m.Catalog == nil {
		return nil, fmt.Errorf("plugin: no catalog is wired up, so %q cannot be resolved", request.Ref)
	}

	entity, err := m.Catalog.Get(ctx, "", request.Ref)
	if err != nil {
		return nil, fmt.Errorf("plugin: %q is not in the catalog, so nothing can be done to it", request.Ref)
	}

	observers, err := m.Catalog.ObservedBy(ctx, "", request.Ref)
	if err != nil {
		return nil, err
	}

	var candidates []chosen
	for _, running := range m.snapshot() {
		if request.Plugin != "" && running.ID != request.Plugin {
			continue
		}
		for _, action := range m.actionsFor(running) {
			if action.Name != request.Action || !slices.Contains(action.Kinds, entity.GetKind()) {
				continue
			}
			candidates = append(candidates, chosen{
				action:   action,
				running:  running,
				instance: instanceObserving(running.ID, observers),
				entity:   entity,
			})
		}
	}

	return pick(candidates, observers, request, entity.GetKind())
}

func pick(candidates []chosen, observers []string, request Request, kind string) (*chosen, error) {
	switch len(candidates) {
	case 0:
		return nil, fmt.Errorf("plugin: nothing running offers %q on a %s. Read the entity to see what can be done to it", request.Action, kind)
	case 1:
		return &candidates[0], nil
	}

	var watching []chosen
	for _, candidate := range candidates {
		if observedBy(candidate.running.ID, observers) {
			watching = append(watching, candidate)
		}
	}
	if len(watching) == 1 {
		return &watching[0], nil
	}

	named := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		named = append(named, candidate.running.ID)
	}
	return nil, fmt.Errorf("plugin: %s is offered on %q by %s. Name which one",
		request.Action, request.Ref, strings.Join(named, ", "))
}

func (m *Manager) resolvePluginScoped(request Request) (*chosen, error) {
	if request.Plugin == "" {
		return nil, fmt.Errorf("plugin: %q names no entity, so it needs the plugin it belongs to", request.Action)
	}

	m.mu.Lock()
	running, ok := m.running[request.Plugin]
	m.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("plugin: %s is not running", request.Plugin)
	}

	for _, action := range m.actionsFor(running) {
		if action.Name != request.Action {
			continue
		}
		if action.EntityScoped() {
			return nil, fmt.Errorf("plugin: %s applies to %s, so it needs the ref of one",
				action.Name, strings.Join(action.Kinds, " or "))
		}
		return &chosen{action: action, running: running}, nil
	}
	return nil, fmt.Errorf("plugin: %s declares no action named %q", request.Plugin, request.Action)
}

// instanceObserving picks which of a plugin's configurations saw this entity,
// so an action reaches the cluster the target actually lives in.
func instanceObserving(id string, observers []string) string {
	prefix := "plugin:" + id
	for _, observer := range observers {
		if observer == prefix {
			return ""
		}
		if instance, ok := strings.CutPrefix(observer, prefix+":"); ok {
			return instance
		}
	}
	return ""
}

func observedBy(id string, observers []string) bool {
	prefix := "plugin:" + id
	for _, observer := range observers {
		if observer == prefix || strings.HasPrefix(observer, prefix+":") {
			return true
		}
	}
	return false
}

// config is the configuration of the instance an action runs against, so a
// plugin watching two sources acts on the one the target lives in.
func (m *Manager) config(id, instance string) (*structpb.Struct, error) {
	record, err := m.Store.Read(id)
	if err != nil {
		return nil, fmt.Errorf("plugin: %q is not installed", id)
	}

	secrets, err := m.Store.ReadSecrets(id)
	if err != nil {
		return nil, err
	}

	plain := record.Config
	if instance != "" {
		plain = record.Instances[instance]
	}
	return configured(plain, secrets.For(instance))
}
