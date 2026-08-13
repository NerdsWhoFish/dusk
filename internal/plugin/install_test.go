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
	corrupt   bool
	omitSums  bool
	omitAsset bool
}

func newRelease(t *testing.T, id, contents string) *release {
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
	if _, err := writer.Write([]byte(contents)); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	sum := sha256.Sum256(buffer.Bytes())
	return &release{archive: buffer.Bytes(), checksum: hex.EncodeToString(sum[:])}
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
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.2.3", "assets": assets})
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

func TestInstallWritesTheBinaryAndItsRecord(t *testing.T) {
	market := newRelease(t, "kubernetes", "#!/bin/sh\necho hi\n").serve(t, "kubernetes")
	store := &plugin.Store{Dir: t.TempDir()}

	listings, err := market.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	record, err := market.Install(t.Context(), store, listings[0])
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if record.Version != "v1.2.3" {
		t.Errorf("version = %q", record.Version)
	}

	body, err := os.ReadFile(store.Binary("kubernetes"))
	if err != nil {
		t.Fatalf("read the installed binary: %v", err)
	}
	if !strings.Contains(string(body), "echo hi") {
		t.Error("the installed binary is not what the archive contained")
	}

	// Executable, or the host cannot start what it just installed.
	info, err := os.Stat(store.Binary("kubernetes"))
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
	if len(installed) != 1 || installed[0].ID != "kubernetes" {
		t.Errorf("installed = %+v, want one record for kubernetes", installed)
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
	if _, err := market.Install(t.Context(), store, listings[0]); err == nil {
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
	if _, err := market.Install(t.Context(), store, listings[0]); err == nil {
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
