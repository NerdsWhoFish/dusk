package controller_test

import (
	"errors"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"github.com/NerdsWhoFish/dusk/internal/controller"
	"github.com/NerdsWhoFish/dusk/internal/index"
)

func TestPreviewStoresTheHeadUnderItsRepositoryScope(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{id: 10, account: "example", repos: map[string]string{
		"example/homelab": rootFile("jellyfin"), "example/other": rootFile("unrelated"),
	}}}}
	c, idx := newController(t, fake, "example", controller.Options{})
	if err := c.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	fake.moveTo("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	fake.installs[0].repos["example/homelab"] = rootFile("proposed")
	preview := controller.Preview{InstallationID: 10, Account: "example", Owner: "example", Name: "homelab", Number: 7, Head: fake.commit()}
	if err := c.SyncPreview(t.Context(), preview); err != nil {
		t.Fatal(err)
	}
	ref := controller.PreviewRef("example/homelab", 7)
	entity, err := idx.Get(t.Context(), ref, "service:home/proposed")
	if err != nil {
		t.Fatalf("preview cannot find indexed head: %v", err)
	}
	if entity.GetProvenance().GetVersion() != preview.Head {
		t.Errorf("provenance = %q, want immutable head %q", entity.GetProvenance().GetVersion(), preview.Head)
	}
	if _, err := idx.Get(t.Context(), ref, "service:home/unrelated"); err != nil {
		t.Fatalf("preview lost another repository's default view: %v", err)
	}
	changes, err := c.Compare(t.Context(), "example/homelab", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("preview diff = %+v, want only proposed addition and jellyfin removal", changes)
	}
	for _, change := range changes {
		if change.Ref == "service:home/unrelated" {
			t.Errorf("unrelated repository appeared in preview diff: %+v", change)
		}
	}
	if fake.calls["/repos/example/homelab/commits/"+preview.Head] == 0 {
		t.Error("preview never resolved its immutable head")
	}
}

func TestClosingPreviewPreservesOtherRepositoriesAndCanReopen(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{id: 10, account: "example", repos: map[string]string{"example/first": rootFile("first")}}}}
	c, idx := newController(t, fake, "example", controller.Options{})
	preview := controller.Preview{InstallationID: 10, Account: "example", Owner: "example", Name: "first", Number: 7, Head: fake.commit()}
	if err := c.SyncPreview(t.Context(), preview); err != nil {
		t.Fatal(err)
	}
	secondRef := controller.PreviewRef("example/second", 7)
	entity := &duskv1alpha1.Entity{Ref: "service:home/second", Kind: "service", Namespace: "home", Name: "second"}
	if err := idx.Put(t.Context(), "example/second", secondRef, []index.Declaration{{Path: "dusk.md", Entity: entity}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	preview.Closed = true
	if err := c.SyncPreview(t.Context(), preview); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.ResolvePreview(t.Context(), controller.PreviewRef("example/first", 7)); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("closed preview still resolves: %v", err)
	}
	if _, err := idx.Get(t.Context(), secondRef, entity.GetRef()); err != nil {
		t.Fatalf("closing first repository's PR deleted second repository's PR: %v", err)
	}
	preview.Closed = false
	if err := c.SyncPreview(t.Context(), preview); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Get(t.Context(), controller.PreviewRef("example/first", 7), "service:home/first"); err != nil {
		t.Fatalf("reopening unchanged head did not restore preview: %v", err)
	}
}

func TestRepositoryRegrantReindexesAnUnchangedCommit(t *testing.T) {
	installed := []install{{id: 10, account: "example", repos: map[string]string{"example/homelab": rootFile("jellyfin")}}}
	fake := &fakeGitHub{installs: installed}
	c, idx := newController(t, fake, "example", controller.Options{})
	if err := c.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	fake.installs = nil
	if err := c.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	fake.installs = installed
	if err := c.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Get(t.Context(), mainRef, "service:home/jellyfin"); err != nil {
		t.Fatalf("restored repository stayed absent after successful sweep: %v", err)
	}
}

func TestPreviewCanDeleteTheRootCatalogFile(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{id: 10, account: "example", repos: map[string]string{"example/homelab": rootFile("jellyfin")}}}}
	c, idx := newController(t, fake, "example", controller.Options{})
	if err := c.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	fake.moveTo("cccccccccccccccccccccccccccccccccccccccc")
	fake.installs[0].repos["example/homelab"] = ""
	if err := c.SyncPreview(t.Context(), controller.Preview{InstallationID: 10, Account: "example", Owner: "example", Name: "homelab", Number: 8, Head: fake.commit()}); err != nil {
		t.Fatal(err)
	}
	ref := controller.PreviewRef("example/homelab", 8)
	if _, err := idx.ResolvePreview(t.Context(), ref); err != nil {
		t.Fatalf("empty preview was mistaken for an absent snapshot: %v", err)
	}
	changes, err := c.Compare(t.Context(), "example/homelab", 8)
	if err != nil || len(changes) != 1 || changes[0].Kind != index.ChangeRemoved {
		t.Fatalf("root deletion diff = %+v, error %v", changes, err)
	}
}

func TestRevokingARepositoryPrunesEmptyDefaultsNotesAndPreviews(t *testing.T) {
	fake := &fakeGitHub{installs: []install{{id: 10, account: "example", repos: map[string]string{"example/retained": ""}}}}
	c, idx := newController(t, fake, "example", controller.Options{})
	for _, repository := range []string{"example/retained", "example/revoked"} {
		if err := idx.SetDefaultView(t.Context(), repository, mainRef); err != nil {
			t.Fatal(err)
		}
		if err := idx.Put(t.Context(), repository, controller.PreviewRef(repository, 7), nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	note := &duskv1alpha1.Note{Id: ".dusk/memo.md", Kind: "note", Body: "revoked knowledge"}
	if err := idx.Put(t.Context(), "example/notes-only", mainRef, nil, nil, []*duskv1alpha1.Note{note}); err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	scopes, err := idx.Scopes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range scopes {
		if scope.Repository != "example/retained" {
			t.Errorf("revoked scope remains materialized: %+v", scope)
		}
	}
	if len(scopes) != 2 {
		t.Fatalf("remaining scopes = %+v, want retained default and empty preview", scopes)
	}
	if _, err := idx.ResolvePreview(t.Context(), controller.PreviewRef("example/revoked", 7)); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("revoked preview is still readable: %v", err)
	}
	if _, err := idx.ResolvePreview(t.Context(), controller.PreviewRef("example/retained", 7)); err != nil {
		t.Fatalf("reachable repository lost its empty preview: %v", err)
	}
}
