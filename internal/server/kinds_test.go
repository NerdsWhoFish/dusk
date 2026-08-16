package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

type kindAnswer struct {
	Roles []string `json:"roles"`
	Kinds []struct {
		Namespace string   `json:"namespace"`
		Kind      string   `json:"kind"`
		Role      string   `json:"role"`
		Count     *int     `json:"count"`
		Aliases   []string `json:"aliases"`
		AliasOf   string   `json:"alias_of"`
		Minted    bool     `json:"minted"`
	} `json:"kinds"`
}

func readKinds(t *testing.T, handler http.Handler) kindAnswer {
	t.Helper()

	rec := get(t, handler, "/api/kinds")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/kinds = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var answer kindAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return answer
}

func kindsHandler(t *testing.T) http.Handler {
	t.Helper()

	return build(t, setup{
		store:   registered(),
		catalog: emptyCatalog(t),
		env:     map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})
}

// The browser groups kinds by role, so the order the roles arrive in is the
// order they are shown in. Sending it means the browser holds no copy of what
// the roles are, which is what lets a third one arrive without a UI change.
func TestKindsAnswersWithTheRolesInRankOrder(t *testing.T) {
	answer := readKinds(t, kindsHandler(t))

	want := []string{"infrastructure", "reference"}
	if len(answer.Roles) != len(want) {
		t.Fatalf("roles = %v, want %v", answer.Roles, want)
	}
	for i, role := range want {
		if answer.Roles[i] != role {
			t.Errorf("roles[%d] = %q, want %q", i, answer.Roles[i], role)
		}
	}
}

// ADR-0051: a count is of what the viewer can see. This read is an aggregate
// over the estate, so it is the fourth one that could ship unfiltered, and the
// count has to come from the same narrowed read the overview makes.
func TestADR0051_AKindCountIsOfWhatTheViewerCanSee(t *testing.T) {
	answer := readKinds(t, kindsHandler(t))

	// An empty catalog is the honest case for a handler with no index fixture:
	// what matters is that nothing is invented, since a count appearing here
	// with no rows behind it could only have come from an unnarrowed read.
	for _, kind := range answer.Kinds {
		if kind.Count == nil {
			t.Errorf("%s carries no count, so the browser cannot show one", kind.Kind)
			continue
		}
		if *kind.Count != 0 {
			t.Errorf("%s counts %d in an empty catalog", kind.Kind, *kind.Count)
		}
	}
}

// ADR-0048: nothing is minted here, and a kind nobody minted still has a role.
// A catalog that never mints has to render exactly as it did before roles
// existed, which is what makes this safe to add to one that already exists.
func TestADR0048_AKindNobodyMintedStillCarriesARole(t *testing.T) {
	answer := readKinds(t, kindsHandler(t))

	for _, kind := range answer.Kinds {
		if kind.Role == "" {
			t.Errorf("%s has no role, so the browser has nothing to group it by", kind.Kind)
		}
		if kind.Minted {
			t.Errorf("%s reports minted in a catalog that has minted nothing", kind.Kind)
		}
	}
}
