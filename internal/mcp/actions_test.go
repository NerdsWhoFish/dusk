package mcp_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

	want := "changes,configure,drift,dusk_context,get,invoke,kinds,neighbors,note,search"
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

// ADR-0054: "not running" is not one answer. An agent has to be able to tell a
// plugin on its way back from one nothing is bringing back, because only the
// second is worth telling somebody about.
func TestChangesTellsARestartFromAPluginNobodyIsRestarting(t *testing.T) {
	tests := []struct {
		name    string
		process *plugin.Process
		says    []string
	}{
		{
			name:    "coming back",
			process: &plugin.Process{Phase: plugin.PhaseRestarting, Exit: "signal: killed", Restarts: 2},
			says:    []string{"being started again", "signal: killed"},
		},
		{
			name:    "given up on",
			process: &plugin.Process{Phase: plugin.PhaseFailed, Exit: "exit status 3", Attempts: 8},
			says:    []string{"no longer being restarted", "8 attempts", "exit status 3"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plugins := &offering{reports: []plugin.Report{
				{ID: "kubernetes", Version: "v0.2.0", Running: false, Process: test.process},
			}}

			body := call(t, acting(t, plugins).session, "changes", nil)
			for _, want := range test.says {
				if !strings.Contains(body, want) {
					t.Errorf("changes did not report %q:\n%s", want, body)
				}
			}
		})
	}
}

