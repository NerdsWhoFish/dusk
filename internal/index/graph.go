package index

import (
	"context"
	"fmt"
	"slices"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
)

// GraphNode is one entity and the knowledge attached to it.
type GraphNode struct {
	Entity *duskv1alpha1.Entity
	Notes  []*duskv1alpha1.Note
}

// Graph is the estate as entities joined by declared relations.
type Graph struct {
	Nodes     []GraphNode
	Relations []*duskv1alpha1.Relation
}

// Graph returns the default view as one graph-shaped read. It resolves
// duplicate observations the same way Get does and omits an edge unless both
// ends are visible, so a restricted graph cannot reveal a hidden ref.
func (db *DB) Graph(ctx context.Context, gitRef string, v Visibility) (Graph, error) {
	var entityRows []entityRow
	query := visible(scoped(db.gorm.WithContext(ctx), gitRef), v, "")
	if err := query.Order("ref, observed, repository").Find(&entityRows).Error; err != nil {
		return Graph{}, fmt.Errorf("index: graph entities at %q: %w", gitRef, err)
	}

	nodes := make([]GraphNode, 0, len(entityRows))
	byRef := make(map[string]int, len(entityRows))
	for _, row := range entityRows {
		if _, exists := byRef[row.Ref]; exists {
			continue
		}
		entity, err := row.entity()
		if err != nil {
			return Graph{}, err
		}
		byRef[row.Ref] = len(nodes)
		nodes = append(nodes, GraphNode{Entity: entity})
	}

	var relationRows []relationRow
	relationsQuery := visible(scoped(db.gorm.WithContext(ctx), gitRef).Model(&relationRow{}), v, "relations")
	if err := relationsQuery.Order("from_ref, to_ref, type, repository").Find(&relationRows).Error; err != nil {
		return Graph{}, fmt.Errorf("index: graph relations at %q: %w", gitRef, err)
	}
	relations, err := graphRelations(relationRows, byRef)
	if err != nil {
		return Graph{}, err
	}

	if len(nodes) == 0 {
		return Graph{Nodes: nodes, Relations: relations}, nil
	}
	if err := db.attachGraphNotes(ctx, gitRef, nodes, byRef); err != nil {
		return Graph{}, err
	}
	return Graph{Nodes: nodes, Relations: relations}, nil
}

func graphRelations(rows []relationRow, byRef map[string]int) ([]*duskv1alpha1.Relation, error) {
	relations := make([]*duskv1alpha1.Relation, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if _, ok := byRef[row.FromRef]; !ok {
			continue
		}
		if _, ok := byRef[row.ToRef]; !ok {
			continue
		}
		key := row.FromRef + "\x00" + row.Type + "\x00" + row.ToRef
		if seen[key] {
			continue
		}
		relation, err := row.relation()
		if err != nil {
			return nil, err
		}
		seen[key] = true
		relations = append(relations, relation)
	}
	return relations, nil
}

func (db *DB) attachGraphNotes(ctx context.Context, gitRef string, nodes []GraphNode, byRef map[string]int) error {
	var notes []noteRow
	if err := scoped(db.gorm.WithContext(ctx), gitRef).Order("pinned DESC, observed_at DESC, note_id").Find(&notes).Error; err != nil {
		return fmt.Errorf("index: graph notes at %q: %w", gitRef, err)
	}

	type noteKey struct {
		repository string
		gitRef     string
		id         string
	}
	byNote := make(map[noteKey]*duskv1alpha1.Note, len(notes))
	for _, row := range notes {
		byNote[noteKey{row.Repository, row.GitRef, row.NoteID}] = row.note()
	}

	var refs []noteRefRow
	if err := scoped(db.gorm.WithContext(ctx), gitRef).Order("ref, note_id").Find(&refs).Error; err != nil {
		return fmt.Errorf("index: graph note refs at %q: %w", gitRef, err)
	}
	for _, ref := range refs {
		node, visible := byRef[ref.Ref]
		if !visible {
			continue
		}
		note := byNote[noteKey{ref.Repository, ref.GitRef, ref.NoteID}]
		if note == nil {
			continue
		}
		nodes[node].Notes = append(nodes[node].Notes, note)
	}
	for i := range nodes {
		slices.SortStableFunc(nodes[i].Notes, func(a, b *duskv1alpha1.Note) int {
			if a.GetPinned() != b.GetPinned() {
				if a.GetPinned() {
					return -1
				}
				return 1
			}
			return compareNotes(a, b)
		})
	}
	return nil
}

func compareNotes(a, b *duskv1alpha1.Note) int {
	if a.GetKind() < b.GetKind() {
		return -1
	}
	if a.GetKind() > b.GetKind() {
		return 1
	}
	if a.GetId() < b.GetId() {
		return -1
	}
	if a.GetId() > b.GetId() {
		return 1
	}
	return 0
}
