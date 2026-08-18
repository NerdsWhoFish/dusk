package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/controller"
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
