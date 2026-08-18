package plugin_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/plugin"
)

// release builds a GitHub that publishes one plugin, so install can be
// exercised end to end without reaching the real one.
type release struct {
	archive   []byte
	checksum  string
	tag       string
	corrupt   bool
	omitSums  bool
	omitAsset bool
}

func newRelease(t *testing.T, id, contents string) *release {
	return newReleaseBytes(t, id, []byte(contents))
}

func newReleaseBytes(t *testing.T, id string, contents []byte) *release {
	t.Helper()

	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(zipped)

	name := plugin.Prefix + id
	if err := writer.WriteHeader(&tar.Header{
		Name: name, Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := writer.Write(contents); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	sum := sha256.Sum256(buffer.Bytes())
	return &release{archive: buffer.Bytes(), checksum: hex.EncodeToString(sum[:]), tag: "v1.2.3"}
}

func (r *release) serve(t *testing.T, id string) *plugin.Market {
	t.Helper()

	mux := http.NewServeMux()
	asset := fmt.Sprintf("%s%s_%s_%s.tar.gz", plugin.Prefix, id, runtime.GOOS, runtime.GOARCH)

	var base string
	mux.HandleFunc("GET /orgs/{org}/repos", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"name":        plugin.Prefix + id,
			"full_name":   "example/" + plugin.Prefix + id,
			"description": "observes things",
			"html_url":    "https://example.com",
		}})
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		var assets []map[string]any
		if !r.omitAsset {
			assets = append(assets, map[string]any{
				"name": asset, "browser_download_url": base + "/download",
			})
		}
		if !r.omitSums {
			assets = append(assets, map[string]any{
				"name": "checksums.txt", "browser_download_url": base + "/checksums",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": r.tag, "assets": assets})
	})
	mux.HandleFunc("GET /download", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(r.archive)
	})
	mux.HandleFunc("GET /checksums", func(w http.ResponseWriter, _ *http.Request) {
		sum := r.checksum
		if r.corrupt {
			sum = strings.Repeat("0", len(sum))
		}
		_, _ = fmt.Fprintf(w, "%s  %s\n", sum, asset)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	base = server.URL

	return &plugin.Market{Orgs: []string{"example"}, BaseURL: server.URL, HTTP: server.Client()}
}

func TestListFindsPrefixedRepositories(t *testing.T) {
	market := newRelease(t, "kubernetes", "binary").serve(t, "kubernetes")

	listings, err := market.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listings) != 1 {
		t.Fatalf("found %d listings, want 1", len(listings))
	}
	if listings[0].ID != "kubernetes" {
		t.Errorf("id = %q, want the name after the prefix", listings[0].ID)
	}
	if listings[0].Version != "v1.2.3" {
		t.Errorf("version = %q", listings[0].Version)
	}
}

// `dusk-plugin-sdk` matches the prefix and is the contract every plugin
// compiles against, not a plugin. It was offered in the marketplace until
// publishing an installable asset became the test.
func TestListSkipsPrefixedRepositoriesWithNothingToInstall(t *testing.T) {
	built := newRelease(t, "kubernetes", "binary")
	built.omitAsset = true
	market := built.serve(t, "kubernetes")

	listings, err := market.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listings) != 0 {
		t.Errorf("offered %+v, want nothing installable to be offered", listings)
	}
}

func TestStageWritesAnImmutableBinaryWithoutActivatingIt(t *testing.T) {
	market := newRelease(t, "kubernetes", "#!/bin/sh\necho hi\n").serve(t, "kubernetes")
	store := &plugin.Store{Dir: t.TempDir()}

	listings, err := market.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	record, err := market.Stage(t.Context(), store, listings[0])
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if record.Version != "v1.2.3" {
		t.Errorf("version = %q", record.Version)
	}

	body, err := os.ReadFile(store.BinaryFor(*record))
	if err != nil {
		t.Fatalf("read the installed binary: %v", err)
	}
	if !strings.Contains(string(body), "echo hi") {
		t.Error("the installed binary is not what the archive contained")
	}

	// Executable, or the host cannot start what it just installed.
	info, err := os.Stat(store.BinaryFor(*record))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("mode = %v, want the owner execute bit", info.Mode().Perm())
	}

	installed, err := store.List()
	if err != nil {
		t.Fatalf("List installed: %v", err)
	}
	if len(installed) != 0 {
		t.Errorf("installed = %+v, want staging not to change the active record", installed)
	}
	if err := store.Write(*record); err != nil {
		t.Fatalf("activate: %v", err)
	}
	installed, err = store.List()
	if err != nil {
		t.Fatalf("List activated: %v", err)
	}
	if len(installed) != 1 || installed[0].ID != "kubernetes" {
		t.Errorf("installed = %+v, want one active record for kubernetes", installed)
	}
}

