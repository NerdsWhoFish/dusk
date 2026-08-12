package proof

import "fmt"

// Codes an agent can branch on. Read-before-write is an unusual contract, so
// the failures have to be machine-readable as well as legible.
const (
	// CodeRequired means no usable token was presented.
	CodeRequired = "E_PROOF_REQUIRED"
	// CodeUnseen means the token is valid but did not cover this entity.
	CodeUnseen = "E_PROOF_UNSEEN"
	// CodeStale means the entity changed after the read that issued the token.
	CodeStale = "E_PROOF_STALE"
	// CodeSearchRequired means creating needs a search, which can witness that
	// something was absent.
	CodeSearchRequired = "E_PROOF_SEARCH_REQUIRED"
	// CodeExists means the search that issued the token already found it.
	CodeExists = "E_ALREADY_EXISTS"
)

// Rejection refuses a write and names the exact call that would fix it.
//
// An unactionable error would make this invariant hostile rather than
// teachable, and an agent that cannot discover how to satisfy it will flail.
type Rejection struct {
	Code   string
	Ref    string
	Detail string
	Fix    string
}

func (r *Rejection) Error() string {
	return fmt.Sprintf("%s: %s: %s; call %s", r.Code, r.Ref, r.Detail, r.Fix)
}
