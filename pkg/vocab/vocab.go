// Package vocab is the catalog's vocabulary of kinds.
//
// A kind is an open string ([ADR-0007]), so the vocabulary is whatever is
// declared today. Minting adds the two things counting rows cannot reach: a
// role, which is what a kind is for, and aliases, which is what else it is
// called. Every role has a consequence, so nothing minted here is decorative
// ([ADR-0048], [ADR-0049]).
//
// It also owns the near-match rule, because a vocabulary that accepts every
// string silently fragments into `service`, `Service` and `services`, and one
// implementation of "did you mean" is the difference between a warning and
// three warnings that disagree.
//
// [ADR-0007]: https://github.com/NerdsWhoFish/dusk/blob/main/adr/0007-entity-schema.md
// [ADR-0048]: https://github.com/NerdsWhoFish/dusk/blob/main/adr/0048-the-kind-vocabulary.md
// [ADR-0049]: https://github.com/NerdsWhoFish/dusk/blob/main/adr/0049-a-notes-kind-is-its-rank.md
package vocab

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/NerdsWhoFish/dusk/pkg/catalogfs"
)

// Path is where a repository declares its minted kinds. `catalogfs` owns the
// constant, because that is what keeps a reconcile from reading it as catalog
// content (ADR-0048).
const Path = catalogfs.KindsPath

// Namespace separates the two vocabularies. ADR-0010 keeps note kinds and
// entity kinds in one machinery and two sets, because `incident` is a sensible
// note and a nonsensical host.
type Namespace string

// The namespaces. There are exactly two and there is no plan for a third.
const (
	// Entity is the kind on an entity: service, host, datastore.
	Entity Namespace = "entity"

	// Note is the kind on a note: gotcha, runbook, todo.
	Note Namespace = "note"
)

// Namespaces lists them in the order they are rendered.
var Namespaces = []Namespace{Entity, Note}

// Role is what a kind is for. It is required on a mint, because a kind that
// changes nothing would be chosen carelessly and would be worth nothing.
type Role string

// The entity roles. They decide whether drift expects the kind to be declared.
const (
	// Infrastructure is something somebody maintains, so one running and
	// undeclared is a gap worth reporting.
	Infrastructure Role = "infrastructure"

	// Reference is a fact about the world. Nobody is going to declare an
	// airport, so drift stays quiet about the ones it has not been told about.
	Reference Role = "reference"
)

// The note roles. They decide rank, and whether a status means anything.
const (
	// Warning is something that will bite you: a gotcha, an incident.
	Warning Role = "warning"

	// Knowledge is how a thing works or why it is that way.
	Knowledge Role = "knowledge"

	// Work is something not done yet, so it can be closed and it never
	// outranks anything in a search.
	Work Role = "work"
)

// Kind is one entry in the vocabulary.
type Kind struct {
	Namespace Namespace
	Name      string

	// Role is resolved rather than raw: a kind nobody minted carries the
	// default for its namespace, so nothing is ever role-less.
	Role Role

	// Aliases are the other names for it. Nothing can work out that `svc`
	// means `service`, which is why they are declared rather than guessed.
	Aliases []string

	// Count is how many things carry it. Zero means minted and unused, or one
	// of the well-known note kinds nobody has written yet.
	Count int

	// Minted is whether somebody declared it, as opposed to it existing only
	// because something carries it.
	Minted bool
}

// wellKnown seeds the note vocabulary, per ADR-0010. Their roles are what
// ADR-0049 derives the well-known ranking from, so this is the one list.
var wellKnown = []Kind{
	{Namespace: Note, Name: "gotcha", Role: Warning},
	{Namespace: Note, Name: "incident", Role: Warning},
	{Namespace: Note, Name: "runbook", Role: Knowledge},
	{Namespace: Note, Name: "howto", Role: Knowledge},
	{Namespace: Note, Name: "decision", Role: Knowledge},
	{Namespace: Note, Name: "todo", Role: Work},
	{Namespace: Note, Name: "idea", Role: Work},
}

// WellKnown returns the seeded note kinds, in rank order.
func WellKnown() []Kind { return slices.Clone(wellKnown) }

// Names returns the names of kinds, which is what an error message wanting to
// list a vocabulary needs.
func Names(kinds []Kind) []string {
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, kind.Name)
	}
	return names
}

// WithRole returns the kinds carrying role.
func WithRole(kinds []Kind, role Role) []Kind {
	var out []Kind
	for _, kind := range kinds {
		if kind.Role == role {
			out = append(out, kind)
		}
	}
	return out
}

// Roles lists the roles a namespace accepts, in rank order.
func Roles(namespace Namespace) []Role {
	if namespace == Entity {
		return []Role{Infrastructure, Reference}
	}
	return []Role{Warning, Knowledge, Work}
}

// DefaultRole is what a kind nobody minted carries, so nothing changes until
// somebody mints. Notes default to the middle rather than to either end, for
// the reasons in ADR-0049.
func DefaultRole(namespace Namespace) Role {
	if namespace == Entity {
		return Infrastructure
	}
	return Knowledge
}

