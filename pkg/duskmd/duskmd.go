// Package duskmd parses dusk.md catalog files into the schema published by the
// plugin SDK.
//
// A catalog file declares exactly one entity. Its frontmatter carries that
// entity's identity and the relations that originate from it, and everything
// below the frontmatter is the entity's description. The rules enforced here,
// and the reasoning behind them, are recorded in ADR-0026.
//
// This package is the normalization edge for declared data, which ADR-0026
// places here: what it returns is final, and nothing downstream re-derives it.
package duskmd

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/NerdsWhoFish/dusk-plugin-sdk/conformance"
	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"go.yaml.in/yaml/v3"
)

// SchemaVersion is the only value the dusk key accepts.
const SchemaVersion = "v1alpha1"

// Source is recorded as the provenance of everything declared in a catalog
// file, distinguishing it from data a plugin observed.
const Source = "dusk.md"

// frontmatterLineOffset converts a line number within the frontmatter block to
// a line number within the file, which is the only one an author can act on.
const frontmatterLineOffset = 1

// Provenance identifies the state of the repository a file was read from. It is
// supplied by the caller and never inferred, per ADR-0007.
type Provenance struct {
	// Version is the git SHA the file was read at.
	Version string

	// ObservedAt is when the read happened. It is passed in rather than taken
	// from the clock so that reconciling the same ref twice produces the same
	// result, and so that tests do not need to control time.
	ObservedAt time.Time
}

// File is one parsed catalog file.
type File struct {
	// Path is the file's path within the repository.
	Path string

	// Entity is the single entity the file declares.
	Entity *duskv1alpha1.Entity

	// Relations all originate from Entity. A file cannot declare an edge from
	// anything else, which is what keeps a repository from asserting facts
	// about entities it does not own.
	Relations []*duskv1alpha1.Relation

	// Include holds the glob patterns a root dusk.md points at. Expanding them
	// against a tree needs a filesystem and belongs to the reconciler.
	Include []string

	// ObservedAs names what an ingester calls this same thing. A human writes
	// `service:home/jellyfin` and Kubernetes calls it `service:prod/media-
	// jellyfin`; without the mapping, drift reports both as drift.
	ObservedAs []string
}

// ParseRoot parses a repository's root dusk.md. The root must declare its own
// namespace, and it is the only file permitted to use include.
func ParseRoot(filePath string, data []byte, p Provenance) (*File, error) {
	return parse(filePath, data, p, config{root: true})
}

// ParseIncluded parses a file reached through a root dusk.md's include. The
// namespace is inherited from that root and may be overridden by the file.
func ParseIncluded(filePath string, data []byte, namespace string, p Provenance) (*File, error) {
	return parse(filePath, data, p, config{namespace: namespace})
}

type config struct {
	root      bool
	namespace string
}

// frontmatter is the authorable shape. Fields absent from it are rejected
// rather than ignored, so a misspelling fails loudly instead of producing a
// catalog that is confidently wrong.
type frontmatter struct {
	Dusk       string         `yaml:"dusk"`
	Namespace  string         `yaml:"namespace,omitempty"`
	Kind       string         `yaml:"kind"`
	Name       string         `yaml:"name"`
	Title      string         `yaml:"title,omitempty"`
	ObservedAs []string       `yaml:"observed_as,omitempty"`
	Relations  []relation     `yaml:"relations,omitempty"`
	Attributes map[string]any `yaml:"attributes,omitempty"`
	Include    []string       `yaml:"include,omitempty"`
}

type relation struct {
	Type       string         `yaml:"type"`
	To         string         `yaml:"to"`
	Attributes map[string]any `yaml:"attributes,omitempty"`
}

var knownFields = map[string]bool{
	"dusk": true, "namespace": true, "kind": true, "name": true,
	"title": true, "relations": true, "attributes": true, "include": true,
	"observed_as": true,
}

// derivedFields carry their own message. Being told a field is unknown when it
// is in fact computed sends the author hunting for a typo they did not make.
var derivedFields = map[string]string{
	"ref":         "ref is derived from kind, namespace and name, so it cannot be declared here",
	"description": "description is the prose below the frontmatter, not a field",
}

var knownRelationFields = map[string]bool{"type": true, "to": true, "attributes": true}

var derivedRelationFields = map[string]string{
	"from": "from is always this file's own entity, so an edge in the other direction is declared by the repository that owns the other end",
}