// Installing over an existing plugin is how an update is applied. Writing a
// fresh record lost the configuration, so updating to a bug fix meant retyping
// the credential that made the plugin work.
func TestStageCarriesConfigurationIntoAnUpdateCandidate(t *testing.T) {
	market := newRelease(t, "kubernetes", "binary").serve(t, "kubernetes")
	store := &plugin.Store{Dir: t.TempDir()}

	listings, err := market.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	first, err := market.Stage(t.Context(), store, listings[0])
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := store.Write(*first); err != nil {
		t.Fatalf("activate: %v", err)
	}

	configured, err := store.Read("kubernetes")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	configured.Config = map[string]any{"cluster": "prod"}
	configured.Instances = map[string]map[string]any{"other": {"cluster": "staging"}}
	if err := store.Write(*configured); err != nil {
		t.Fatalf("Write: %v", err)
	}

	candidate, err := market.Stage(t.Context(), store, listings[0])
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if candidate.Config["cluster"] != "prod" {
		t.Errorf("config = %v, want the candidate to have kept it", candidate.Config)
	}
	if _, ok := candidate.Instances["other"]; !ok {
		t.Errorf("instances = %v, want the candidate to have kept them", candidate.Instances)
	}
}

func TestADR0066_AFailedUpdateLeavesTheActivePluginRunning(t *testing.T) {
	manager, rotation := manager(t)
	old := oneAction("upgradable", readOnly)
	old.Version = "v0.0.1"
	install(t, manager.Store, old)
	manager.Restore(t.Context())
	t.Cleanup(manager.Stop)

	broken := newRelease(t, old.ID, "#!/bin/sh\nexit 9\n")
	broken.tag = "v0.0.2"
	manager.Market = broken.serve(t, old.ID)

	if _, err := manager.Install(t.Context(), old.ID); err == nil {
		t.Fatal("an update whose process never became ready was activated")
	}

	active, err := manager.Store.Read(old.ID)
	if err != nil {
		t.Fatalf("Read active record: %v", err)
	}
	if active.Version != old.Version {
		t.Errorf("active version = %q, want the old %q", active.Version, old.Version)
	}
	described, running := manager.Describe(old.ID)
	if !running || described.GetVersion() != old.Version {
		t.Errorf("running description = %+v, %v; want the old process still serving", described, running)
	}
	if got := rotation.names(); len(got) != 1 || got[0] != "plugin:"+old.ID {
		t.Errorf("rotation = %v, want the old instance untouched", got)
	}
}

func TestADR0066_AReadyUpdateCutsOverAndRestoresOffline(t *testing.T) {
	manager, _ := manager(t)
	old := oneAction("upgradable", readOnly)
	old.Version = "v0.0.1"
	old.Fields = []string{"cluster"}
	install(t, manager.Store, old)

	record, err := manager.Store.Read(old.ID)
	if err != nil {
		t.Fatalf("Read old record: %v", err)
	}
	record.Config = map[string]any{"cluster": "production"}
	record.Instances = map[string]map[string]any{"staging": {"cluster": "staging"}}
	record.Enabled = []string{"poke"}
	if err := manager.Store.Write(*record); err != nil {
		t.Fatalf("configure old record: %v", err)
	}
	manager.Restore(t.Context())
	t.Cleanup(manager.Stop)

	executable, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatalf("read test executable: %v", err)
	}
	released := newReleaseBytes(t, old.ID, executable)
	released.tag = "v0.0.2"
	manager.Market = released.serve(t, old.ID)

	next := old
	next.Version = released.tag
	encoded, err := json.Marshal(next)
	if err != nil {
		t.Fatalf("encode update stand-in: %v", err)
	}
	t.Setenv(standInEnv, string(encoded))

	active, err := manager.Install(t.Context(), old.ID)
	if err != nil {
		t.Fatalf("Install update: %v", err)
	}
	if active.Version != next.Version || active.Config["cluster"] != "production" {
		t.Errorf("active record = %+v, want the new version with old configuration", active)
	}
	if _, ok := active.Instances["staging"]; !ok || len(active.Enabled) != 1 {
		t.Errorf("active record lost instances or enabled actions: %+v", active)
	}
	if path := manager.Store.BinaryFor(*active); !strings.Contains(path, filepath.Join("versions", active.SHA256)) {
		t.Errorf("active binary = %q, want the immutable digest path", path)
	}
	if described, running := manager.Describe(old.ID); !running || described.GetVersion() != next.Version {
		t.Errorf("running description = %+v, %v; want the new process", described, running)
	}
	if _, err := manager.Install(t.Context(), old.ID); err != nil {
		t.Fatalf("reinstall the active release beside its candidate socket: %v", err)
	}

	manager.Stop()
	restoredRotation := newRota()
	restored := &plugin.Manager{Store: manager.Store, Market: manager.Market, Rota: restoredRotation}
	restored.Restore(t.Context())
	t.Cleanup(restored.Stop)
	if described, running := restored.Describe(old.ID); !running || described.GetVersion() != next.Version {
		t.Errorf("offline restore = %+v, %v; want the activated version from disk", described, running)
	}
	if got := restoredRotation.names(); len(got) != 2 {
		t.Errorf("restored rotation = %v, want the default and named instance", got)
	}
}

