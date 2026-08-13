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
	f.files[commit.Path] = string(commit.Content)
	return &githubapp.Commit{SHA: "c0ffee", URL: "https://github.com/example/homelab/commit/c0ffee"}, nil
}

type fakeRepositories struct{ target *fakeTarget }

func (f fakeRepositories) Target(_ context.Context, slug string) (write.Target, error) {
	if slug != repo {
		return nil, fmt.Errorf("write: no installation grants access to %q", slug)
	}
	return f.target, nil
}

type fakeCatalog struct{ at *index.Location }

func (f fakeCatalog) Locate(_ context.Context, _, entityRef string) (*index.Location, error) {
	if f.at == nil {
		return nil, fmt.Errorf("locate %q: %w", entityRef, index.ErrNotFound)
	}
	return f.at, nil
}

func (f fakeCatalog) Get(context.Context, string, string) (*duskv1alpha1.Entity, error) {
	return nil, index.ErrNotFound
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
	return &index.Location{Repository: repo, GitRef: "refs/heads/main", Path: path, Version: "v1"}
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

// Dusk must never create a dusk.md: that file is how a repository consents, and
// creating one would be Dusk granting itself permission.
func TestDeclareWillNotOptARepositoryIn(t *testing.T) {
	writer, target, tokens := newWriter(t, nil, map[string]string{})

	token := tokens.Issue(proof.FromSearch, nil)
	_, err := writer.Declare(t.Context(), token.ID, write.Declaration{
		Ref: "service:home/navidrome", Repository: repo,
	})
	if err == nil {
		t.Fatal("Declare succeeded against a repository that never opted in")
	}
	if !strings.Contains(err.Error(), "consent") {
		t.Errorf("error = %q, want it to explain why Dusk will not add the file", err)
	}
	if len(target.commits) != 0 {
		t.Errorf("it wrote anyway: %+v", target.commits)
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
