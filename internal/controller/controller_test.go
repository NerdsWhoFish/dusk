package controller_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/controller"
	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/store"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
	"github.com/NerdsWhoFish/dusk/pkg/secret"
)

const mainRef = "refs/heads/main"

func rootFile(name string) string {
	return fmt.Sprintf(`---
dusk: v1alpha1
namespace: home
kind: service
name: %s
---

Declared by %s.
`, name, name)
}

// install is one installation the fake GitHub will report.
type install struct {
	id      int64
	account string
	repos   map[string]string

	listFails bool
}

// fakeGitHub serves enough of the API for a whole sweep.
type fakeGitHub struct {
	installs []install
	appOwner string

	// sha lets a test move a repository on, since an unchanged commit is
	// skipped without being read.
	sha string

	// failTarballs makes that many downloads fail, standing in for a blip.
	failTarballs int

	// snapshots let a concurrency test serve the tree belonging to each resolved commit.
	snapshots map[string]string

	blockTarball   string
	tarballStarted chan struct{}
	releaseTarball chan struct{}

	mu    sync.Mutex
	calls map[string]int
}

func (f *fakeGitHub) start(t *testing.T) *githubapp.Client {
	t.Helper()
	f.calls = map[string]int{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		f.count(req.URL.Path)
		switch {
		case strings.HasSuffix(req.URL.Path, "/access_tokens"):
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"token":"ghs_x","expires_at":"2099-01-01T00:00:00Z"}`)
		case req.URL.Path == "/app":
			_, _ = io.WriteString(w, `{"id":1,"slug":"dusk","owner":{"login":"`+f.appOwner+`"}}`)
		case req.URL.Path == "/app/installations":
			f.writeInstallations(w)
		case req.URL.Path == "/installation/repositories":
			f.writeRepositories(w, req)
		case strings.Contains(req.URL.Path, "/tarball/"):
			f.writeTarball(w, req)
		case strings.Contains(req.URL.Path, "/commits/"):
			_, _ = io.WriteString(w, `{"sha":"`+f.commit()+`"}`)
		case strings.Contains(req.URL.Path, "/git/trees/"):
			_, _ = io.WriteString(w, `{"truncated":false,"tree":[]}`)
		case strings.Contains(req.URL.Path, "/contents/"):
			f.writeContents(w, req)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return &githubapp.Client{BaseURL: server.URL}
}

func (f *fakeGitHub) commit() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sha != "" {
		return f.sha
	}
	return "abc1234def5678"
}

func (f *fakeGitHub) moveTo(commit string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sha = commit
}

func (f *fakeGitHub) count(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[path]++
}

// total is every request made, which is the number the API budget is spent in.
func (f *fakeGitHub) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	var sum int
	for path, n := range f.calls {
		// Minting a token is not a catalog read and is cached across them.
		if strings.Contains(path, "/access_tokens") {
			continue
		}
		sum += n
	}
	return sum
}

func (f *fakeGitHub) writeInstallations(w http.ResponseWriter) {
	out := make([]map[string]any, 0, len(f.installs))
	for _, i := range f.installs {
		out = append(out, map[string]any{"id": i.id, "account": map[string]any{"login": i.account}})
	}
	_ = json.NewEncoder(w).Encode(out)
}

// writeRepositories answers for whichever installation the token belongs to.
// The fake cannot tell them apart from the token, so a test uses one
// installation per repository set, or marks one as failing.
func (f *fakeGitHub) writeRepositories(w http.ResponseWriter, _ *http.Request) {
	for _, i := range f.installs {
		if i.listFails {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"upstream is having a day"}`)
			return
		}
	}

	repos := make([]map[string]any, 0)
	for _, i := range f.installs {
		for slug := range i.repos {
			owner, name, _ := strings.Cut(slug, "/")
			repos = append(repos, map[string]any{
				"name":           name,
				"default_branch": "main",
				"owner":          map[string]any{"login": owner},
			})
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
}

// writeContents answers the probe that decides whether a repository opted in,
// in the JSON envelope the write path also reads.
func (f *fakeGitHub) writeContents(w http.ResponseWriter, req *http.Request) {
	slug := strings.TrimPrefix(strings.SplitN(req.URL.Path, "/contents/", 2)[0], "/repos/")
	content, ok := f.contentAt(slug, req.URL.Query().Get("ref"))
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"content":  base64.StdEncoding.EncodeToString([]byte(content)),
		"encoding": "base64",
		"sha":      "blob-" + slug,
	})
}