func parse(filePath string, data []byte, p Provenance, cfg config) (*File, error) {
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
	checkKeys(c, mapping, knownFields, derivedFields)

	var fm frontmatter
	if err := mapping.Decode(&fm); err != nil {
		c.decodeError(err)
		return nil, c.err()
	}

	kind, namespace, name := c.checkIdentity(fm, cfg, lines)
	c.checkVersion(fm, lines)
	include := c.checkInclude(fm, cfg, lines)
	checkRelationKeys(c, mapping)

	entity := &duskv1alpha1.Entity{
		Ref:         conformance.CanonicalRef(kind, namespace, name),
		Kind:        kind,
		Namespace:   namespace,
		Name:        name,
		Title:       title(fm, name),
		Description: string(bytes.TrimSpace(body)),
		Attributes:  c.attributes(fm.Attributes, "attributes", lines["attributes"]),
		Provenance:  provenance(p),
	}

	relations := c.relations(fm, entity.GetRef(), p, lines["relations"])

	if err := c.err(); err != nil {
		return nil, err
	}
	return &File{
		Path: filePath, Entity: entity, Relations: relations,
		Include: include, ObservedAs: fm.ObservedAs,
	}, nil
}

func title(fm frontmatter, name string) string {
	if fm.Title != "" {
		return fm.Title
	}
	return name
}

func provenance(p Provenance) *duskv1alpha1.Provenance {
	out := &duskv1alpha1.Provenance{Source: Source, Version: p.Version}
	if !p.ObservedAt.IsZero() {
		out.ObservedAt = timestamppb.New(p.ObservedAt)
	}
	return out
}

// checkIdentity validates the three fields the ref is built from. Each is
// checked against the separators conformance.ParseRef splits on, because a ref
// that does not round-trip breaks correlation between sources.
func (c *collector) checkIdentity(fm frontmatter, cfg config, lines map[string]int) (kind, namespace, name string) {
	kind = strings.TrimSpace(fm.Kind)
	name = strings.TrimSpace(fm.Name)
	namespace = strings.TrimSpace(fm.Namespace)
	if namespace == "" {
		namespace = cfg.namespace
	}

	c.required(kind, "kind", lines)
	c.required(name, "name", lines)
	if namespace == "" {
		switch {
		case cfg.root:
			c.at(0, "namespace", "is required on a repository's root dusk.md")
		default:
			c.at(0, "namespace", "is required, because no namespace was inherited from the root dusk.md")
		}
	}

	c.rejectRune(kind, "kind", ":", lines)
	c.rejectRune(kind, "kind", "/", lines)
	c.rejectRune(namespace, "namespace", "/", lines)
	c.rejectRune(name, "name", "/", lines)
	return kind, namespace, name
}

func (c *collector) checkVersion(fm frontmatter, lines map[string]int) {
	switch fm.Dusk {
	case "":
		c.at(lines["dusk"], "dusk", fmt.Sprintf("is required and must be %q", SchemaVersion))
	case SchemaVersion:
	default:
		c.at(lines["dusk"], "dusk", fmt.Sprintf("must be %q, got %q", SchemaVersion, fm.Dusk))
	}
}

func (c *collector) checkInclude(fm frontmatter, cfg config, lines map[string]int) []string {
	line := lines["include"]
	if len(fm.Include) > 0 && !cfg.root {
		c.at(line, "include", "is only allowed in a repository's root dusk.md, because includes are one level deep")
	}

	patterns := make([]string, 0, len(fm.Include))
	for i, pattern := range fm.Include {
		field := fmt.Sprintf("include[%d]", i)
		pattern = strings.TrimSpace(pattern)
		switch {
		case pattern == "":
			c.at(line, field, "must not be empty")
		case path.IsAbs(pattern):
			c.at(line, field, "must be relative to the repository root, not an absolute path")
		case hasParentSegment(pattern):
			c.at(line, field, "must not contain a .. segment, because an include cannot reach outside the repository")
		}
		patterns = append(patterns, pattern)
	}
	return patterns
}

// hasParentSegment inspects the segments rather than the cleaned path, because
// a glob cannot be resolved until it is expanded.
func hasParentSegment(pattern string) bool {
	return slices.Contains(strings.Split(pattern, "/"), "..")
}

