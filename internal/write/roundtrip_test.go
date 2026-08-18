package write_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/reconcile"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

const roundTripRef = "refs/heads/main"

// dirTarget is a repository on disk, so a write and a reconcile read the same
// bytes and nothing but the write path sits between them.
type dirTarget struct{ dir string }

func (d dirTarget) DefaultBranch(context.Context) (string, error) { return roundTripRef, nil }

func (d dirTarget) ReadFileContents(_ context.Context, _, filePath string) (*githubapp.FileContents, error) {
	data, err := os.ReadFile(filepath.Join(d.dir, filePath))
	if err != nil {
		return nil, err
	}
	return &githubapp.FileContents{Data: data, SHA: "blob-" + filePath}, nil
}

func (d dirTarget) CommitFile(_ context.Context, commit githubapp.FileCommit) (*githubapp.Commit, error) {
	path := filepath.Join(d.dir, commit.Path)
	if commit.Delete {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		return &githubapp.Commit{SHA: "c0ffee", URL: "https://example.com/commit/c0ffee"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, commit.Content, 0o600); err != nil {
		return nil, err
	}
	return &githubapp.Commit{SHA: "c0ffee", URL: "https://example.com/commit/c0ffee"}, nil
}

type dirRepositories struct{ target dirTarget }

func (d dirRepositories) Target(context.Context, string) (write.Target, error) { return d.target, nil }

// A relation is only worth writing if it comes back as a graph, so this drives
// the whole loop rather than the commit: declare, reconcile, relate, reconcile,
// and read it from both ends the way `get` and `neighbors` do.
func TestRelateReachesTheGraphThroughAReconcile(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, RootPath, rootFile)
	put(t, dir, "services/jellyfin/dusk.md", jellyfinFile)

	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	reindex(t, idx, dir)
	if relations, _ := idx.Neighbors(t.Context(), "", jellyfin); len(relations) != 0 {
		t.Fatalf("the fixture already declares %v", relations)
	}

	tokens := &proof.Store{}
	writer := &write.Writer{
		Catalog:      idx,
		Repositories: dirRepositories{target: dirTarget{dir: dir}},
		Proof:        tokens,
	}

	at, err := idx.Locate(t.Context(), "", jellyfin)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: at.Version})

	if _, err := writer.Relate(t.Context(), token.ID, write.Relation{
		From: jellyfin, To: "host:home/nas", Type: "runs_on",
	}); err != nil {
		t.Fatalf("Relate: %v", err)
	}
	reindex(t, idx, dir)

	// Declared by one entity, read from both: the inbound half is assembled by
	// the index across repositories rather than written anywhere (ADR-0026).
	for _, ref := range []string{jellyfin, "host:home/nas"} {
		t.Run("from "+ref, func(t *testing.T) {
			relations, err := idx.Neighbors(t.Context(), "", ref)
			if err != nil {
				t.Fatalf("Neighbors: %v", err)
			}
			if len(relations) != 1 {
				t.Fatalf("relations = %v, want the one that was written", relations)
			}
			if !declares(relations[0], jellyfin, "host:home/nas", "runs_on") {
				t.Errorf("relation = %+v, want jellyfin runs_on nas", relations[0])
			}
		})
	}
}

func declares(relation *duskv1alpha1.Relation, from, to, relType string) bool {
	return relation.GetFrom() == from && relation.GetTo() == to && relation.GetType() == relType
}

func put(t *testing.T, dir, path, body string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func reindex(t *testing.T, idx *index.DB, dir string) {
	t.Helper()

	source, err := reconcile.NewDir(dir, roundTripRef)
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}
	t.Cleanup(func() { _ = source.Close() })

	if _, err := reconcile.New(source, idx).Reconcile(t.Context(), repo, roundTripRef, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if err := idx.SetDefaultView(t.Context(), repo, roundTripRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}
}
