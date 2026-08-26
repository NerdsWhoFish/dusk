package write_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/catalogfs"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

const (
	jellyfin = "service:home/jellyfin"
	repo     = "example/homelab"
	RootPath = "dusk.md"
)

const rootFile = `---
dusk: v1alpha1
namespace: home
kind: host
name: nas
title: The NAS
include:
  - services/*/dusk.md
---

Four bays.
`

const jellyfinFile = `---
dusk: v1alpha1
kind: service
name: jellyfin
title: Jellyfin
---

Media server.

## Gotchas

**Transcoding is off.** Anything that will not direct play is a client problem.
`

type fakeTarget struct {
	files   map[string]string
	commits []githubapp.FileCommit
}

func (f *fakeTarget) DefaultBranch(context.Context) (string, error) { return "main", nil }

func (f *fakeTarget) ReadFileContents(_ context.Context, _, filePath string) (*githubapp.FileContents, error) {
	body, ok := f.files[filePath]
	if !ok {
		return nil, fmt.Errorf("%q: %w", filePath, fs.ErrNotExist)
	}
	return &githubapp.FileContents{Data: []byte(body), SHA: "blob-" + filePath}, nil
}

func (f *fakeTarget) CommitFile(_ context.Context, commit githubapp.FileCommit) (*githubapp.Commit, error) {
	f.commits = append(f.commits, commit)
	if commit.Delete {
		delete(f.files, commit.Path)
	} else {
		f.files[commit.Path] = string(commit.Content)
	}
	return &githubapp.Commit{SHA: "c0ffee", URL: "https://github.com/example/homelab/commit/c0ffee"}, nil
}

type fakeRepositories struct{ target *fakeTarget }

func (f fakeRepositories) Target(_ context.Context, slug string) (write.Target, error) {
	if slug != repo {
		return nil, fmt.Errorf("write: no installation grants access to %q", slug)
	}
	return f.target, nil
}

type fakeCatalog struct {
	at    *index.Location
	alike []index.Similarity
}

func (f fakeCatalog) Locate(_ context.Context, _, entityRef string) (*index.Location, error) {
	if f.at == nil {
		return nil, fmt.Errorf("locate %q: %w", entityRef, index.ErrNotFound)
	}
	return f.at, nil
}

func (f fakeCatalog) LocateIn(ctx context.Context, gitRef, entityRef, repository string) (*index.Location, error) {
	at, err := f.Locate(ctx, gitRef, entityRef)
	if err != nil {
		return nil, err
	}
	if repository != "" && at.Repository != repository {
		return nil, fmt.Errorf("locate %q in %q: %w", entityRef, repository, index.ErrNotFound)
	}
	return at, nil
}

func (f fakeCatalog) Get(context.Context, string, string) (*duskv1alpha1.Entity, error) {
	return nil, index.ErrNotFound
}

func (f fakeCatalog) SimilarNotes(context.Context, string, string, int) ([]index.Similarity, error) {
	return f.alike, nil
}

func newWriter(t *testing.T, at *index.Location, files map[string]string) (*write.Writer, *fakeTarget, *proof.Store) {
	t.Helper()
	target := &fakeTarget{files: files}
	tokens := &proof.Store{}
	return &write.Writer{
		Catalog:      fakeCatalog{at: at},
		Repositories: fakeRepositories{target: target},
		Proof:        tokens,
	}, target, tokens
}

func located(path string) *index.Location {
	return locatedContents(path, jellyfinFile)
}

func locatedContents(path, contents string) *index.Location {
	return &index.Location{
		Repository: repo, GitRef: "refs/heads/main", Path: path, Version: "v1",
		ContentHash: duskmd.FileContentHash([]byte(contents)),
	}
}

func TestDeclareUpdatesAnExistingEntity(t *testing.T) {
	files := map[string]string{RootPath: rootFile, "services/jellyfin/dusk.md": jellyfinFile}
	writer, target, tokens := newWriter(t, located("services/jellyfin/dusk.md"), files)

	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	result, err := writer.Declare(t.Context(), token.ID, write.Declaration{
		Ref:        jellyfin,
		Title:      "Jellyfin Media Server",
		Attributes: map[string]string{"backup": "nightly"},
	})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}

	if result.Created {
		t.Error("reported a create for an entity that existed")
	}
	if result.URL == "" || result.Commit == "" {
		t.Errorf("result = %+v, want somewhere a human can look", result)
	}

	if len(target.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(target.commits))
	}
	commit := target.commits[0]

	t.Run("it presents the blob it read", func(t *testing.T) {
		if commit.ReplacingSHA == "" {
			t.Error("no sha presented, so a raced write would overwrite silently")
		}
	})

	t.Run("the message names the entity", func(t *testing.T) {
		if !strings.Contains(commit.Message, jellyfin) {
			t.Errorf("message = %q, want the ref in it", commit.Message)
		}
	})

	// The whole point of separating frontmatter from prose: a write can change
	// what Dusk owns without touching a word somebody wrote.
	t.Run("the prose is untouched", func(t *testing.T) {
		written := string(commit.Content)
		for _, want := range []string{"Media server.", "## Gotchas", "**Transcoding is off.**"} {
			if !strings.Contains(written, want) {
				t.Errorf("prose lost %q:\n%s", want, written)
			}
		}
	})

	t.Run("the frontmatter carries the change", func(t *testing.T) {
		written := string(commit.Content)
		if !strings.Contains(written, "title: Jellyfin Media Server") {
			t.Errorf("title not applied:\n%s", written)
		}
		if !strings.Contains(written, "backup: nightly") {
			t.Errorf("attribute not applied:\n%s", written)
		}
	})
}