func (c *collector) relations(fm frontmatter, from string, p Provenance, line int) []*duskv1alpha1.Relation {
	out := make([]*duskv1alpha1.Relation, 0, len(fm.Relations))
	for i, r := range fm.Relations {
		field := fmt.Sprintf("relations[%d]", i)

		relType := strings.TrimSpace(r.Type)
		to := strings.TrimSpace(r.To)
		if relType == "" {
			c.at(line, field+".type", "is required")
		}
		switch to {
		case "":
			c.at(line, field+".to", "is required")
		default:
			if _, _, _, err := conformance.ParseRef(to); err != nil {
				c.at(line, field+".to", "must be a ref of the form kind:namespace/name: "+err.Error())
			}
		}

		out = append(out, &duskv1alpha1.Relation{
			From:       from,
			To:         to,
			Type:       relType,
			Attributes: c.attributes(r.Attributes, field+".attributes", line),
			Provenance: provenance(p),
		})
	}
	return out
}

func (c *collector) attributes(m map[string]any, field string, line int) *structpb.Struct {
	if len(m) == 0 {
		return nil
	}
	s, err := structpb.NewStruct(normalize(m))
	if err != nil {
		c.at(line, field, "must contain only values representable in JSON: "+err.Error())
		return nil
	}
	return s
}

// normalize converts values decoded from YAML into the subset structpb accepts.
// YAML resolves an unquoted date to a time.Time, which has no Struct
// equivalent, so it is recorded in RFC 3339 rather than rejected.
func normalize(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = normalizeValue(v)
	}
	return out
}

func normalizeValue(v any) any {
	switch t := v.(type) {
	case time.Time:
		return t.Format(time.RFC3339)
	case map[string]any:
		return normalize(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalizeValue(e)
		}
		return out
	default:
		return v
	}
}

var (
	byteOrderMark = []byte{0xEF, 0xBB, 0xBF}
	crlf          = []byte("\r\n")
	newline       = []byte("\n")
)

// splitFrontmatter separates the leading YAML block from the markdown body.
// Line endings are normalized because a stray carriage return would otherwise
// end up inside an entity's description.
func splitFrontmatter(data []byte) (front, body []byte, err error) {
	data = bytes.ReplaceAll(bytes.TrimPrefix(data, byteOrderMark), crlf, newline)

	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		return nil, nil, errors.New("must open with a frontmatter block: a line containing only ---")
	}

	for offset := 0; offset < len(rest); {
		line := rest[offset:]
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		if closesFrontmatter(line) {
			return rest[:offset], rest[offset+len(line):], nil
		}
		offset += len(line) + 1
	}
	return nil, nil, errors.New("frontmatter block is never closed by a line containing only ---")
}

func closesFrontmatter(line []byte) bool {
	s := strings.TrimRight(string(line), " \t")
	return s == "---" || s == "..."
}

func frontmatterMapping(front []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(front, &doc); err != nil {
		return nil, errors.New("frontmatter is not valid YAML: " + err.Error())
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, errors.New("frontmatter is empty")
	}
	if m := doc.Content[0]; m.Kind == yaml.MappingNode {
		return m, nil
	}
	return nil, errors.New("frontmatter must be a mapping of fields")
}

func keyLines(m *yaml.Node) map[string]int {
	lines := make(map[string]int, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		lines[m.Content[i].Value] = m.Content[i].Line + frontmatterLineOffset
	}
	return lines
}

func checkKeys(c *collector, m *yaml.Node, known map[string]bool, derived map[string]string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		key := m.Content[i]
		line := key.Line + frontmatterLineOffset
		switch {
		case known[key.Value]:
		case derived[key.Value] != "":
			c.at(line, key.Value, derived[key.Value])
		default:
			c.at(line, key.Value, "is not a field this format defines")
		}
	}
}

// checkRelationKeys inspects the relations sequence as nodes, because unknown
// keys inside a list element are invisible once the list has been decoded.
func checkRelationKeys(c *collector, m *yaml.Node) {
	seq := valueNode(m, "relations")
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return
	}
	for _, element := range seq.Content {
		if element.Kind == yaml.MappingNode {
			checkKeys(c, element, knownRelationFields, derivedRelationFields)
		}
	}
}

func valueNode(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func (c *collector) required(value, field string, lines map[string]int) {
	if value == "" {
		c.at(lines[field], field, "is required")
	}
}

func (c *collector) rejectRune(value, field, forbidden string, lines map[string]int) {
	if strings.Contains(value, forbidden) {
		c.at(lines[field], field, fmt.Sprintf("must not contain %q, because it separates the parts of a ref", forbidden))
	}
}

func (c *collector) decodeError(err error) {
	if typeErr, ok := errors.AsType[*yaml.TypeError](err); ok {
		for _, message := range typeErr.Errors {
			c.at(0, "", message)
		}
		return
	}
	c.at(0, "", "frontmatter could not be read: "+err.Error())
}