// ValidNamespace parses a namespace, naming both when it is neither.
func ValidNamespace(namespace string) (Namespace, error) {
	for _, candidate := range Namespaces {
		if string(candidate) == strings.ToLower(strings.TrimSpace(namespace)) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("a namespace is entity or note, and %q is neither", namespace)
}

// RoleList names the roles a namespace takes, in rank order.
func RoleList(namespace Namespace) string {
	names := make([]string, 0, 3)
	for _, candidate := range Roles(namespace) {
		names = append(names, string(candidate))
	}
	return strings.Join(names, ", ")
}

// RoleExpectation is what a role field must say, phrased once so a parser's
// field error and a standalone rejection cannot word it differently.
func RoleExpectation(namespace Namespace) string {
	return fmt.Sprintf("is required, and for %s kinds must be one of %s", namespace, RoleList(namespace))
}

// NameExpectation is what a kind name must be. A kind ends up inside a ref and
// inside the index, so it is an identifier.
const NameExpectation = "is required, and must be an identifier: letters, digits, and any of -_."

// ValidRole parses a role for a namespace. A note role on an entity kind is a
// mistake worth catching, because nothing downstream would act on it.
func ValidRole(namespace Namespace, role string) (Role, error) {
	wanted := strings.ToLower(strings.TrimSpace(role))
	for _, candidate := range Roles(namespace) {
		if string(candidate) == wanted {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%q is not a role for %s kinds, which take %s", role, namespace, RoleList(namespace))
}

// ValidName accepts a kind name, against NameExpectation.
func ValidName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("a kind needs a name")
	}
	for _, r := range trimmed {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.", r) {
			continue
		}
		return "", fmt.Errorf("%q is not a kind name: a kind is an identifier of letters, digits and the joiners -_. , and this one contains %q",
			trimmed, string(r))
	}
	return trimmed, nil
}

// Rank orders notes by role: a warning first, then knowledge, then work.
// Anything else sorts with knowledge, so an entity role passed here by mistake
// does not sort to a surprising end.
func Rank(role Role) int {
	switch role {
	case Warning:
		return 0
	case Work:
		return 2
	default:
		return 1
	}
}

// RoleOf resolves what a name is for. A minted kind, matched on its name or one
// of its aliases, wins; then the well-known set; then the namespace default.
func RoleOf(namespace Namespace, name string, minted []Kind) Role {
	if kind, ok := Lookup(namespace, name, minted); ok && kind.Role != "" {
		return kind.Role
	}
	if kind, ok := Lookup(namespace, name, wellKnown); ok {
		return kind.Role
	}
	return DefaultRole(namespace)
}

// Lookup finds the kind a name resolves to, matching its name or any of its
// aliases after normalization. The bool distinguishes "not in the vocabulary"
// from a zero Kind.
func Lookup(namespace Namespace, name string, kinds []Kind) (Kind, bool) {
	wanted := Normalize(name)
	if wanted == "" {
		return Kind{}, false
	}

	for _, kind := range kinds {
		if kind.Namespace != namespace {
			continue
		}
		if Normalize(kind.Name) == wanted {
			return kind, true
		}
		for _, alias := range kind.Aliases {
			if Normalize(alias) == wanted {
				return kind, true
			}
		}
	}
	return Kind{}, false
}

// Normalize is the collision rule: two names that normalize the same are one
// kind spelled two ways. It is deliberately conservative, folding only case and
// separators, because refusing a mint on it has to be right every time.
func Normalize(name string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Nearest returns the kinds name is probably a misspelling of, closest first.
// An exact match is not near, it is the same kind, so Lookup answers that.
func Nearest(namespace Namespace, name string, kinds []Kind) []Kind {
	wanted := Normalize(name)
	if wanted == "" {
		return nil
	}

	type scored struct {
		kind     Kind
		distance int
	}

	var found []scored
	for _, kind := range kinds {
		if kind.Namespace != namespace {
			continue
		}
		distance, ok := near(wanted, Normalize(kind.Name))
		if !ok || distance == 0 {
			continue
		}
		found = append(found, scored{kind: kind, distance: distance})
	}

	slices.SortFunc(found, func(a, b scored) int {
		if a.distance != b.distance {
			return a.distance - b.distance
		}
		return strings.Compare(a.kind.Name, b.kind.Name)
	})

	out := make([]Kind, 0, len(found))
	for _, s := range found {
		out = append(out, s.kind)
	}
	return out
}

// near reports whether two normalized names are close enough to be worth
// mentioning, and how close. A prefix counts because `host` and `hostname` are
// the same fragmentation as `host` and `hosts` without being a typo.
func near(a, b string) (int, bool) {
	if a == "" || b == "" {
		return 0, false
	}
	if a == b {
		return 0, true
	}

	longer := max(len(a), len(b))
	shorter := min(len(a), len(b))
	if shorter >= prefixFloor && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a)) {
		return longer - shorter, true
	}

	budget := 1
	if longer > shortName {
		budget = 2
	}
	distance := editDistance(a, b)
	return distance, distance <= budget
}

const (
	// prefixFloor keeps two-letter kinds from matching everything starting
	// with them, since `db` would otherwise be near `dbadmin`.
	prefixFloor = 3

	// shortName is where one edit stops being most of the word.
	shortName = 5
)

// editDistance is optimal string alignment: Levenshtein plus transposition, so
// `serivce` is one edit from `service` rather than the two plain Levenshtein
// charges for the typo this exists to catch.
func editDistance(a, b string) int {
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}
