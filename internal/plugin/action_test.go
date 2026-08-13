package plugin_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/events"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// catalog answers what an action needs to resolve: what the entity is, and
// which scope observed it.
type catalog struct {
	kind      string
	version   string
	observers []string
	missing   bool
}

func (c *catalog) Get(_ context.Context, _, ref string) (*duskv1alpha1.Entity, error) {
	if c.missing {
		return nil, errors.New("no such entity")
	}
	return &duskv1alpha1.Entity{
		Ref:        ref,
		Kind:       c.kind,
		Provenance: &duskv1alpha1.Provenance{Version: c.version},
	}, nil
}

func (c *catalog) ObservedBy(context.Context, string, string) ([]string, error) {
	return c.observers, nil
}

// acting installs one plugin declaring the given actions, starts it, enables
// every one of them, and returns everything a test needs to invoke.
func acting(t *testing.T, spec standIn, observed *catalog) (*plugin.Manager, *proof.Store, *events.Log, *rota) {
	t.Helper()

	manager, rotation := manager(t)
	tokens := &proof.Store{}
	log := &events.Log{}

	manager.Catalog = observed
	manager.Proof = tokens
	manager.Events = log

	install(t, manager.Store, spec)
	manager.Restore(t.Context())
	t.Cleanup(manager.Stop)

	for _, action := range spec.Actions {
		if err := manager.Enable(spec.ID, action.Name, true); err != nil {
			t.Fatalf("enable %s: %v", action.Name, err)
		}
	}
	return manager, tokens, log, rotation
}

// read issues the token a get would, so an invocation can present one.
func read(tokens *proof.Store, ref, version string) string {
	return tokens.Issue(proof.FromGet, map[string]string{ref: version}).ID
}

const (
	readOnly    = int32(duskv1alpha1.ActionClass_ACTION_CLASS_READ_ONLY)
	mutating    = int32(duskv1alpha1.ActionClass_ACTION_CLASS_MUTATING)
	destructive = int32(duskv1alpha1.ActionClass_ACTION_CLASS_DESTRUCTIVE)
)

func oneAction(id string, class int32) standIn {
	return standIn{
		ID:      id,
		Kinds:   []string{"widget"},
		Actions: []standInAction{{Name: "poke", Class: class, Kinds: []string{"widget"}}},
	}
}

// ADR-0015: declared actions are denied by default. Installing a plugin must
// not silently grant capability, so enabling one is a separate decision.
func TestADR0015_AnActionIsDeniedUntilSomebodyEnablesIt(t *testing.T) {
	manager, rotation := manager(t)
	manager.Catalog = &catalog{kind: "widget", version: "v1", observers: []string{"plugin:denied"}}
	manager.Proof = &proof.Store{}
	manager.Events = &events.Log{}
	_ = rotation

	install(t, manager.Store, oneAction("denied", readOnly))
	manager.Restore(t.Context())
	t.Cleanup(manager.Stop)

	_, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:denied/one", Action: "poke"})
	if err == nil {
		t.Fatal("expected a declared but unenabled action to be refused")
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("the refusal should say it is not enabled, got %q", err)
	}

	if err := manager.Enable("denied", "poke", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:denied/one", Action: "poke"}); err != nil {
		t.Fatalf("expected an enabled action to run, got %v", err)
	}
}

// ADR-0009 through ADR-0015: you cannot change what you have not read, and the
// action names which read satisfies it.
func TestADR0015_AMutatingActionNeedsProofOfTheReadItNames(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:proven"}}
	manager, tokens, _, _ := acting(t, oneAction("proven", mutating), observed)

	ref := "widget:proven/one"

	if _, err := manager.Invoke(t.Context(), plugin.Request{Ref: ref, Action: "poke"}); err == nil {
		t.Fatal("expected a mutating action with no proof token to be refused")
	}

	stale := tokens.Issue(proof.FromGet, map[string]string{ref: "v0"}).ID
	_, err := manager.Invoke(t.Context(), plugin.Request{Ref: ref, Action: "poke", Proof: stale})
	if err == nil {
		t.Fatal("expected a token from a read of an older version to be refused")
	}

	if _, err := manager.Invoke(t.Context(), plugin.Request{
		Ref: ref, Action: "poke", Proof: read(tokens, ref, "v1"),
	}); err != nil {
		t.Fatalf("expected a current token to be accepted, got %v", err)
	}
}

