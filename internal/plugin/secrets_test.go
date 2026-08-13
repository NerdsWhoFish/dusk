package plugin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/plugin"
)

// configured installs a stand-in declaring one plain and one sensitive field,
// starts it, and applies config. It is the shape every test here starts from.
func configuredPlugin(t *testing.T, id string, config map[string]any) (*plugin.Manager, *rota) {
	t.Helper()

	manager, rotation := manager(t)
	install(t, manager.Store, standIn{
		ID:        id,
		Kinds:     []string{"thing"},
		Fields:    []string{"base_url", "api_key"},
		Sensitive: []string{"api_key"},
	})

	manager.Restore(t.Context())
	t.Cleanup(manager.Stop)

	if err := manager.Configure(t.Context(), id, config); err != nil {
		t.Fatalf("configure: %v", err)
	}
	return manager, rotation
}

func onDisk(t *testing.T, store *plugin.Store, id string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(store.Dir, id, "installed.json"))
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	return string(body)
}

// ADR-0023 splits configuration by sensitivity: the plain half is the record,
// the sensitive half is sealed. Before this, every plugin credential sat in
// readable JSON on the volume and was returned by the plugins API.
func TestADR0023_ASensitiveValueNeverReachesTheRecord(t *testing.T) {
	manager, _ := configuredPlugin(t, "sealed", map[string]any{
		"base_url": "https://example.com",
		"api_key":  "s3cret-value",
	})

	if record := onDisk(t, manager.Store, "sealed"); strings.Contains(record, "s3cret-value") {
		t.Fatalf("the credential is in the plain record:\n%s", record)
	}

	offers, err := manager.Available(t.Context())
	if err != nil {
		t.Fatalf("available: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("expected the installed plugin to be offered, got %d", len(offers))
	}

	shown, err := json.Marshal(offers[0])
	if err != nil {
		t.Fatalf("encode the offer: %v", err)
	}
	if strings.Contains(string(shown), "s3cret-value") {
		t.Fatalf("the credential is in what the API returns:\n%s", shown)
	}
	if offers[0].Config["base_url"] != "https://example.com" {
		t.Fatalf("the plain half should still be readable, got %v", offers[0].Config)
	}
}

// ADR-0023: a sensitive value reaches the plugin over its own socket, and
// nowhere else. Sealing it is worthless if the plugin never receives it.
func TestADR0023_ASensitiveValueReachesThePluginItself(t *testing.T) {
	_, rotation := configuredPlugin(t, "delivered", map[string]any{
		"base_url": "https://example.com",
		"api_key":  "s3cret-value",
	})

	observation := rotation.observed(t.Context(), t, "plugin:delivered")
	if len(observation.Entities) != 1 {
		t.Fatalf("expected one entity, got %d", len(observation.Entities))
	}

	got := observation.Entities[0].GetAttributes().AsMap()
	if got["api_key"] != "s3cret-value" {
		t.Fatalf("the plugin was not handed its credential, it received %v", got)
	}
	if got["base_url"] != "https://example.com" {
		t.Fatalf("the plugin was not handed its plain configuration, it received %v", got)
	}
}

func TestASensitiveFieldSubmittedEmptyKeepsWhatIsStored(t *testing.T) {
	manager, rotation := configuredPlugin(t, "kept", map[string]any{
		"base_url": "https://one.example.com",
		"api_key":  "s3cret-value",
	})

	// What a write-only form submits when the field was not retyped.
	if err := manager.Configure(t.Context(), "kept", map[string]any{
		"base_url": "https://two.example.com",
		"api_key":  "",
	}); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}

	got := rotation.observed(t.Context(), t, "plugin:kept").Entities[0].GetAttributes().AsMap()
	if got["api_key"] != "s3cret-value" {
		t.Fatalf("editing another field erased the credential, the plugin now has %v", got)
	}
	if got["base_url"] != "https://two.example.com" {
		t.Fatalf("the edit did not land, the plugin has %v", got)
	}
}

func TestASensitiveFieldSubmittedNullIsForgotten(t *testing.T) {
	manager, rotation := configuredPlugin(t, "forgotten", map[string]any{
		"base_url": "https://example.com",
		"api_key":  "s3cret-value",
	})

	if err := manager.Configure(t.Context(), "forgotten", map[string]any{
		"base_url": "https://example.com",
		"api_key":  nil,
	}); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}

	got := rotation.observed(t.Context(), t, "plugin:forgotten").Entities[0].GetAttributes().AsMap()
	if _, still := got["api_key"]; still {
		t.Fatalf("an explicitly forgotten credential is still being handed over: %v", got)
	}
}

func TestTheNamesOfSetSecretsAreReportedAndTheValuesAreNot(t *testing.T) {
	manager, _ := configuredPlugin(t, "named", map[string]any{
		"base_url": "https://example.com",
		"api_key":  "s3cret-value",
	})

	offers, err := manager.Available(t.Context())
	if err != nil {
		t.Fatalf("available: %v", err)
	}
	if got := offers[0].Set[""]; !slices.Equal(got, []string{"api_key"}) {
		t.Fatalf("expected api_key to be reported as set, got %v", got)
	}
}

// A plugin credential written by an older Dusk sits in the plain record. It has
// to move on the next start, or upgrading leaves it readable forever.
func TestACredentialWrittenInTheClearIsSealedOnTheNextStart(t *testing.T) {
	manager, rotation := manager(t)
	install(t, manager.Store, standIn{
		ID:        "legacy",
		Kinds:     []string{"thing"},
		Fields:    []string{"base_url", "api_key"},
		Sensitive: []string{"api_key"},
	})

	// The shape an older Dusk left behind: everything in one plain record.
	if err := manager.Store.Write(plugin.Installed{
		ID:      "legacy",
		Version: "v0.0.1",
		Config:  map[string]any{"base_url": "https://example.com", "api_key": "s3cret-value"},
	}); err != nil {
		t.Fatalf("write the old record: %v", err)
	}

	manager.Restore(t.Context())
	t.Cleanup(manager.Stop)

	if record := onDisk(t, manager.Store, "legacy"); strings.Contains(record, "s3cret-value") {
		t.Fatalf("the credential was left in the plain record:\n%s", record)
	}

	got := rotation.observed(t.Context(), t, "plugin:legacy").Entities[0].GetAttributes().AsMap()
	if got["api_key"] != "s3cret-value" {
		t.Fatalf("sealing the credential lost it, the plugin now has %v", got)
	}
}

func TestConfiguringAPluginThatIsNotRunningSaysWhy(t *testing.T) {
	manager, _ := manager(t)
	install(t, manager.Store, standIn{ID: "stopped", Fields: []string{"api_key"}, Sensitive: []string{"api_key"}})

	err := manager.Configure(context.Background(), "stopped", map[string]any{"api_key": "x"})
	if err == nil {
		t.Fatal("expected configuring a stopped plugin to be refused rather than guessed at")
	}
	if !strings.Contains(err.Error(), "secret") {
		t.Fatalf("the refusal should say only the plugin knows which fields are secret, got %q", err)
	}
}
