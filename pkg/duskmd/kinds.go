package duskmd

import (
	"bytes"
	"errors"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// WellKnownNoteKinds seed the vocabulary, per ADR-0010. The list is derived
// from vocab so the names and their roles cannot disagree.
var WellKnownNoteKinds = vocab.Names(vocab.WellKnown())

// Working are the kinds a status means something for. A gotcha is never done;
// an idea and a todo are the things somebody finishes or abandons.
var Working = vocab.Names(vocab.WithRole(vocab.WellKnown(), vocab.Work))

// Kinds is a parsed vocabulary file: the kinds it mints, and its own prose.
type Kinds struct {
	Kinds []vocab.Kind

	// Body is whatever the operator wrote about their taxonomy, written back
	// untouched the way a note's prose is.
	Body string
}

// kindsFrontmatter is the authorable shape. Two lists rather than one with a
// discriminator, because a kind belongs to exactly one namespace.
type kindsFrontmatter struct {
	Dusk     string      `yaml:"dusk"`
	Entities []kindEntry `yaml:"entities,omitempty"`
	Notes    []kindEntry `yaml:"notes,omitempty"`
}

type kindEntry struct {
	Name    string   `yaml:"name"`
	Role    string   `yaml:"role"`
	Aliases []string `yaml:"aliases,omitempty"`
}

var knownKindsFields = map[string]bool{"dusk": true, "entities": true, "notes": true}

var knownKindFields = map[string]bool{"name": true, "role": true, "aliases": true}

// ParseKinds reads the file a repository mints kinds in. Unknown fields are
// rejected rather than ignored, so a misspelled key fails loudly instead of
// silently minting nothing.
func ParseKinds(filePath string, data []byte) (*Kinds, error) {
	front, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, Errors{{Path: filePath, Message: err.Error()}}
	}

	mapping, err := frontmatterMapping(front)
	if err != nil {
		return nil, Errors{{Path: filePath, Message: err.Error()}}
	}

	c := &collector{path: filePath}
	lines := keyLines(mapping)
	checkKeys(c, mapping, knownKindsFields, nil)
	checkKindKeys(c, mapping)

	var fm kindsFrontmatter
	if err := mapping.Decode(&fm); err != nil {
		c.decodeError(err)
		return nil, c.err()
	}
	c.checkKindsVersion(fm, lines)

	kinds := c.kinds(fm, lines)
	if err := c.err(); err != nil {
		return nil, err
	}
	return &Kinds{Kinds: kinds, Body: strings.TrimSpace(string(body))}, nil
}

func (c *collector) checkKindsVersion(fm kindsFrontmatter, lines map[string]int) {
	if fm.Dusk != SchemaVersion {
		c.at(lines["dusk"], "dusk", `is required and must be "`+SchemaVersion+`"`)
	}
}

// kinds validates both lists. A kind colliding with one already read is
// refused here rather than downstream, because two rows meaning one thing is
// what the vocabulary exists to prevent.
func (c *collector) kinds(fm kindsFrontmatter, lines map[string]int) []vocab.Kind {
	var kinds []vocab.Kind
	for _, group := range []struct {
		namespace vocab.Namespace
		field     string
		entries   []kindEntry
	}{
		{vocab.Entity, "entities", fm.Entities},
		{vocab.Note, "notes", fm.Notes},
	} {
		for i, entry := range group.entries {
			field := group.field + "[" + strconv.Itoa(i) + "]"
			kind, ok := c.kind(group.namespace, entry, field, lines[group.field])
			if !ok {
				continue
			}
			if _, clash := vocab.Lookup(kind.Namespace, kind.Name, kinds); clash {
				c.at(lines[group.field], field+".name",
					"is already minted in this file, or is another spelling of one that is")
				continue
			}
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func (c *collector) kind(namespace vocab.Namespace, entry kindEntry, field string, line int) (vocab.Kind, bool) {
	name, err := vocab.ValidName(entry.Name)
	if err != nil {
		c.at(line, field+".name", err.Error())
		return vocab.Kind{}, false
	}

	role, err := vocab.ValidRole(namespace, entry.Role)
	if err != nil {
		c.at(line, field+".role", err.Error())
		return vocab.Kind{}, false
	}

	aliases := make([]string, 0, len(entry.Aliases))
	for i, alias := range entry.Aliases {
		valid, err := vocab.ValidName(alias)
		if err != nil {
			c.at(line, field+".aliases["+strconv.Itoa(i)+"]", err.Error())
			continue
		}
		aliases = append(aliases, valid)
	}

	return vocab.Kind{
		Namespace: namespace, Name: name, Role: role,
		Aliases: aliases, Minted: true,
	}, true
}

// checkKindKeys inspects each list element as nodes, because an unknown key
// inside a list element is invisible once the list has been decoded.
func checkKindKeys(c *collector, m *yaml.Node) {
	for _, field := range []string{"entities", "notes"} {
		seq := valueNode(m, field)
		if seq == nil || seq.Kind != yaml.SequenceNode {
			continue
		}
		for _, element := range seq.Content {
			if element.Kind == yaml.MappingNode {
				checkKeys(c, element, knownKindFields, nil)
			}
		}
	}
}

// FormatKinds writes a vocabulary file back. Kinds are sorted, so two mints in
// either order produce the same file and a diff shows only what changed.
func FormatKinds(kinds *Kinds) ([]byte, error) {
	if kinds == nil {
		return nil, errNothingToFormat
	}

	fm := kindsFrontmatter{Dusk: SchemaVersion}
	sorted := slices.Clone(kinds.Kinds)
	slices.SortFunc(sorted, func(a, b vocab.Kind) int { return strings.Compare(a.Name, b.Name) })

	for _, kind := range sorted {
		entry := kindEntry{Name: kind.Name, Role: string(kind.Role), Aliases: kind.Aliases}
		if kind.Namespace == vocab.Entity {
			fm.Entities = append(fm.Entities, entry)
			continue
		}
		fm.Notes = append(fm.Notes, entry)
	}
	if len(fm.Entities) == 0 && len(fm.Notes) == 0 {
		return nil, errNoKindsToMint
	}

	encoded, err := marshalFrontmatter(fm)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(encoded)
	out.WriteString("---\n")
	if body := strings.TrimSpace(kinds.Body); body != "" {
		out.WriteString("\n")
		out.WriteString(body)
		out.WriteString("\n")
	}
	return out.Bytes(), nil
}

var errNoKindsToMint = errors.New("duskmd: a vocabulary file with no kinds in it would mint nothing")