func TestAReadOnlyActionNeedsNoProof(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:looking"}}
	manager, _, _, _ := acting(t, oneAction("looking", readOnly), observed)

	if _, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:looking/one", Action: "poke"}); err != nil {
		t.Fatalf("a read-only action should not need proof, got %v", err)
	}
}

// ADR-0015: class drives approval. Destructive means this particular run has to
// be agreed to, with the preview or its absence put in front of whoever agrees.
func TestADR0015_ADestructiveActionNeedsThisRunConfirmed(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:sharp"}}
	manager, tokens, _, _ := acting(t, oneAction("sharp", destructive), observed)

	ref := "widget:sharp/one"
	_, err := manager.Invoke(t.Context(), plugin.Request{Ref: ref, Action: "poke", Proof: read(tokens, ref, "v1")})
	if !errors.Is(err, plugin.ErrNeedsApproval) {
		t.Fatalf("expected a destructive action to need approval, got %v", err)
	}
	if !strings.Contains(err.Error(), "cannot be previewed") {
		t.Fatalf("the refusal should say there is no preview, got %q", err)
	}

	outcome, err := manager.Invoke(t.Context(), plugin.Request{
		Ref: ref, Action: "poke", Proof: read(tokens, ref, "v1"), Confirm: true,
	})
	if err != nil {
		t.Fatalf("expected a confirmed run to go ahead, got %v", err)
	}
	if !outcome.OK {
		t.Fatalf("expected it to succeed, got %q", outcome.Message)
	}
}

func TestARefusalCarriesThePreviewWhenThereIsOne(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:previewed"}}
	spec := oneAction("previewed", destructive)
	spec.Actions[0].Preview = true
	manager, tokens, _, _ := acting(t, spec, observed)

	ref := "widget:previewed/one"
	_, err := manager.Invoke(t.Context(), plugin.Request{Ref: ref, Action: "poke", Proof: read(tokens, ref, "v1")})
	if !strings.Contains(err.Error(), "would would poke "+ref) {
		t.Fatalf("the refusal should carry what the dry run said, got %q", err)
	}
}

// A composition is only as safe as its most destructive step, so a harmless
// first action that leads to a destructive one is confirmed like one.
func TestADR0015_ApprovalCoversTheWholeChain(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:chained"}}
	manager, tokens, _, _ := acting(t, standIn{
		ID:    "chained",
		Kinds: []string{"widget"},
		Actions: []standInAction{
			{Name: "start", Class: mutating, Kinds: []string{"widget"}, Then: []string{"finish"}},
			{Name: "finish", Class: destructive, Kinds: []string{"widget"}},
		},
	}, observed)

	ref := "widget:chained/one"
	_, err := manager.Invoke(t.Context(), plugin.Request{Ref: ref, Action: "start", Proof: read(tokens, ref, "v1")})
	if !errors.Is(err, plugin.ErrNeedsApproval) {
		t.Fatalf("a chain reaching a destructive step should need approval, got %v", err)
	}
	if !strings.Contains(err.Error(), "then finish") {
		t.Fatalf("the refusal should name what follows, got %q", err)
	}
}

