package mcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
)

// offering is a plugin manager that offers one action and records what was
// asked of it, so a test asserts on the agent surface rather than on gRPC.
type offering struct {
	actions []plugin.Action
	reports []plugin.Report

	asked   plugin.Request
	outcome *plugin.Outcome
	err     error

	settings   map[string]any
	fields     []plugin.Field
	configured map[string]any
}

func (o *offering) Actions(string) []plugin.Action       { return o.actions }
func (o *offering) PluginActions(string) []plugin.Action { return o.actions }
func (o *offering) Report() []plugin.Report              { return o.reports }

func (o *offering) Invoke(_ context.Context, request plugin.Request) (*plugin.Outcome, error) {
	o.asked = request
	return o.outcome, o.err
}

func (o *offering) Preview(ctx context.Context, request plugin.Request) (*plugin.Outcome, error) {
	return o.Invoke(ctx, request)
}

func (o *offering) Settings(string, string) (map[string]any, []plugin.Field, error) {
	return o.settings, o.fields, nil
}

func (o *offering) Configure(_ context.Context, _, _ string, settings map[string]any) error {
	o.configured = settings
	return nil
}

type acted struct {
	session *sdk.ClientSession
	index   *index.DB
}

func acting(t *testing.T, plugins mcp.Plugins) *acted {
	t.Helper()

	idx := newIndex(t)
	return &acted{
		session: serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test", Plugins: plugins})),
		index:   idx,
	}
}

// ADR-0041: a plugin's capability reaches an agent through the tools that
// already exist, so installing one adds exactly one tool and never more.
func TestADR0041_APluginAddsOneToolAndNoMore(t *testing.T) {
	acting := acting(t, &offering{})

	tools, err := acting.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}

	want := "changes,configure,drift,dusk_context,get,invoke,neighbors,note,search"
	if got := joinSorted(names); got != want {
		t.Errorf("tools = %s, want %s", got, want)
	}
}