// ADR-0009's gate has to hold at the write path, not only in its own package.
func TestADR0009_DeclareWithoutProofIsRejected(t *testing.T) {
	files := map[string]string{RootPath: rootFile, "services/jellyfin/dusk.md": jellyfinFile}
	writer, target, tokens := newWriter(t, located("services/jellyfin/dusk.md"), files)

	t.Run("no token", func(t *testing.T) {
		_, err := writer.Declare(t.Context(), "", write.Declaration{Ref: jellyfin, Title: "x"})
		var rejection *proof.Rejection
		if !errors.As(err, &rejection) {
			t.Fatalf("err = %v, want a proof rejection", err)
		}
	})

	t.Run("a token from before somebody else wrote", func(t *testing.T) {
		token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v0"})
		_, err := writer.Declare(t.Context(), token.ID, write.Declaration{Ref: jellyfin, Title: "x"})
		var rejection *proof.Rejection
		if !errors.As(err, &rejection) || rejection.Code != proof.CodeStale {
			t.Fatalf("err = %v, want %s", err, proof.CodeStale)
		}
	})

	if len(target.commits) != 0 {
		t.Errorf("a rejected declare still committed: %+v", target.commits)
	}
}

// ADR-0009 protects the state the agent read, not merely the last state the
// asynchronous reconciler happened to materialize.
func TestADR0009_DeclareRejectsAFileChangedBeforeReconcile(t *testing.T) {
	changed := strings.Replace(jellyfinFile, "Media server.", "Changed directly in Git.", 1)
	files := map[string]string{RootPath: rootFile, "services/jellyfin/dusk.md": changed}
	writer, target, tokens := newWriter(t, located("services/jellyfin/dusk.md"), files)

	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	_, err := writer.Declare(t.Context(), token.ID, write.Declaration{Ref: jellyfin, Title: "x"})
	var rejection *proof.Rejection
	if !errors.As(err, &rejection) || rejection.Code != proof.CodeStale {
		t.Fatalf("err = %v, want %s", err, proof.CodeStale)
	}
	if len(target.commits) != 0 {
		t.Errorf("a stale declare still committed: %+v", target.commits)
	}
}

func TestDeclareCreates(t *testing.T) {
	files := map[string]string{RootPath: rootFile}
	writer, target, tokens := newWriter(t, nil, files)

	token := tokens.Issue(proof.FromSearch, map[string]string{"host:home/nas": "v9"})
	result, err := writer.Declare(t.Context(), token.ID, write.Declaration{
		Ref:         "service:home/navidrome",
		Repository:  repo,
		Description: "Music streaming.",
	})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}

	if !result.Created {
		t.Error("Created = false, want true")
	}
	// The include glob decides where a new file may live, or it is never read.
	if result.Path != "services/navidrome/dusk.md" {
		t.Errorf("path = %q, want it placed under the include glob", result.Path)
	}
	if len(target.commits) != 1 || target.commits[0].ReplacingSHA != "" {
		t.Errorf("a create presented a sha: %+v", target.commits)
	}
	if !strings.Contains(string(target.commits[0].Content), "Music streaming.") {
		t.Errorf("description missing:\n%s", target.commits[0].Content)
	}
}