func TestAnActionRoutesToThePluginThatObservedTheEntity(t *testing.T) {
	manager, rotation := manager(t)
	manager.Catalog = &catalog{kind: "widget", version: "v1", observers: []string{"plugin:second"}}
	manager.Proof = &proof.Store{}
	manager.Events = &events.Log{}
	_ = rotation

	for _, id := range []string{"first", "second"} {
		install(t, manager.Store, oneAction(id, readOnly))
	}
	manager.Restore(t.Context())
	t.Cleanup(manager.Stop)

	for _, id := range []string{"first", "second"} {
		if err := manager.Enable(id, "poke", true); err != nil {
			t.Fatalf("enable %s: %v", id, err)
		}
	}

	outcome, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:x/one", Action: "poke"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if outcome.Plugin != "second" {
		t.Fatalf("expected the plugin that observed it to serve the action, got %q", outcome.Plugin)
	}
}

func TestAnAmbiguousActionAsksWhichPlugin(t *testing.T) {
	manager, _ := manager(t)
	manager.Catalog = &catalog{kind: "widget", version: "v1"}
	manager.Proof = &proof.Store{}
	manager.Events = &events.Log{}

	for _, id := range []string{"alpha", "beta"} {
		install(t, manager.Store, oneAction(id, readOnly))
	}
	manager.Restore(t.Context())
	t.Cleanup(manager.Stop)

	for _, id := range []string{"alpha", "beta"} {
		if err := manager.Enable(id, "poke", true); err != nil {
			t.Fatalf("enable %s: %v", id, err)
		}
	}

	_, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:x/one", Action: "poke"})
	if err == nil || !strings.Contains(err.Error(), "Name which one") {
		t.Fatalf("expected an ambiguous action to ask which plugin, got %v", err)
	}

	if _, err := manager.Invoke(t.Context(), plugin.Request{
		Ref: "widget:x/one", Action: "poke", Plugin: "beta",
	}); err != nil {
		t.Fatalf("naming the plugin should resolve it, got %v", err)
	}
}

// An action has to run against the configuration of the instance that observed
// the entity: a plugin watching two clusters must act on the right one.
func TestAnActionRunsAgainstTheInstanceThatObservedTheEntity(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:many:staging"}}
	manager, _, _, _ := acting(t, standIn{
		ID:      "many",
		Kinds:   []string{"widget"},
		Fields:  []string{"cluster"},
		Actions: []standInAction{{Name: "poke", Class: readOnly, Kinds: []string{"widget"}}},
	}, observed)

	if err := manager.Configure(t.Context(), "many", "", map[string]any{"cluster": "production"}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if err := manager.Configure(t.Context(), "many", "staging", map[string]any{"cluster": "staging"}); err != nil {
		t.Fatalf("configure the instance: %v", err)
	}

	outcome, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:many/one", Action: "poke"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	config, _ := outcome.Detail["config"].(map[string]any)
	if config["cluster"] != "staging" {
		t.Fatalf("the action ran against the wrong configuration: %v", outcome.Detail)
	}
}

func TestACompositionStepTheDescriptorDidNotDeclareIsRefused(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:sneaky"}}
	manager, _, _, _ := acting(t, standIn{
		ID:    "sneaky",
		Kinds: []string{"widget"},
		Actions: []standInAction{
			{Name: "start", Class: readOnly, Kinds: []string{"widget"}, Produces: []string{"wipe"}},
			{Name: "wipe", Class: destructive, Kinds: []string{"widget"}},
		},
	}, observed)

	outcome, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:sneaky/one", Action: "start"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(outcome.Steps) != 1 {
		t.Fatalf("expected the undeclared step to be recorded, got %d", len(outcome.Steps))
	}
	if outcome.Steps[0].OK {
		t.Fatal("an undeclared step must not run: approving a chain has to mean approving what was declared")
	}
	if !strings.Contains(outcome.Steps[0].Message, "did not declare") {
		t.Fatalf("the refusal should say why, got %q", outcome.Steps[0].Message)
	}
}

