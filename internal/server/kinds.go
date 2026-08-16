package server

import (
	"net/http"

	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// kindJSON is one entry in the vocabulary as the browser reads it. The count is
// the viewer's, so this shape can never be a route around ADR-0051.
type kindJSON struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`

	// Role is resolved rather than raw, so a kind nobody minted still says what
	// it is for rather than saying nothing.
	Role string `json:"role"`

	// Count is a pointer because zero is an answer: a minted kind nothing
	// carries reports 0, and a namespace that is not counted here reports none.
	Count *int `json:"count,omitempty"`

	Aliases []string `json:"aliases,omitempty"`

	// AliasOf names the kind this spelling resolves to. Both halves are said
	// because the split is the thing worth seeing: an operator whose catalog
	// carries `service` and `svc` has two chips and one kind.
	AliasOf string `json:"alias_of,omitempty"`

	Minted bool `json:"minted,omitempty"`
}

// handleAPIKinds answers GET /api/kinds: what every entity kind is for and what
// else it is called. Without it an airport and a service read as the same sort
// of thing, when the catalog knows one is maintained and one is not (ADR-0048).
func (s *Server) handleAPIKinds(w http.ResponseWriter, r *http.Request) {
	minted, err := s.catalog.Minted(r.Context(), refOf(r))
	if err != nil {
		writeError(w, err)
		return
	}

	// The same read the overview makes, so the chips and their roles are counts
	// of one thing. The derived vocabulary counts every row in the estate.
	counts, err := s.catalog.Kinds(r.Context(), refOf(r), s.visibilityFor(r))
	if err != nil {
		writeError(w, err)
		return
	}

	kinds := make([]kindJSON, 0, len(counts)+len(minted))
	carried := make(map[string]bool, len(counts))
	for _, counted := range counts {
		count := counted.Count
		kinds = append(kinds, describeKind(counted.Kind, minted, &count))
		carried[counted.Kind] = true
	}

	// A kind somebody minted and nothing carries is worth saying: it is how
	// "minted and unused" is visible at all, and it costs a row.
	for _, kind := range minted {
		if kind.Namespace != vocab.Entity || carried[kind.Name] {
			continue
		}
		none := 0
		kinds = append(kinds, describeKind(kind.Name, minted, &none))
	}

	roles := make([]string, 0, 2)
	for _, role := range vocab.Roles(vocab.Entity) {
		roles = append(roles, string(role))
	}

	// The roles ride along in rank order, so the browser groups by them without
	// holding its own copy of what they are or which one comes first.
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles, "kinds": kinds})
}

// describeKind resolves what one carried name is for. A name matching a minted
// alias resolves to that kind's role and says which kind it is a spelling of.
func describeKind(name string, minted []vocab.Kind, count *int) kindJSON {
	out := kindJSON{
		Namespace: string(vocab.Entity),
		Kind:      name,
		Role:      string(vocab.RoleOf(vocab.Entity, name, minted)),
		Count:     count,
	}

	known, ok := vocab.Lookup(vocab.Entity, name, minted)
	switch {
	case !ok:
	case known.Name == name:
		out.Minted = true
		out.Aliases = known.Aliases
	default:
		out.AliasOf = known.Name
	}
	return out
}