// A create places the file under an include, and the pattern that reaches every
// existing entity has to reach the new one too. A repository whose entities can
// be read and not written is one an agent can only declare into by hand.
func TestDeclareCreatesUnderAnyIncludeThatCanBeRead(t *testing.T) {
	for _, tt := range []struct {
		name    string
		include string
		want    string
	}{
		{"a directory per entity", "services/*/dusk.md", "services/navidrome/dusk.md"},
		{"a file per entity", "entities/*.md", "entities/navidrome.md"},
		{"at any depth", "entities/**/*.md", "entities/navidrome.md"},
		{"anywhere at all", `"**/*.md"`, "navidrome.md"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := strings.Replace(rootFile, "services/*/dusk.md", tt.include, 1)
			writer, target, tokens := newWriter(t, nil, map[string]string{RootPath: root})

			token := tokens.Issue(proof.FromSearch, nil)
			result, err := writer.Declare(t.Context(), token.ID, write.Declaration{
				Ref: "service:home/navidrome", Repository: repo,
			})
			if err != nil {
				t.Fatalf("Declare under %q: %v", tt.include, err)
			}
			if result.Path != tt.want {
				t.Errorf("path = %q, want %q", result.Path, tt.want)
			}

			// The pattern reads the file back, or the write is a commit the
			// catalog never sees.
			if len(target.commits) != 1 {
				t.Fatalf("commits = %d, want 1", len(target.commits))
			}
			ok, err := catalogfs.Match(strings.Trim(tt.include, `"`), target.commits[0].Path)
			if err != nil || !ok {
				t.Errorf("%q does not match the include %q it was placed under", target.commits[0].Path, tt.include)
			}
		})
	}
}

// An include naming one file exactly cannot name a file for anything, and
// treating it as a destination puts every new entity in that one file.
func TestDeclareRefusesAnIncludeThatNamesOneFile(t *testing.T) {
	root := strings.Replace(rootFile, "services/*/dusk.md", "entities/nas.md", 1)
	writer, target, tokens := newWriter(t, nil, map[string]string{RootPath: root})

	token := tokens.Issue(proof.FromSearch, nil)
	_, err := writer.Declare(t.Context(), token.ID, write.Declaration{
		Ref: "service:home/navidrome", Repository: repo,
	})
	if err == nil {
		t.Fatal("Declare placed an entity in a file declared for another one")
	}
	if len(target.commits) != 0 {
		t.Errorf("it wrote anyway: %+v", target.commits)
	}
}

func TestRepositoryRootOptsARepositoryIn(t *testing.T) {
	writer, target, tokens := newWriter(t, nil, map[string]string{})

	file, err := writer.RepositoryRoot(t.Context(), repo)
	if err != nil {
		t.Fatalf("RepositoryRoot: %v", err)
	}
	if file.Exists || len(file.Template) == 0 {
		t.Fatalf("file = %+v, want a missing file with an editable starter", file)
	}

	token := tokens.Issue(proof.FromRepository, nil)
	result, err := writer.SetRepositoryRoot(t.Context(), token.ID, repo, file.Template)
	if err != nil {
		t.Fatalf("SetRepositoryRoot: %v", err)
	}
	if !result.Created || result.Path != RootPath {
		t.Errorf("result = %+v, want a created root dusk.md", result)
	}
	if len(target.commits) != 1 || target.commits[0].ReplacingSHA != "" {
		t.Fatalf("commits = %+v, want one create", target.commits)
	}
	if _, err := duskmd.ParseRoot(RootPath, target.commits[0].Content, duskmd.Provenance{}); err != nil {
		t.Errorf("starter committed an invalid root: %v", err)
	}
}

func TestRepositoryRootEditRequiresItsOwnFreshRead(t *testing.T) {
	writer, target, tokens := newWriter(t, nil, map[string]string{RootPath: rootFile})
	changed := strings.Replace(rootFile, "The NAS", "NAS", 1)

	wrong := tokens.Issue(proof.FromSearch, nil)
	if _, err := writer.SetRepositoryRoot(t.Context(), wrong.ID, repo, []byte(changed)); err == nil {
		t.Fatal("a search token edited dusk.md")
	}

	file, err := writer.RepositoryRoot(t.Context(), repo)
	if err != nil {
		t.Fatalf("RepositoryRoot: %v", err)
	}
	token := tokens.Issue(proof.FromRepository, map[string]string{repo: file.Version})
	result, err := writer.SetRepositoryRoot(t.Context(), token.ID, repo, []byte(changed))
	if err != nil {
		t.Fatalf("SetRepositoryRoot: %v", err)
	}
	if result.Created || len(target.commits) != 1 || target.commits[0].ReplacingSHA == "" {
		t.Errorf("result = %+v, commits = %+v; want one guarded update", result, target.commits)
	}
}

// A file no include reaches is committed and never read, so refusing up front
// beats a write that appears to work and changes nothing.
func TestDeclareRefusesToCreateAnOrphan(t *testing.T) {
	noIncludes := strings.Replace(rootFile, "include:\n  - services/*/dusk.md\n", "", 1)
	writer, target, tokens := newWriter(t, nil, map[string]string{RootPath: noIncludes})

	token := tokens.Issue(proof.FromSearch, nil)
	_, err := writer.Declare(t.Context(), token.ID, write.Declaration{
		Ref: "service:home/navidrome", Repository: repo,
	})
	if err == nil {
		t.Fatal("Declare created a file nothing would read")
	}
	if !strings.Contains(err.Error(), "include") {
		t.Errorf("error = %q, want it to name the missing include", err)
	}
	if len(target.commits) != 0 {
		t.Errorf("it wrote an orphan: %+v", target.commits)
	}
}