// Composing with something absent has to report that it cannot run. A silently
// skipped step is the failure mode the whole mechanism exists to prevent.
func TestAMissingLinkIsReportedRatherThanSkipped(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:lonely"}}
	manager, _, _, _ := acting(t, standIn{
		ID:    "lonely",
		Kinds: []string{"widget"},
		Actions: []standInAction{{
			Name: "start", Class: readOnly, Kinds: []string{"widget"},
			Then: []string{"elsewhere"}, Produces: []string{"elsewhere"},
		}},
	}, observed)

	outcome, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:lonely/one", Action: "start"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(outcome.Steps) != 1 || outcome.Steps[0].OK {
		t.Fatalf("expected the missing step to be reported as failed, got %+v", outcome.Steps)
	}
	if !strings.Contains(outcome.Steps[0].Message, "nothing running offers") {
		t.Fatalf("the report should name what is absent, got %q", outcome.Steps[0].Message)
	}
}

func TestEveryInvocationInACompositionSharesAChain(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:linked"}}
	manager, _, log, _ := acting(t, standIn{
		ID:    "linked",
		Kinds: []string{"widget"},
		Actions: []standInAction{
			{Name: "start", Class: readOnly, Kinds: []string{"widget"}, Then: []string{"follow"}, Produces: []string{"follow"}},
			{Name: "follow", Class: readOnly, Kinds: []string{"widget"}},
		},
	}, observed)

	outcome, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:linked/one", Action: "start"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if len(outcome.Steps) != 1 || !outcome.Steps[0].OK {
		t.Fatalf("expected the declared step to run, got %+v", outcome.Steps)
	}

	chains := map[string]int{}
	for _, event := range log.Recent(0) {
		chains[event.GetChain()]++
	}
	if len(chains) != 1 {
		t.Fatalf("every invocation in one composition should share a chain, got %v", chains)
	}
	if chains[outcome.Chain] != 2 {
		t.Fatalf("expected two invocations tied together, got %v", chains)
	}
}

func TestAnAsyncActionReturnsAHandleThatStatusPolls(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:slow"}}
	spec := oneAction("slow", readOnly)
	spec.Actions[0].Async = true
	manager, _, _, _ := acting(t, spec, observed)

	outcome, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:slow/one", Action: "poke"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if outcome.Done || outcome.Handle == "" {
		t.Fatalf("expected an asynchronous action to return a handle, got %+v", outcome)
	}

	status, err := manager.Status(t.Context(), "slow", outcome.Handle)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Done || !status.OK {
		t.Fatalf("expected the handle to report completion, got %+v", status)
	}
}

