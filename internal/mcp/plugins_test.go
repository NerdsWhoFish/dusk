package mcp_test

import (
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// installed is a marketplace: an observer with nothing to run, an actor about
// no entity, and one that does both.
func installed() *offering {
	return &offering{
		reports: []plugin.Report{
			{ID: "kubernetes", Version: "1.2.0", Running: true},
			{ID: "adr", Version: "0.1.3", Running: true},
			{ID: "planty", Version: "0.2.0", Running: true},
		},
		emits: map[string][]string{
			"kubernetes": {"cluster", "host", "service"},
			"planty":     {"plant"},
		},
		actions: []plugin.Action{
			{Plugin: "adr", Name: "render", Description: "Render a decision.", Class: plugin.ClassReadOnly, Enabled: true},
			{Plugin: "adr", Name: "supersede", Description: "Replace one.", Class: plugin.ClassMutating, Enabled: true},
			{Plugin: "adr", Name: "retire", Description: "Retire one.", Class: plugin.ClassMutating},
		},
	}
}

// ADR-0077: ADR-0041 wrote down as its own worst consequence that a plugin's
// capability cannot be seen from the tool list. This is the call that answers.
func TestADR0077_ThePluginToolListsEveryInstalledIntegration(t *testing.T) {
	acting := acting(t, installed())
	body := call(t, acting.session, "plugin", nil)

	for _, want := range []string{"kubernetes", "adr", "planty"} {
		if !strings.Contains(body, want) {
			t.Errorf("the roster never named %q:\n%s", want, body)
		}
	}
	// The observing half, which no agent surface used to show at all.
	if !strings.Contains(body, "`cluster`") || !strings.Contains(body, "`plant`") {
		t.Errorf("the roster never said what a plugin puts in the catalog:\n%s", body)
	}
	// The acting half, and only what somebody enabled (ADR-0015).
	if !strings.Contains(body, "`render`") {
		t.Errorf("the roster never said what can be run:\n%s", body)
	}
	if strings.Contains(body, "`retire`") {
		t.Errorf("the roster offered an action nobody enabled:\n%s", body)
	}
	if !strings.Contains(body, "`invoke`") {
		t.Errorf("the roster never named the call that runs one:\n%s", body)
	}
}

// A plugin that only observes is the case ADR-0076's narrowing could not reach:
// it has no action attached to any kind, and no action attached to none either.
func TestADR0077_AnObservingPluginIsNamedInTheContext(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test", Plugins: installed()}))
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	for _, want := range []string{"`kubernetes`", "`planty`", "`adr`"} {
		if !strings.Contains(body, want) {
			t.Errorf("the context never named the %s plugin:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "observes `cluster`") {
		t.Errorf("the context never said what a plugin observes:\n%s", body)
	}
}

// The manual names a tool if and only if the server registered it. ADR-0076
// tested one direction, so `page` was registered and unnamed from the day it
// was built, and every tool added later inherits that silence.
func TestADR0077_TheManualNamesEveryRegisteredTool(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	writer := &recordingWriter{notesGo: "example/config"}
	session := serve(t, mcp.New(mcp.Options{
		Catalog: idx, Version: "test", Plugins: installed(),
		Writer: writer, Tokens: &proof.Store{},
	}))
	body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot})

	for _, name := range toolNames(t, session) {
		// The call the reader is already inside, named by the instructions.
		if name == "dusk_context" {
			continue
		}
		if !strings.Contains(body, "`"+name+"`") {
			t.Errorf("the server registered %q and the manual never names it:\n%s", name, body)
		}
	}
}

// A deployment with no plugins registers no plugin tool, so naming one would
// send an agent at something that is not there (ADR-0057).
func TestADR0077_NoPluginsMeansNoPluginTool(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)

	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))

	for _, name := range toolNames(t, session) {
		if name == "plugin" {
			t.Fatal("a deployment with no plugins registered the plugin tool")
		}
	}
	if body := call(t, session, "dusk_context", map[string]any{"root": homelabRoot}); strings.Contains(body, "integrations") {
		t.Errorf("a deployment with no plugins talked about integrations anyway:\n%s", body)
	}
}

// One renderer behind both doors, so the page an agent reaches through a
// `plugin:` ref and the one it reaches by name cannot drift.
func TestADR0077_BothDoorsReachOnePluginPage(t *testing.T) {
	acting := acting(t, installed())

	named := call(t, acting.session, "plugin", map[string]any{"name": "adr"})
	byRef := call(t, acting.session, "get", map[string]any{"ref": "plugin:adr"})

	if named != byRef {
		t.Errorf("the two doors answered differently:\n%s\n---\n%s", named, byRef)
	}
	if !strings.Contains(named, "`render`") || !strings.Contains(named, "`supersede`") {
		t.Errorf("the page did not list what can be run:\n%s", named)
	}
}

// The page is where the whole answer lives, so nothing on it is capped: the
// roster defers to it, and a reader sent here must not be sent on again.
func TestADR0077_ThePluginPageSaysWhatItObserves(t *testing.T) {
	acting := acting(t, installed())
	body := call(t, acting.session, "plugin", map[string]any{"name": "kubernetes"})

	for _, want := range []string{"`cluster`", "`host`", "`service`"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page never named %s among what it observes:\n%s", want, body)
		}
	}
	// ADR-0015: declaring an action is not granting it, and this plugin
	// declares none at all, which is a different answer.
	if !strings.Contains(body, "declares no actions") {
		t.Errorf("the page did not say which kind of nothing it has to run:\n%s", body)
	}
}

// ADR-0059: an overflow says what it left out, and that count sits inside
// a list of identifiers. Quoted with them it reads as an action named
// "2 more", which an agent can try to invoke and Dusk will refuse.
func TestADR0059_ACappedListDoesNotDressItsRemainderAsAName(t *testing.T) {
	crowded := &offering{
		reports: []plugin.Report{{ID: "planty", Version: "0.1.0", Running: true}},
		emits:   map[string][]string{"planty": {"a", "b", "c", "d", "e", "f", "g", "h"}},
	}
	for i := range 10 {
		crowded.actions = append(crowded.actions, plugin.Action{
			Plugin: "planty", Name: string(rune('a' + i)), Class: plugin.ClassReadOnly, Enabled: true,
		})
	}

	idx := newIndex(t)
	seed(t, idx)
	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test", Plugins: crowded}))

	for _, body := range []string{
		call(t, session, "plugin", nil),
		call(t, session, "dusk_context", map[string]any{"root": homelabRoot}),
	} {
		if !strings.Contains(body, "more") {
			t.Fatalf("nothing was capped, so the test proves nothing:\n%s", body)
		}
		if strings.Contains(body, "`more") || strings.Contains(body, "more`") {
			t.Errorf("the remainder was rendered as an identifier:\n%s", body)
		}
	}
}

func TestADR0077_AnUnknownPluginNamesTheOnesThatExist(t *testing.T) {
	acting := acting(t, installed())
	body := call(t, acting.session, "plugin", map[string]any{"name": "spacelift"})

	if !strings.Contains(body, "adr") || !strings.Contains(body, "planty") {
		t.Errorf("an unknown plugin did not name the installed ones:\n%s", body)
	}
}
