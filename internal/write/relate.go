package write

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/NerdsWhoFish/dusk-plugin-sdk/conformance"
	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// Relation is an edge an agent asked to declare. It originates from From and
// can originate from nothing else, because a file declares only its own
// outbound edges (ADR-0026).
type Relation struct {
	From       string
	To         string
	Type       string
	Repository string
	Attributes map[string]string
	Unset      []string
	Remove     bool
	Confirm    bool
}

// Relate declares an outbound relation in the file that declares From. Whether
// To resolves is deliberately unchecked, because the other end may live in a
// repository Dusk was never shown: integrity reports it (ADR-0033).
func (w *Writer) Relate(ctx context.Context, token string, relation Relation) (*Result, error) {
	from, to, relType, err := relation.parts()
	if err != nil {
		return nil, err
	}

	// A relation is a change to the source entity's own declaration, so it
	// routes and is authorized exactly as an update to that entity is.
	at, err := w.Catalog.LocateIn(ctx, "", from, relation.Repository)
	if errors.Is(err, index.ErrNotFound) {
		return nil, fmt.Errorf("write: nothing declares %s, and a relation lives in the file of the entity it points from. Declare that entity before relating it to anything", from)
	}
	if err != nil {
		return nil, err
	}
	if err := w.Proof.AuthorizeUpdate(token, proof.Entity(from), at.Version); err != nil {
		return nil, err
	}

	target, err := w.Repositories.Target(ctx, at.Repository)
	if err != nil {
		return nil, err
	}
	branch, err := target.DefaultBranch(ctx)
	if err != nil {
		return nil, err
	}

	file, contents, err := w.read(ctx, target, branch, at.Path)
	if err != nil {
		return nil, err
	}
	if err := currentDeclaration(from, at, contents.Data); err != nil {
		return nil, err
	}

	changed, err := applyRelation(file, relation, from, to, relType, at.Path)
	if err != nil {
		return nil, err
	}
	if !changed {
		return &Result{Ref: from, Repository: at.Repository, Path: at.Path, Existing: true}, nil
	}

	rendered, err := render(file, at.Path)
	if err != nil {
		return nil, err
	}

	return w.land(ctx, change{
		target:     target,
		repository: at.Repository,
		ref:        from,
		before:     contents.Data,
		commit: githubapp.FileCommit{
			Branch:       branch,
			Path:         at.Path,
			Message:      relationMessage(relation.Remove, from, relType, to),
			Content:      rendered,
			ReplacingSHA: contents.SHA,
		},
	})
}

func applyRelation(file *duskmd.File, relation Relation, from, to, relType, path string) (bool, error) {
	edge := relationIndex(file.Relations, relType, to)
	if relation.Remove {
		if !relation.Confirm {
			return false, fmt.Errorf("write: withdrawing %s %s %s removes the edge from %s; pass confirm only if that is intended", from, relType, to, path)
		}
		if edge < 0 {
			return false, nil
		}
		file.Relations = append(file.Relations[:edge], file.Relations[edge+1:]...)
		return true, nil
	}
	if edge < 0 {
		if len(relation.Unset) > 0 {
			return false, fmt.Errorf("write: cannot unset attributes on %s %s %s because that relation is not declared", from, relType, to)
		}
		attributes, err := relationAttributes(nil, relation)
		if err != nil {
			return false, err
		}
		file.Relations = append(file.Relations, &duskv1alpha1.Relation{
			From: from, To: to, Type: relType, Attributes: attributes,
		})
		return true, nil
	}

	before := file.Relations[edge].GetAttributes().AsMap()
	attributes, err := relationAttributes(file.Relations[edge].GetAttributes(), relation)
	if err != nil {
		return false, err
	}
	if reflect.DeepEqual(before, attributes.AsMap()) {
		return false, nil
	}
	file.Relations[edge].Attributes = attributes
	return true, nil
}

// parts validates what an agent supplied. The target's *syntax* is checked
// while its existence is not: a ref the parser refuses would be committed and
// then fail the whole file, taking the entity out of the catalog.
func (r Relation) parts() (from, to, relType string, err error) {
	from = strings.TrimSpace(r.From)
	to = strings.TrimSpace(r.To)
	relType = strings.TrimSpace(r.Type)

	switch {
	case from == "":
		return "", "", "", errors.New("write: a relation needs the ref it points from, which is the entity whose file will declare it")
	case to == "":
		return "", "", "", errors.New("write: a relation needs the ref it points at")
	case relType == "":
		return "", "", "", errors.New("write: a relation needs a type, such as runs_on or depends_on")
	}

	// Ordered, so an agent that got both refs wrong is told about the same one
	// every time rather than whichever a map handed over first.
	for _, checked := range []struct{ field, ref string }{{"from", from}, {"to", to}} {
		if _, _, _, err := conformance.ParseRef(checked.ref); err != nil {
			return "", "", "", fmt.Errorf("write: %s %q is not a ref of the form kind:namespace/name: %w",
				checked.field, checked.ref, err)
		}
	}
	return from, to, relType, nil
}

// alreadyDeclares reports whether the file carries this exact edge. A second
// copy of one says nothing the first did not, so it is answered rather than
// committed, the way an identical note is (ADR-0053).
func relationIndex(relations []*duskv1alpha1.Relation, relType, to string) int {
	for i, relation := range relations {
		if relation.GetType() == relType && relation.GetTo() == to {
			return i
		}
	}
	return -1
}

func relationAttributes(current *structpb.Struct, relation Relation) (*structpb.Struct, error) {
	merged := current.AsMap()
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range relation.Attributes {
		merged[key] = value
	}
	for _, raw := range relation.Unset {
		key := strings.TrimSpace(raw)
		if key == "" || strings.Contains(key, ".") {
			return nil, fmt.Errorf("write: relation attribute to unset must be one name, got %q", raw)
		}
		if _, set := relation.Attributes[key]; set {
			return nil, fmt.Errorf("write: relation attribute %q cannot be set and unset in the same call", key)
		}
		delete(merged, key)
	}
	attributes, err := structpb.NewStruct(merged)
	if err != nil {
		return nil, fmt.Errorf("write: relation attributes: %w", err)
	}
	return attributes, nil
}

func relationMessage(remove bool, from, relType, to string) string {
	verb := "declare"
	if remove {
		verb = "withdraw"
	}
	return fmt.Sprintf("relate: %s %s %s %s", verb, from, relType, to)
}
