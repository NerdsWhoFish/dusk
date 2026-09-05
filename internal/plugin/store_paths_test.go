package plugin_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/pkg/vault"
)

func TestStoreRejectsIDsOutsideOnePluginDirectory(t *testing.T) {
	root := t.TempDir()
	store := &plugin.Store{Dir: filepath.Join(root, "plugins"), Master: make([]byte, vault.KeySize)}
	marker := filepath.Join(root, "credentials.enc")
	if err := os.WriteFile(marker, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "..", "../outside", "one/../../outside", "/absolute", `..\outside`, "one\x00two"} {
		t.Run(id, func(t *testing.T) {
			_, readErr := store.Read(id)
			_, secretsErr := store.ReadSecrets(id)
			for operation, err := range map[string]error{
				"read":          readErr,
				"write":         store.Write(plugin.Installed{ID: id}),
				"read secrets":  secretsErr,
				"write secrets": store.WriteSecrets(id, &plugin.Secrets{}),
				"remove":        store.Remove(id),
				"uninstall":     (&plugin.Manager{Store: store}).Uninstall(id),
			} {
				if !errors.Is(err, plugin.ErrInvalidID) {
					t.Errorf("%s accepted invalid ID: %v", operation, err)
				}
			}
			if body, err := os.ReadFile(marker); err != nil || string(body) != "preserved" {
				t.Fatalf("parent data changed: %q, %v", body, err)
			}
		})
	}
}

func TestStoreUninstallRemovesOnlyTheSelectedPlugin(t *testing.T) {
	store := &plugin.Store{Dir: t.TempDir()}
	for _, id := range []string{"home-assistant", "other_v2.1"} {
		if err := store.Write(plugin.Installed{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := (&plugin.Manager{Store: store}).Uninstall("home-assistant"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("home-assistant"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstalled record remains: %v", err)
	}
	if _, err := store.Read("other_v2.1"); err != nil {
		t.Fatalf("uninstall affected another plugin: %v", err)
	}
}

func TestStoreRejectsARecordForAnotherDirectory(t *testing.T) {
	store := &plugin.Store{Dir: t.TempDir()}
	if err := store.Write(plugin.Installed{ID: "example"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "example", "installed.json"), []byte(`{"id":"../outside"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("example"); err == nil {
		t.Fatal("record redirected subsequent operations outside its directory")
	}
}
