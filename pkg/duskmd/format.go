package duskmd

import (
	"bytes"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// FormatRoot renders a repository's root dusk.md.
func FormatRoot(file *File) ([]byte, error) {
	return format(file, "")
}

// FormatIncluded renders a file reached through an include. The namespace is
// written only when it differs from inherited, so a file does not repeat what
// the root already says.
func FormatIncluded(file *File, inheritedNamespace string) ([]byte, error) {
	return format(file, inheritedNamespace)
}

// format writes the frontmatter from the entity and leaves the prose exactly as
// it was. The description is authored markdown, so rendering it through
// anything would quietly rewrite somebody's file.
func format(file *File, inheritedNamespace string) ([]byte, error) {
	if file == nil || file.Entity == nil {
		return nil, fmt.Errorf("duskmd: nothing to format")
	}

	front, err := frontmatterFor(file, inheritedNamespace)
	if err != nil {
		return nil, err
	}

	encoded, err := marshalFrontmatter(front)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(encoded)
	out.WriteString("---\n")

	if prose := strings.TrimSpace(file.Entity.GetDescription()); prose != "" {
		out.WriteString("\n")
		out.WriteString(prose)
		out.WriteString("\n")
	}
	return out.Bytes(), nil
}

func frontmatterFor(file *File, inheritedNamespace string) (frontmatter, error) {
	entity := file.Entity

	front := frontmatter{
		Dusk: SchemaVersion,
		Kind: entity.GetKind(),
		Name: entity.GetName(),
	}
	if entity.GetNamespace() != inheritedNamespace {
		front.Namespace = entity.GetNamespace()
	}
	// A title equal to the name is what the parser defaults to, so writing it
	// back would add a line that means nothing.
	if title := entity.GetTitle(); title != "" && title != entity.GetName() {
		front.Title = title
	}
	front.Include = file.Include
	front.ObservedAs = file.ObservedAs

	for _, edge := range file.Relations {
		front.Relations = append(front.Relations, relation{
			Type:       edge.GetType(),
			To:         edge.GetTo(),
			Attributes: edge.GetAttributes().AsMap(),
		})
	}
	if attributes := entity.GetAttributes(); attributes != nil {
		front.Attributes = attributes.AsMap()
	}
	return front, nil
}

// marshalFrontmatter encodes any frontmatter shape, so an entity and a note
// come out indented the same way rather than through two encoders that drift.
func marshalFrontmatter(front any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	if err := encoder.Encode(front); err != nil {
		return nil, fmt.Errorf("duskmd: encode frontmatter: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("duskmd: encode frontmatter: %w", err)
	}
	return buf.Bytes(), nil
}