// GoReleaser's Version template omits the leading v from a git tag. That is a
// spelling difference, not permission for a candidate to report another
// release, and every official plugin uses the template.
func TestADR0066_AGitTagAndGoReleaserVersionAreTheSameRelease(t *testing.T) {
	manager, _ := manager(t)
	t.Cleanup(manager.Stop)

	executable, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatalf("read test executable: %v", err)
	}
	released := newReleaseBytes(t, "versioned", executable)
	released.tag = "v1.2.3-rc.1"
	manager.Market = released.serve(t, "versioned")

	spec := oneAction("versioned", readOnly)
	spec.Version = "1.2.3-rc.1"
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("encode plugin stand-in: %v", err)
	}
	t.Setenv(standInEnv, string(encoded))

	active, err := manager.Install(t.Context(), spec.ID)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if active.Version != released.tag {
		t.Errorf("active version = %q, want release tag %q", active.Version, released.tag)
	}
}

func TestADR0066_ADifferentCandidateVersionIsStillRefused(t *testing.T) {
	manager, _ := manager(t)
	t.Cleanup(manager.Stop)

	executable, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatalf("read test executable: %v", err)
	}
	released := newReleaseBytes(t, "wrong-version", executable)
	released.tag = "v1.2.3"
	manager.Market = released.serve(t, "wrong-version")

	spec := oneAction("wrong-version", readOnly)
	spec.Version = "1.2.4"
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("encode plugin stand-in: %v", err)
	}
	t.Setenv(standInEnv, string(encoded))

	if _, err := manager.Install(t.Context(), spec.ID); err == nil {
		t.Fatal("a candidate reporting another release was activated")
	}
}

// The checksum is the only automated check between trusting an org and running
// a binary from the internet, so a mismatch has to stop the install dead.
func TestInstallRefusesAChecksumMismatch(t *testing.T) {
	built := newRelease(t, "kubernetes", "binary")
	built.corrupt = true
	market := built.serve(t, "kubernetes")
	store := &plugin.Store{Dir: t.TempDir()}

	listings, err := market.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := market.Stage(t.Context(), store, listings[0]); err == nil {
		t.Fatal("installed a binary whose checksum did not match")
	}

	if _, err := os.Stat(filepath.Join(store.Dir, "kubernetes", "plugin")); err == nil {
		t.Error("a refused install still wrote a binary to disk")
	}
}

// A release with no checksums cannot be verified, and unverifiable is refused
// rather than trusted.
func TestInstallRefusesAReleaseWithNoChecksums(t *testing.T) {
	built := newRelease(t, "kubernetes", "binary")
	built.omitSums = true
	market := built.serve(t, "kubernetes")
	store := &plugin.Store{Dir: t.TempDir()}

	listings, err := market.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := market.Stage(t.Context(), store, listings[0]); err == nil {
		t.Error("installed from a release publishing no checksums")
	}
}

// Reading from disk is what makes a restart independent of GitHub.
func TestListInstalledIsEmptyRatherThanFailingWithNoDirectory(t *testing.T) {
	store := &plugin.Store{Dir: filepath.Join(t.TempDir(), "never-created")}

	installed, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(installed) != 0 {
		t.Errorf("installed = %+v, want none", installed)
	}
}

func TestBinaryForFallsBackToAPreVersionedInstall(t *testing.T) {
	store := &plugin.Store{Dir: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(store.Dir, "legacy"), 0o700); err != nil {
		t.Fatalf("make legacy directory: %v", err)
	}
	if err := os.WriteFile(store.Binary("legacy"), []byte("old binary"), 0o700); err != nil {
		t.Fatalf("write legacy binary: %v", err)
	}

	record := plugin.Installed{ID: "legacy", SHA256: strings.Repeat("a", 64)}
	if got := store.BinaryFor(record); got != store.Binary("legacy") {
		t.Errorf("BinaryFor = %q, want legacy path %q", got, store.Binary("legacy"))
	}
}