func TestPreviewingNeedsNoProofBecauseItChangesNothing(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:peek"}}
	spec := oneAction("peek", destructive)
	spec.Actions[0].Preview = true
	manager, _, _, _ := acting(t, spec, observed)

	outcome, err := manager.Preview(t.Context(), plugin.Request{Ref: "widget:peek/one", Action: "poke"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !outcome.Previewed || !strings.Contains(outcome.Preview, "would poke") {
		t.Fatalf("expected the preview, got %+v", outcome)
	}
}

// The catalog would otherwise serve a view from before the action ran, until
// the instance's interval came round.
func TestAMutatingActionAsksItsInstanceToLookAgain(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:refresh"}}
	manager, tokens, _, rotation := acting(t, oneAction("refresh", mutating), observed)

	ref := "widget:refresh/one"
	if _, err := manager.Invoke(t.Context(), plugin.Request{
		Ref: ref, Action: "poke", Proof: read(tokens, ref, "v1"),
	}); err != nil {
		t.Fatalf("invoke: %v", err)
	}

	if !rotation.wasDue("plugin:refresh") {
		t.Fatal("expected the observing instance to be asked to look again")
	}
}

func TestAnInvocationIsRecordedAsAnEvent(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:recorded"}}
	manager, _, log, _ := acting(t, oneAction("recorded", readOnly), observed)

	if _, err := manager.Invoke(t.Context(), plugin.Request{
		Ref: "widget:recorded/one", Action: "poke", Actor: "someone",
	}); err != nil {
		t.Fatalf("invoke: %v", err)
	}

	recent := log.Recent(0)
	if len(recent) != 1 {
		t.Fatalf("expected one event, got %d", len(recent))
	}
	if recent[0].GetStatus() != duskv1alpha1.EventStatus_EVENT_STATUS_SUCCEEDED {
		t.Fatalf("expected it to record success, got %s", recent[0].GetStatus())
	}
	if recent[0].GetActor() != "someone" || recent[0].GetPlugin() != "recorded" {
		t.Fatalf("the event should say who asked and what served it, got %+v", recent[0])
	}
}

// A plugin asking for input gets it, and the answer reaches the same action on
// the turn that follows.
func TestAPluginCanAskTheInvokerForInput(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:asker:"}}
	manager, _, _, _ := acting(t, standIn{
		ID:      "asker",
		Kinds:   []string{"widget"},
		Actions: []standInAction{{Name: "poke", Class: readOnly, Kinds: []string{"widget"}, Asks: "reason"}},
	}, observed)

	var asked plugin.Ask
	outcome, err := manager.Invoke(t.Context(), plugin.Request{
		Ref: "widget:asker/one", Action: "poke",
		Elicit: func(_ context.Context, ask plugin.Ask) (plugin.Answer, error) {
			asked = ask
			return plugin.Answer{Outcome: plugin.Accepted, Values: map[string]any{"reason": "because"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	if asked.Prompt != "what is the reason?" {
		t.Errorf("prompt = %q, want the plugin's own question", asked.Prompt)
	}
	if asked.Schema["type"] != "object" {
		t.Errorf("schema = %v, want the plugin's schema passed through", asked.Schema)
	}
	if !strings.Contains(outcome.Message, `accept: "because"`) {
		t.Errorf("message = %q, want the answer to have reached the plugin", outcome.Message)
	}
}

// A surface with nobody attached must not hang or fail on the plugin's behalf.
// The plugin is told nobody can answer and decides for itself, which is what
// keeps one declaration usable from the UI, a chain and a schedule (ADR-0046).
func TestADR0046_AnUnattachedSurfaceAnswersRatherThanHanging(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:asker:"}}
	manager, _, _, _ := acting(t, standIn{
		ID:      "asker",
		Kinds:   []string{"widget"},
		Actions: []standInAction{{Name: "poke", Class: readOnly, Kinds: []string{"widget"}, Asks: "reason"}},
	}, observed)

	outcome, err := manager.Invoke(t.Context(), plugin.Request{Ref: "widget:asker/one", Action: "poke"})
	if err != nil {
		t.Fatalf("invoke with nobody to ask: %v", err)
	}
	if !outcome.OK {
		t.Fatalf("the action failed instead of being told nobody could answer: %+v", outcome)
	}
	if !strings.Contains(outcome.Message, plugin.Unanswerable) {
		t.Errorf("message = %q, want the plugin to have been told %q", outcome.Message, plugin.Unanswerable)
	}
}

// A plugin that keeps asking however it is answered is looping, and Dusk stops
// it rather than putting the same question to somebody forever.
func TestAnEndlessElicitationIsStopped(t *testing.T) {
	observed := &catalog{kind: "widget", version: "v1", observers: []string{"plugin:asker:"}}
	manager, _, _, _ := acting(t, standIn{
		ID:    "asker",
		Kinds: []string{"widget"},
		Actions: []standInAction{{
			Name: "poke", Class: readOnly, Kinds: []string{"widget"},
			Asks: "reason", Insists: true,
		}},
	}, observed)

	var asks int
	_, err := manager.Invoke(t.Context(), plugin.Request{
		Ref: "widget:asker/one", Action: "poke",
		Elicit: func(_ context.Context, _ plugin.Ask) (plugin.Answer, error) {
			asks++
			return plugin.Answer{Outcome: plugin.Accepted, Values: map[string]any{"reason": "again"}}, nil
		},
	})
	if err == nil {
		t.Fatal("an endlessly asking plugin was not stopped")
	}
	if asks > 8 {
		t.Errorf("the human was asked %d times before it stopped", asks)
	}
}