func TestDeclareCreateNeedsARepository(t *testing.T) {
	writer, _, tokens := newWriter(t, nil, map[string]string{RootPath: rootFile})

	token := tokens.Issue(proof.FromSearch, nil)
	_, err := writer.Declare(t.Context(), token.ID, write.Declaration{Ref: "service:home/navidrome"})
	if err == nil || !strings.Contains(err.Error(), "which repository") {
		t.Fatalf("err = %v, want it to ask which repository declares it", err)
	}
}

func TestDeclareCanUnsetOwnedFields(t *testing.T) {
	withAttributes := strings.Replace(jellyfinFile, "title: Jellyfin\n", "title: Jellyfin\nobserved_as:\n  - service:cluster/media\nattributes:\n  backup: nightly\n  owner: joey\n", 1)
	writer, target, tokens := newWriter(t, locatedContents(jellyfinPath, withAttributes), map[string]string{
		RootPath: rootFile, jellyfinPath: withAttributes,
	})

	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	_, err := writer.Declare(t.Context(), token.ID, write.Declaration{
		Ref: jellyfin, Unset: []string{"title", "observed_as", "attributes.backup"},
	})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	written := string(target.commits[0].Content)
	for _, absent := range []string{"title: Jellyfin", "observed_as:", "backup: nightly"} {
		if strings.Contains(written, absent) {
			t.Errorf("unset field remains %q:\n%s", absent, written)
		}
	}
	if !strings.Contains(written, "owner: joey") {
		t.Errorf("unrelated attribute was lost:\n%s", written)
	}
}

func TestDeclareDecommissionsAndRecommissions(t *testing.T) {
	decommissioned := true
	writer, target, tokens := newWriter(t, located(jellyfinPath), map[string]string{
		RootPath: rootFile, jellyfinPath: jellyfinFile,
	})
	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	_, err := writer.Declare(t.Context(), token.ID, write.Declaration{Ref: jellyfin, Decommissioned: &decommissioned})
	if err != nil {
		t.Fatalf("decommission: %v", err)
	}
	if !strings.Contains(string(target.commits[0].Content), "lifecycle: decommissioned") {
		t.Fatalf("decommission marker missing:\n%s", target.commits[0].Content)
	}

	active := false
	updated := string(target.commits[0].Content)
	writer, target, tokens = newWriter(t, locatedContents(jellyfinPath, updated), map[string]string{
		RootPath: rootFile, jellyfinPath: updated,
	})
	token = tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	_, err = writer.Declare(t.Context(), token.ID, write.Declaration{Ref: jellyfin, Decommissioned: &active})
	if err != nil {
		t.Fatalf("recommission: %v", err)
	}
	if strings.Contains(string(target.commits[0].Content), "decommissioned") {
		t.Errorf("lifecycle marker remains:\n%s", target.commits[0].Content)
	}
}

func TestDeclareRemoveNeedsConfirmationAndDeletesIncludedFile(t *testing.T) {
	files := map[string]string{RootPath: rootFile, jellyfinPath: jellyfinFile}
	writer, target, tokens := newWriter(t, located(jellyfinPath), files)
	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})

	if _, err := writer.Declare(t.Context(), token.ID, write.Declaration{Ref: jellyfin, Remove: true}); err == nil {
		t.Fatal("remove without confirmation succeeded")
	}
	if len(target.commits) != 0 {
		t.Fatalf("unconfirmed remove committed: %+v", target.commits)
	}

	result, err := writer.Declare(t.Context(), token.ID, write.Declaration{Ref: jellyfin, Remove: true, Confirm: true})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !result.Removed || !target.commits[0].Delete || target.commits[0].ReplacingSHA == "" {
		t.Errorf("result = %+v, commit = %+v; want a guarded deletion", result, target.commits[0])
	}
	if _, present := target.files[jellyfinPath]; present {
		t.Error("included declaration remains after deletion")
	}
}

func TestDeclareWillNotRemoveTheRepositoryRoot(t *testing.T) {
	writer, target, tokens := newWriter(t, locatedContents(RootPath, rootFile), map[string]string{RootPath: rootFile})
	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	_, err := writer.Declare(t.Context(), token.ID, write.Declaration{Ref: jellyfin, Remove: true, Confirm: true})
	if err == nil || !strings.Contains(err.Error(), "opt the whole repository out") {
		t.Fatalf("error = %v, want root removal refused", err)
	}
	if len(target.commits) != 0 {
		t.Fatalf("root removal committed: %+v", target.commits)
	}
}
