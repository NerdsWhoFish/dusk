package server_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/controller"
	"github.com/NerdsWhoFish/dusk/internal/index"
)

type fixedSyncs []controller.Status

func (s fixedSyncs) Status() []controller.Status { return s }

func TestHomeCarriesRepositoryCheckpointsForTheOperator(t *testing.T) {
	handler := build(t, setup{
		store:   registered(),
		catalog: emptyCatalog(t),
		syncs: fixedSyncs{{
			Repository:    "example/homelab",
			Commit:        "0123456789abcdef",
			Participating: true,
		}},
		env: map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})

	rec := get(t, handler, "/api/home")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/home = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var answer struct {
		Repositories []controller.Status `json:"repositories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode home: %v", err)
	}
	if len(answer.Repositories) != 1 || answer.Repositories[0].Commit != "0123456789abcdef" {
		t.Fatalf("repositories = %+v", answer.Repositories)
	}
}

func TestSearchNoteHasAReadableDestination(t *testing.T) {
	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	note := &duskv1alpha1.Note{
		Id: ".dusk/howto-ed383fb8.md", Kind: "howto",
		Body: "How jdflora is deployed.", Refs: []string{"service:home/jdflora"},
		ContentHash: "note-version",
	}
	if err := db.Put(t.Context(), "example/homelab", "refs/heads/main", nil, nil, []*duskv1alpha1.Note{note}); err != nil {
		t.Fatalf("seed note: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), "example/homelab", "refs/heads/main"); err != nil {
		t.Fatalf("set default view: %v", err)
	}

	handler := build(t, setup{
		store: registered(), catalog: db,
		env: map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})
	rec := get(t, handler, "/api/notes/.dusk%2Fhowto-ed383fb8.md")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET note = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var answer struct {
		Note struct {
			ID string `json:"id"`
		} `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode note: %v", err)
	}
	if answer.Note.ID != note.GetId() {
		t.Fatalf("note = %+v, want the searched note", answer.Note)
	}
}