// writeTarball sends the repository the way GitHub does: gzipped tar under one
// wrapping directory.
func (f *fakeGitHub) writeTarball(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	failing := f.failTarballs > 0
	if failing {
		f.failTarballs--
	}
	f.mu.Unlock()

	if failing {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"message":"upstream is having a day"}`)
		return
	}

	parts := strings.SplitN(req.URL.Path, "/tarball/", 2)
	slug := strings.TrimPrefix(parts[0], "/repos/")
	commit := parts[1]

	f.mu.Lock()
	blocked := commit == f.blockTarball && f.releaseTarball != nil
	started := f.tarballStarted
	release := f.releaseTarball
	if blocked && started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	f.mu.Unlock()
	if blocked {
		select {
		case <-req.Context().Done():
			return
		case <-release:
		}
	}

	content, ok := f.contentAt(slug, commit)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{
		Name: "wrapper/dusk.md", Mode: 0o644,
		Size: int64(len(content)), Typeflag: tar.TypeReg,
	})
	_, _ = tw.Write([]byte(content))
	_ = tw.Close()
	_ = gz.Close()
}

func (f *fakeGitHub) contentAt(slug, commit string) (string, bool) {
	f.mu.Lock()
	content, ok := f.snapshots[commit]
	f.mu.Unlock()
	if ok {
		return content, true
	}
	return f.contentsOf(slug)
}

// contentsOf returns a repository's dusk.md. Empty means the repository exists
// but declares nothing, which is the majority case and not the same as absent.
func (f *fakeGitHub) contentsOf(slug string) (string, bool) {
	for _, i := range f.installs {
		if content, ok := i.repos[slug]; ok {
			return content, content != ""
		}
	}
	return "", false
}

// fakeCredentials stands in for the encrypted store.
type fakeCredentials struct{ creds *store.Credentials }

func (f fakeCredentials) Load() (*store.Credentials, error) { return f.creds, nil }

func newController(t *testing.T, fake *fakeGitHub, owner string, opts controller.Options) (*controller.Controller, *index.DB) {
	t.Helper()

	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	opts.Index = idx
	opts.Client = fake.start(t)
	opts.Credentials = fakeCredentials{creds: &store.Credentials{
		AppID: 1,
		Owner: owner,
		PrivateKey: secret.New(string(pem.EncodeToMemory(
			&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))),
	}}
	opts.Logger = slog.New(slog.DiscardHandler)

	c, err := controller.New(opts)
	if err != nil {
		t.Fatalf("controller.New: %v", err)
	}
	return c, idx
}

func TestSyncReconcilesEveryRepository(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      10,
		account: "example",
		repos: map[string]string{
			"example/homelab": rootFile("jellyfin"),
			"example/media":   rootFile("navidrome"),
		},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{})

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	for _, ref := range []string{"service:home/jellyfin", "service:home/navidrome"} {
		if _, err := idx.Get(t.Context(), mainRef, ref); err != nil {
			t.Errorf("Get(%q): %v", ref, err)
		}
	}

	t.Run("status reports each repository", func(t *testing.T) {
		statuses := c.Status()
		if len(statuses) != 2 {
			t.Fatalf("Status = %d entries, want 2", len(statuses))
		}
		for _, s := range statuses {
			if s.Error != "" {
				t.Errorf("%s reported %q", s.Repository, s.Error)
			}
			if s.Commit == "" {
				t.Errorf("%s recorded no commit", s.Repository)
			}
		}
	})
}

// Most repositories in an installation declare nothing and never will, and
// most pushes to the ones that do are code. Both are answerable from the index
// alone, and doing so is what keeps a busy account inside its API budget.
func TestAnIrrelevantPushCostsNothing(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id: 10, account: "example",
		repos: map[string]string{
			"example/homelab": rootFile("jellyfin"),
			"example/website": "",
		},
	}}}
	c, _ := newController(t, fake, "example", controller.Options{})

	// Learn which repositories participate, the way a first sweep would.
	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	before := fake.total()

	tests := []struct {
		name  string
		repo  string
		files []string
		reads bool
	}{
		{
			name:  "a code push to a repository with no dusk.md",
			repo:  "website",
			files: []string{"index.html", "style.css"},
		},
		{
			name:  "a code push to a repository that does participate",
			repo:  "homelab",
			files: []string{"main.go", "Dockerfile"},
		},
		{
			name:  "a dusk.md appearing in a repository that had none",
			repo:  "website",
			files: []string{"dusk.md"},
			reads: true,
		},
		{
			name:  "markdown changing in a repository that participates",
			repo:  "homelab",
			files: []string{"services/jellyfin/dusk.md"},
			reads: true,
		},
		// Nil is not "no files". An untrustworthy payload must be read.
		{
			name:  "a push whose files could not be trusted",
			repo:  "homelab",
			files: nil,
			reads: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Move the commit so a read cannot be skipped for being unchanged,
			// which would make this test pass for the wrong reason.
			fake.sha = "deadbeef" + tt.name[:1]
			at := fake.total()

			err := c.SyncPush(t.Context(), controller.Push{
				InstallationID: 10, Account: "example", Owner: "example",
				Name: tt.repo, GitRef: mainRef, Files: tt.files,
			})
			if err != nil {
				t.Fatalf("SyncPush: %v", err)
			}

			spent := fake.total() - at
			if tt.reads && spent == 0 {
				t.Error("the push was skipped, but it could have changed the catalog")
			}
			if !tt.reads && spent != 0 {
				t.Errorf("spent %d requests on a push that cannot have changed anything", spent)
			}
		})
	}

	if fake.total() == before {
		t.Fatal("no request was made by any case, so this test proves nothing")
	}
}

// An ingester's scope occupies the repository slot but is not one, so a sweep
// never sees it and pruning it deletes every observation. This shipped, and
// wiped a real catalog's observations a minute after the first sweep.
func TestASweepDoesNotPruneObservationsOrPreviews(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id: 10, account: "example",
		repos: map[string]string{"example/homelab": rootFile("jellyfin")},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{})

	observed := index.ObservedScope("kubernetes:prod")
	seed := []index.Declaration{{Path: "observed", Entity: &duskv1alpha1.Entity{
		Ref: "service:cluster/seen", Kind: "service", Namespace: "cluster", Name: "seen",
	}}}
	if err := idx.Put(t.Context(), observed, "refs/dusk/observed", seed, nil, nil); err != nil {
		t.Fatalf("Put observed: %v", err)
	}

	preview := []index.Declaration{{Path: "dusk.md", Entity: &duskv1alpha1.Entity{
		Ref: "service:home/proposed", Kind: "service", Namespace: "home", Name: "proposed",
	}}}
	if err := idx.Put(t.Context(), "example/homelab", "refs/pull/7/head", preview, nil, nil); err != nil {
		t.Fatalf("Put preview: %v", err)
	}

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if _, err := idx.Get(t.Context(), "refs/dusk/observed", "service:cluster/seen"); err != nil {
		t.Errorf("a sweep deleted what an ingester observed: %v", err)
	}
	if _, err := idx.Get(t.Context(), "refs/pull/7/head", "service:home/proposed"); err != nil {
		t.Errorf("a sweep deleted a pull request preview: %v", err)
	}
}

// An unchanged commit is not read again. This is what makes sweeping an idle
// installation affordable, and therefore what lets the poll floor be slow.
func TestAnUnchangedCommitIsNotDownloaded(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id: 10, account: "example",
		repos: map[string]string{"example/homelab": rootFile("jellyfin")},
	}}}
	c, _ := newController(t, fake, "example", controller.Options{})

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	first := fake.calls["/repos/example/homelab/tarball/"+fake.commit()]

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := fake.calls["/repos/example/homelab/tarball/"+fake.commit()]; got != first {
		t.Errorf("downloaded %d times across two sweeps, want %d", got, first)
	}

	t.Run("a moved commit is read again", func(t *testing.T) {
		fake.sha = "0000000feedface"
		if err := c.Sync(t.Context()); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if fake.calls["/repos/example/homelab/tarball/"+fake.sha] == 0 {
			t.Error("a changed commit was skipped")
		}
	})
}

func TestADR0006_AnOlderReconcileCannotReplaceANewerCommit(t *testing.T) {
	const (
		oldCommit = "1111111old"
		newCommit = "2222222new"
	)
	fake := &fakeGitHub{
		sha: oldCommit,
		installs: []install{{
			id: 10, account: "example",
			repos: map[string]string{"example/homelab": rootFile("fallback")},
		}},
		snapshots: map[string]string{
			oldCommit: rootFile("old"),
			newCommit: rootFile("new"),
		},
		blockTarball: oldCommit, tarballStarted: make(chan struct{}), releaseTarball: make(chan struct{}),
	}
	c, idx := newController(t, fake, "example", controller.Options{})

	syncRepository := func(done chan<- error) {
		done <- c.SyncRepository(t.Context(), 10, "example", "example", "homelab", mainRef)
	}
	oldDone := make(chan error, 1)
	go syncRepository(oldDone)

	select {
	case <-fake.tarballStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("the old reconcile never reached its blocked download")
	}

	fake.moveTo(newCommit)
	newDone := make(chan error, 1)
	go syncRepository(newDone)

	var newErr error
	select {
	case newErr = <-newDone:
		// Without scope serialization the newer reconcile finishes here. Releasing
		// the older one afterwards makes the stale snapshot deterministically win.
		close(fake.releaseTarball)
	case <-time.After(250 * time.Millisecond):
		// The newer reconcile is correctly waiting before it resolves the ref.
		close(fake.releaseTarball)
		newErr = <-newDone
	}
	if err := <-oldDone; err != nil {
		t.Fatalf("old SyncRepository: %v", err)
	}
	if newErr != nil {
		t.Fatalf("new SyncRepository: %v", newErr)
	}

	if _, err := idx.Get(t.Context(), mainRef, "service:home/new"); err != nil {
		t.Errorf("new commit is not the final graph: %v", err)
	}
	if _, err := idx.Get(t.Context(), mainRef, "service:home/old"); !errors.Is(err, index.ErrNotFound) {
		t.Errorf("old commit replaced the newer graph: %v", err)
	}
	statuses := c.Status()
	if len(statuses) != 1 || statuses[0].Commit != newCommit {
		t.Errorf("Status = %+v, want commit %s", statuses, newCommit)
	}
}

// A delivery is answered before the work runs, so GitHub never redelivers. A
// transient failure has to be retried here or it waits for the poll floor.
func TestADeliveryRetriesATransientFailure(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id: 10, account: "example",
		repos: map[string]string{"example/homelab": rootFile("jellyfin")},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{})

	// Fail the first attempt only, the way a blip would.
	fake.failTarballs = 1

	if err := c.SyncRepository(t.Context(), 10, "example", "example", "homelab", mainRef); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	if _, err := idx.Get(t.Context(), mainRef, "service:home/jellyfin"); err != nil {
		t.Errorf("the retry did not recover: %v", err)
	}
}

// Retrying a file that does not parse only delays the error reaching whoever
// wrote it, and burns the delivery's budget doing nothing.
func TestADeliveryDoesNotRetryABrokenFile(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id: 10, account: "example",
		repos: map[string]string{"example/homelab": "---\nkidn: service\n---\n\nbroken\n"},
	}}}
	c, _ := newController(t, fake, "example", controller.Options{})

	if err := c.SyncRepository(t.Context(), 10, "example", "example", "homelab", mainRef); err == nil {
		t.Fatal("SyncRepository succeeded on an unparseable file")
	}
	if got := fake.calls["/repos/example/homelab/tarball/"+fake.commit()]; got != 1 {
		t.Errorf("downloaded %d times, want 1: a parse error is not transient", got)
	}
}

// A failure must not record the commit, or the sweep would skip the repository
// and the error would never be retried at all.
func TestAFailureLeavesTheCommitUnfinished(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id: 10, account: "example",
		repos: map[string]string{"example/homelab": rootFile("jellyfin")},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{})

	fake.failTarballs = 99
	_ = c.Sync(t.Context())

	fake.failTarballs = 0
	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := idx.Get(t.Context(), mainRef, "service:home/jellyfin"); err != nil {
		t.Errorf("the next sweep did not retry the failed commit: %v", err)
	}
}

// A GitHub App can be installed by anyone able to see it. An uninvited
// installation must never reach the catalog, because its markdown becomes
// context agents treat as fact.
func TestUninvitedInstallationIsNeverReconciled(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      66,
		account: "stranger",
		repos:   map[string]string{"stranger/malicious": rootFile("backdoor")},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{})

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if _, err := idx.Get(t.Context(), mainRef, "service:home/backdoor"); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("an unallowed account reached the catalog: %v", err)
	}
	if scopes, _ := idx.Scopes(t.Context()); len(scopes) != 0 {
		t.Errorf("index holds %v, want nothing", scopes)
	}

	t.Run("a webhook for that account is refused too", func(t *testing.T) {
		if err := c.SyncRepository(t.Context(), 66, "stranger", "stranger", "malicious", mainRef); err != nil {
			t.Fatalf("SyncRepository: %v", err)
		}
		if scopes, _ := idx.Scopes(t.Context()); len(scopes) != 0 {
			t.Errorf("a delivery bypassed the allowlist: %v", scopes)
		}
	})
}

// The App's owner and a repository's owner are different things, and they
// coincide often enough that confusing them passes most tests. An App owned by
// a person and installed on an organisation is where it shows.
func TestRepositoryOwnerIsNotTheAppOwner(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      10,
		account: "acme",
		repos:   map[string]string{"acme/platform": rootFile("jellyfin")},
	}}}
	c, idx := newController(t, fake, "a-person", controller.Options{Accounts: []string{"acme"}})

	if err := c.SyncRepository(t.Context(), 10, "acme", "acme", "platform", mainRef); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}

	scopes, err := idx.Scopes(t.Context())
	if err != nil {
		t.Fatalf("Scopes: %v", err)
	}
	if len(scopes) != 1 || scopes[0].Repository != "acme/platform" {
		t.Fatalf("stored under %v, want acme/platform", scopes)
	}
}

func TestAllowlist(t *testing.T) {
	tests := []struct {
		name     string
		owner    string
		accounts []string
		account  string
		want     bool
	}{
		{"the App owner is allowed by default", "example", nil, "example", true},
		{"any other account is refused by default", "example", nil, "stranger", false},
		{"logins compare case insensitively", "Example", nil, "eXaMpLe", true},
		{"an explicit list is honoured", "example", []string{"trusted"}, "trusted", true},
		{"an explicit list excludes the owner unless listed", "example", []string{"trusted"}, "example", false},
		{"an empty account is never allowed", "", nil, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newController(t, &fakeGitHub{}, tt.owner, controller.Options{
				Accounts: tt.accounts,
			})
			if got := c.Permitted(tt.account, tt.owner); got != tt.want {
				t.Errorf("Permitted(%q) = %v, want %v", tt.account, got, tt.want)
			}
		})
	}
}

func TestSyncPrunesRepositoriesItCanNoLongerSee(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      10,
		account: "example",
		repos: map[string]string{
			"example/homelab": rootFile("jellyfin"),
			"example/media":   rootFile("navidrome"),
		},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{})

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	delete(fake.installs[0].repos, "example/media")
	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if _, err := idx.Get(t.Context(), mainRef, "service:home/navidrome"); !errors.Is(err, index.ErrNotFound) {
		t.Errorf("a revoked repository kept its entities: %v", err)
	}
	if _, err := idx.Get(t.Context(), mainRef, "service:home/jellyfin"); err != nil {
		t.Errorf("a still-installed repository was pruned: %v", err)
	}
}

// ADR-0011's rule generalised: "I could not look" must never be mistaken for
// "it is not there". A sweep that failed to enumerate must not delete.
func TestADR0011_AnIncompleteSweepNeverPrunes(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      10,
		account: "example",
		repos:   map[string]string{"example/homelab": rootFile("jellyfin")},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{})

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	fake.installs[0].listFails = true
	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync returned an error rather than carrying on: %v", err)
	}

	if _, err := idx.Get(t.Context(), mainRef, "service:home/jellyfin"); err != nil {
		t.Errorf("a failed sweep deleted the previous graph: %v", err)
	}
}

func TestRunSweepsUntilCancelled(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      10,
		account: "example",
		repos:   map[string]string{"example/homelab": rootFile("jellyfin")},
	}}}
	c, idx := newController(t, fake, "example", controller.Options{Interval: time.Hour})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()

	// Run sweeps immediately rather than waiting out the first tick, so the
	// catalog is current as soon as the process is.
	deadline := time.After(10 * time.Second)
	for {
		if _, err := idx.Get(t.Context(), mainRef, "service:home/jellyfin"); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run did not sweep before its first tick")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop when its context was cancelled")
	}
}

// The floor keeps sweeping when it looks redundant, which is the whole of it:
// a delivery that never arrives leaves nothing to notice, so the sweep that
// recovers it is the one a reader would delete as duplicated work.
func TestADR0006_PollFloorRunsWithWebhooksConfigured(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{
		id:      10,
		account: "example",
		repos:   map[string]string{"example/homelab": rootFile("jellyfin")},
	}}}
	c, _ := newController(t, fake, "example", controller.Options{Interval: 50 * time.Millisecond})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go c.Run(ctx)

	first := awaitAttempt(t, c, time.Time{})
	if second := awaitAttempt(t, c, first); !second.After(first) {
		t.Fatal("the floor swept once and stopped, so nothing recovers a delivery that never arrived")
	}
}

// awaitAttempt waits for a sweep later than one already seen.
func awaitAttempt(t *testing.T, c *controller.Controller, after time.Time) time.Time {
	t.Helper()

	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		for _, status := range c.Status() {
			if status.Attempted.After(after) {
				return status.Attempted
			}
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("no sweep was attempted before the deadline")
	return time.Time{}
}

// Credentials stored before onboarding recorded an owner leave it empty, and an
// empty owner allows nothing. Upgrading must not silently stop reconciling.
func TestOwnerIsResolvedWhenOnboardingDidNotRecordIt(t *testing.T) {
	fake := &fakeGitHub{
		appOwner: "example",
		installs: []install{{
			id:      10,
			account: "example",
			repos:   map[string]string{"example/homelab": rootFile("jellyfin")},
		}},
	}
	c, idx := newController(t, fake, "", controller.Options{})

	if err := c.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if _, err := idx.Get(t.Context(), mainRef, "service:home/jellyfin"); err != nil {
		t.Fatalf("an upgraded install reconciled nothing: %v", err)
	}
}