// What can be done to a thing is part of the picture of that thing, which is
// the whole of ADR-0041's discovery story.
func TestADR0041_GetSaysWhatCanBeDoneToTheEntity(t *testing.T) {
	plugins := &offering{actions: []plugin.Action{
		{
			Plugin: "airtrail", Name: "delete_flight", Description: "Remove it from the logbook.",
			Class: plugin.ClassDestructive, Approval: plugin.ApprovalConfirm,
			ProofFrom: "get", Enabled: true, Kinds: []string{"service"},
		},
		{
			Plugin: "airtrail", Name: "hidden", Description: "Not enabled.",
			Class: plugin.ClassReadOnly, Enabled: false, Kinds: []string{"service"},
		},
	}}

	acting := acting(t, plugins)
	seed(t, acting.index)

	body := call(t, acting.session, "get", map[string]any{"ref": "service:home/jellyfin"})

	for _, want := range []string{"## Actions", "delete_flight", "destructive", "Needs `confirm`", "proof token from `get`", "`invoke`"} {
		if !strings.Contains(body, want) {
			t.Errorf("get did not mention %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "hidden") {
		t.Errorf("an action nobody enabled must not be offered:\n%s", body)
	}
}

func TestInvokeCarriesEverythingTheActionNeeds(t *testing.T) {
	plugins := &offering{outcome: &plugin.Outcome{
		Action: "delete_flight", Plugin: "airtrail", Ref: "flight:airtrail/one",
		Done: true, OK: true, Message: "removed it",
	}}

	acting := acting(t, plugins)
	body := call(t, acting.session, "invoke", map[string]any{
		"ref": "flight:airtrail/one", "action": "delete_flight",
		"proof": "token-1", "confirm": true,
		"params": map[string]any{"reason": "duplicate"},
	})

	if plugins.asked.Ref != "flight:airtrail/one" || plugins.asked.Action != "delete_flight" {
		t.Fatalf("the request did not reach the manager intact: %+v", plugins.asked)
	}
	if plugins.asked.Proof != "token-1" || !plugins.asked.Confirm {
		t.Fatalf("the proof token and the confirmation were dropped: %+v", plugins.asked)
	}
	if plugins.asked.Params["reason"] != "duplicate" {
		t.Fatalf("the parameters were dropped: %+v", plugins.asked.Params)
	}
	if !strings.Contains(body, "removed it") {
		t.Fatalf("the answer should say what happened:\n%s", body)
	}
}

// Being asked to confirm is an answer, not a failure: an agent has to be able
// to put the question to whoever is watching.
func TestNeedingApprovalIsAnAnswerNotAnError(t *testing.T) {
	plugins := &offering{err: errors.New("plugin: needs approval: wipe is destructive")}
	plugins.err = errors.Join(plugin.ErrNeedsApproval, plugins.err)

	acting := acting(t, plugins)
	body := call(t, acting.session, "invoke", map[string]any{"ref": "x:y/z", "action": "wipe"})

	if !strings.Contains(body, "destructive") {
		t.Fatalf("the answer should explain what is being agreed to:\n%s", body)
	}
}

// The surface meant to be primary could not see a broken plugin, which made
// plugin health a UI-only answer.
func TestChangesReportsPluginHealth(t *testing.T) {
	plugins := &offering{reports: []plugin.Report{
		{ID: "airtrail", Version: "v1.0.0", Running: true,
			Health: []plugin.Health{{Problem: "airtrail answered 502 Bad Gateway"}}},
		{ID: "kubernetes", Version: "v0.2.0", Running: false},
	}}

	body := call(t, acting(t, plugins).session, "changes", nil)

	for _, want := range []string{"## Plugins", "airtrail", "502 Bad Gateway", "kubernetes", "not running"} {
		if !strings.Contains(body, want) {
			t.Errorf("changes did not report %q:\n%s", want, body)
		}
	}
}

// ADR-0041: a secret passed as a tool argument is a secret written into the
// transcript, so it is refused rather than accepted and quietly dropped.
func TestConfigureRefusesASensitiveField(t *testing.T) {
	plugins := &offering{
		settings: map[string]any{"base_url": "https://example.com"},
		fields: []plugin.Field{
			{Name: "base_url", Type: "string"},
			{Name: "api_key", Type: "string", Sensitive: true},
		},
	}

	acting := acting(t, plugins)
	body := call(t, acting.session, "configure", map[string]any{
		"plugin":   "airtrail",
		"settings": map[string]any{"api_key": "s3cret"},
	})

	if plugins.configured != nil {
		t.Fatalf("a sensitive field must not be written from here, got %v", plugins.configured)
	}
	if !strings.Contains(body, "api_key") || !strings.Contains(body, "transcript") {
		t.Fatalf("the refusal should name the field and say why:\n%s", body)
	}
}

// An agent setting one field should not clear the rest, which is what sending
// a whole form does.
func TestConfigureMergesOverWhatIsAlreadyThere(t *testing.T) {
	plugins := &offering{
		settings: map[string]any{"base_url": "https://example.com", "namespace": "airtrail"},
		fields: []plugin.Field{
			{Name: "base_url", Type: "string"},
			{Name: "namespace", Type: "string"},
		},
	}

	acting := acting(t, plugins)
	call(t, acting.session, "configure", map[string]any{
		"plugin":   "airtrail",
		"settings": map[string]any{"namespace": "flights"},
	})

	if plugins.configured["namespace"] != "flights" {
		t.Fatalf("the edit did not land: %v", plugins.configured)
	}
	if plugins.configured["base_url"] != "https://example.com" {
		t.Fatalf("setting one field cleared another: %v", plugins.configured)
	}
}

func TestConfigureWithNoSettingsReadsThem(t *testing.T) {
	plugins := &offering{
		settings: map[string]any{"base_url": "https://example.com"},
		fields: []plugin.Field{
			{Name: "base_url", Type: "string"},
			{Name: "api_key", Type: "string", Sensitive: true},
		},
	}

	body := call(t, acting(t, plugins).session, "configure", map[string]any{"plugin": "airtrail"})

	if !strings.Contains(body, "https://example.com") {
		t.Fatalf("reading should show the plain values:\n%s", body)
	}
	if !strings.Contains(body, "never here") {
		t.Fatalf("reading should say where a credential is entered instead:\n%s", body)
	}
}