// A plugin that has been restarted is serving observations that are younger
// than the plugin, which is exactly what `changes` exists to disclose.
func TestChangesSaysWhenARunningPluginHasBeenRestarted(t *testing.T) {
	plugins := &offering{reports: []plugin.Report{{
		ID: "airtrail", Version: "v1.0.0", Running: true,
		Process: &plugin.Process{Phase: plugin.PhaseRunning, Restarts: 3, Since: time.Now()},
	}}}

	body := call(t, acting(t, plugins).session, "changes", nil)
	if !strings.Contains(body, "restarted 3 times") {
		t.Errorf("changes did not say the plugin had been restarted:\n%s", body)
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

// asking is a plugin that puts a question to whoever invoked it and reports
// what came back, which is the only way to see the elicitor from outside.
type asking struct {
	offering
	heard plugin.Answer
}

func (a *asking) Invoke(ctx context.Context, request plugin.Request) (*plugin.Outcome, error) {
	if request.Elicit == nil {
		a.heard = plugin.Answer{Outcome: plugin.Unanswerable}
		return &plugin.Outcome{Done: true, OK: true, Message: "nobody to ask"}, nil
	}
	answer, err := request.Elicit(ctx, plugin.Ask{Prompt: "why?"})
	if err != nil {
		return nil, err
	}
	a.heard = answer
	return &plugin.Outcome{Done: true, OK: true, Message: "heard " + answer.Outcome}, nil
}

// A client may declare the elicitation capability and then never answer. The
// wait has to end anyway: ADR-0046 bounded how many times a plugin may ask and
// not how long one ask may take, so an unanswered question hung the whole
// invocation for as long as the connection lived.
func TestAnUnansweredQuestionEndsRatherThanHanging(t *testing.T) {
	t.Cleanup(mcp.SetElicitPatience(200 * time.Millisecond))

	silent := &asking{offering: offering{
		actions: []plugin.Action{{
			Plugin: "asker", Name: "poke", Class: "read_only",
			Kinds: []string{"service"}, Enabled: true,
		}},
	}}

	httpServer := httptest.NewServer(mcp.New(mcp.Options{
		Catalog: newIndex(t), Version: "test", Plugins: silent,
	}).Handler())
	t.Cleanup(httpServer.Close)

	// A handler that declares the capability and then waits for the deadline
	// rather than answering, which is what a wedged client looks like.
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "test"}, &sdk.ClientOptions{
		ElicitationHandler: func(ctx context.Context, _ *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	session, err := client.Connect(t.Context(),
		&sdk.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	done := make(chan string, 1)
	go func() {
		result, err := session.CallTool(t.Context(), &sdk.CallToolParams{
			Name:      "invoke",
			Arguments: map[string]any{"ref": "service:home/thing", "action": "poke"},
		})
		if err != nil {
			done <- "error: " + err.Error()
			return
		}
		done <- fmt.Sprint(result.Content)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the invocation never returned, so an unanswered question still hangs it")
	}

	if silent.heard.Outcome != plugin.Unanswerable {
		t.Errorf("the plugin heard %q, want %q so it can decide for itself",
			silent.heard.Outcome, plugin.Unanswerable)
	}
}

// An action about the plugin rather than about one thing is listed on no
// entity, so without reading the plugin itself nothing on the agent surface
// can find it. ADR-0041 promises a capability reaches an agent through the
// tools that already exist, and this is the half that was missing.
func TestAPluginCanBeReadToFindWhatItOffers(t *testing.T) {
	standalone := plugin.Action{
		Plugin: "adr", Name: "next_number", Class: "read_only",
		Description: "The next free number.", Enabled: true,
	}
	acts := acting(t, &offering{
		actions: []plugin.Action{standalone},
		reports: []plugin.Report{{ID: "adr", Version: "0.1.0", Running: true}},
	})

	body := call(t, acts.session, "get", map[string]any{"ref": "plugin:adr"})
	for _, want := range []string{"adr", "0.1.0", "next_number", "The next free number."} {
		if !strings.Contains(body, want) {
			t.Errorf("reading the plugin did not mention %q:\n%s", want, body)
		}
	}

	// The generic renderer already says to use invoke; only this one knows the
	// action takes no ref. Saying both leaves the reader told twice.
	if strings.Contains(body, "Run one with `invoke`.") {
		t.Errorf("the plugin page kept the hint that does not know it is a plugin:\n%s", body)
	}
	if n := strings.Count(body, "`invoke`"); n != 1 {
		t.Errorf("the invoke hint appears %d times, want once:\n%s", n, body)
	}
}

// A plugin page listed everything the plugin offers and closed by saying to run
// one naming no ref. That is wrong for every entity-scoped action, and `invoke`
// refuses those by name, so the page was teaching a call that fails.
func TestAPluginPageSaysHowEachActionIsInvoked(t *testing.T) {
	entityScoped := []plugin.Action{
		{
			Plugin: "kubernetes", Name: "restart", Class: plugin.ClassMutating,
			Description: "Restart the workload.", Enabled: true, Kinds: []string{"service"},
		},
		{
			Plugin: "kubernetes", Name: "scale", Class: plugin.ClassMutating,
			Description: "Scale the workload.", Enabled: true, Kinds: []string{"service"},
		},
	}
	pluginScoped := plugin.Action{
		Plugin: "kubernetes", Name: "sync", Class: plugin.ClassMutating,
		Description: "Sync everything.", Enabled: true,
	}

	applies := "Run `restart` and `scale` with `invoke`, naming the ref of a service."
	noRef := "Run `sync` with `invoke`, naming this plugin and no ref."

	for _, tt := range []struct {
		name    string
		actions []plugin.Action
		says    []string
		quiet   []string
	}{
		{
			name:    "both halves",
			actions: append([]plugin.Action{pluginScoped}, entityScoped...),
			says:    []string{noRef, applies},
		},
		{
			name:    "entity scoped only",
			actions: entityScoped,
			says:    []string{applies},
			quiet:   []string{"naming this plugin and no ref"},
		},
		{
			name:    "plugin scoped only",
			actions: []plugin.Action{pluginScoped},
			says:    []string{noRef},
			quiet:   []string{"naming the ref of"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			acts := acting(t, &offering{
				actions: tt.actions,
				reports: []plugin.Report{{ID: "kubernetes", Version: "0.2.0", Running: true}},
			})

			body := call(t, acts.session, "get", map[string]any{"ref": "plugin:kubernetes"})
			for _, want := range tt.says {
				if !strings.Contains(body, want) {
					t.Errorf("the plugin page does not say %q:\n%s", want, body)
				}
			}
			for _, unwanted := range tt.quiet {
				if strings.Contains(body, unwanted) {
					t.Errorf("the plugin page still says %q:\n%s", unwanted, body)
				}
			}
		})
	}
}

// Nothing to run has two causes, and only one of them is fixable by whoever is
// reading: a plugin declaring actions nobody enabled needs a deliberate act.
func TestAPluginWithNothingToRunSaysWhichKindOfNothing(t *testing.T) {
	for _, tt := range []struct {
		name    string
		actions []plugin.Action
		says    string
	}{
		{name: "declares none", says: "declares no actions"},
		{
			name: "none enabled",
			actions: []plugin.Action{{
				Plugin: "kubernetes", Name: "restart", Class: plugin.ClassMutating,
				Kinds: []string{"service"},
			}},
			says: "enabled",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			acts := acting(t, &offering{
				actions: tt.actions,
				reports: []plugin.Report{{ID: "kubernetes", Version: "0.2.0", Running: true}},
			})

			body := call(t, acts.session, "get", map[string]any{"ref": "plugin:kubernetes"})
			if !strings.Contains(body, tt.says) {
				t.Errorf("the plugin page does not say %q:\n%s", tt.says, body)
			}
		})
	}
}

// Reading a plugin is where an agent finds out what it can be asked to do, so
// it is also where it has to find out that nothing can be asked of it at all.
func TestReadingAPluginSaysWhyItIsNotAnswering(t *testing.T) {
	acts := acting(t, &offering{
		reports: []plugin.Report{{
			ID: "adr", Version: "0.1.0", Running: false,
			Process: &plugin.Process{Phase: plugin.PhaseFailed, Attempts: 8, Exit: "exit status 3"},
		}},
	})

	body := call(t, acts.session, "get", map[string]any{"ref": "plugin:adr"})
	for _, want := range []string{"no longer being restarted", "exit status 3"} {
		if !strings.Contains(body, want) {
			t.Errorf("reading the plugin did not say %q:\n%s", want, body)
		}
	}
}

// Naming a plugin nobody installed has to say so and say what is there, rather
// than answering as though the question were about an entity.
func TestReadingAnUnknownPluginNamesTheOnesThatExist(t *testing.T) {
	acts := acting(t, &offering{
		reports: []plugin.Report{{ID: "adr", Version: "0.1.0", Running: true}},
	})

	body := call(t, acts.session, "get", map[string]any{"ref": "plugin:nope"})
	if !strings.Contains(body, "nope") || !strings.Contains(body, "adr") {
		t.Errorf("want a refusal naming what is installed, got:\n%s", body)
	}
}

// A plugin's detail is arbitrary structure. Rendered with Go's default
// formatting it arrives as `map[k:v]`, which is neither JSON nor prose, and a
// plugin whose whole answer is structured data becomes unreadable.
func TestStructuredDetailIsRenderedAsJSON(t *testing.T) {
	acts := acting(t, &offering{
		actions: []plugin.Action{{
			Plugin: "adr", Name: "validate", Class: "read_only",
			Kinds: []string{"service"}, Enabled: true,
		}},
		outcome: &plugin.Outcome{
			Plugin: "adr", Action: "validate", Done: true, OK: true, Message: "checked",
			Detail: map[string]any{
				"findings": []any{map[string]any{"rule": "rejected_missing", "number": "0045"}},
				"count":    float64(1),
			},
		},
	})

	body := call(t, acts.session, "invoke",
		map[string]any{"ref": "service:home/thing", "action": "validate"})

	if strings.Contains(body, "map[") {
		t.Errorf("detail was rendered with Go formatting:\n%s", body)
	}
	if !strings.Contains(body, `"rule": "rejected_missing"`) {
		t.Errorf("structured detail did not survive as JSON:\n%s", body)
	}
	if !strings.Contains(body, "- count: 1") {
		t.Errorf("a scalar should stay bare rather than becoming JSON:\n%s", body)
	}
}
