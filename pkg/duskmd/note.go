package duskmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/NerdsWhoFish/dusk-plugin-sdk/conformance"
	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
)

// WellKnownNoteKinds seed the vocabulary. Kind drives ranking and rendering
// rather than only labelling, so a gotcha surfaces where a todo does not.
var WellKnownNoteKinds = []string{"gotcha", "runbook", "howto", "decision", "incident", "todo", "idea"}

// Statuses a note that is work can be in. Empty means open, so a note written
// before there was a status is not silently closed.
const (
	StatusOpen    = "open"
	StatusDone    = "done"
	StatusDropped = "dropped"
)

// Working are the kinds a status means something for. A gotcha is never done;
// an idea and a todo are the things somebody finishes or abandons.
var Working = []string{"idea", "todo"}

// noteFrontmatter is the authorable shape of a note file.
type noteFrontmatter struct {
	Dusk   string   `yaml:"dusk"`
	Note   string   `yaml:"note"`
	Refs   []string `yaml:"refs,omitempty"`
	Pinned bool     `yaml:"pinned,omitempty"`
	Status string   `yaml:"status,omitempty"`
}

var knownNoteFields = map[string]bool{
	"dusk": true, "note": true, "refs": true, "pinned": true, "status": true,
}

var derivedNoteFields = map[string]string{
	"id":           "id is the file's path, so it cannot be declared here",
	"body":         "body is the prose below the frontmatter, not a field",
	"content_hash": "content_hash is computed from the body",
}

// IsNote reports whether a catalog file declares a note rather than an entity.
// The discriminator is required rather than inferred: a file whose type has to
// be guessed is one that will eventually be guessed wrong.
func IsNote(data []byte) bool {
	front, _, err := splitFrontmatter(data)
	if err != nil {
		return false
	}
	mapping, err := frontmatterMapping(front)
	if err != nil {
		return false
	}
	return valueNode(mapping, "note") != nil
}

// ParseNote reads a note file.
func ParseNote(filePath string, data []byte, p Provenance) (*duskv1alpha1.Note, error) {
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
	checkKeys(c, mapping, knownNoteFields, derivedNoteFields)

	var fm noteFrontmatter
	if err := mapping.Decode(&fm); err != nil {
		c.decodeError(err)
		return nil, c.err()
	}

	c.checkNoteVersion(fm, lines)
	kind := strings.TrimSpace(fm.Note)
	if kind == "" {
		c.at(lines["note"], "note", "is required and is what makes this file a note")
	}

	refs := c.checkNoteRefs(fm, lines)
	status := c.checkNoteStatus(fm, lines)
	prose := strings.TrimSpace(string(body))
	if prose == "" {
		c.at(0, "", "a note is its prose, and this file has none below the frontmatter")
	}

	if err := c.err(); err != nil {
		return nil, err
	}
	return &duskv1alpha1.Note{
		Id:          filePath,
		Kind:        kind,
		Refs:        refs,
		Body:        prose,
		Pinned:      fm.Pinned,
		Status:      status,
		ContentHash: ContentHash(prose),
		Provenance:  provenance(p),
	}, nil
}

// checkNoteStatus validates how a note that is work was closed. The vocabulary
// is closed because it decides whether the note is still asking for something.
func (c *collector) checkNoteStatus(fm noteFrontmatter, lines map[string]int) string {
	status := strings.ToLower(strings.TrimSpace(fm.Status))
	switch status {
	case "", StatusOpen, StatusDone, StatusDropped:
		return status
	}

	c.at(lines["status"], "status", "must be "+strings.Join(
		[]string{StatusOpen, StatusDone, StatusDropped}, ", ")+", or left out")
	return ""
}

// FormatNote writes a note back to the file it came from, round-tripping what
// ParseNote read. The body is authored markdown and is written through
// untouched, because rendering it would quietly rewrite somebody's prose.
func FormatNote(note *duskv1alpha1.Note) ([]byte, error) {
	if note == nil {
		return nil, errNothingToFormat
	}
	if strings.TrimSpace(note.GetKind()) == "" {
		return nil, errNoteNeedsKind
	}

	encoded, err := marshalFrontmatter(noteFrontmatter{
		Dusk:   SchemaVersion,
		Note:   note.GetKind(),
		Refs:   note.GetRefs(),
		Pinned: note.GetPinned(),
		Status: note.GetStatus(),
	})
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(encoded)
	out.WriteString("---\n")

	if prose := strings.TrimSpace(note.GetBody()); prose != "" {
		out.WriteString("\n")
		out.WriteString(prose)
		out.WriteString("\n")
	}
	return out.Bytes(), nil
}

var (
	errNothingToFormat = errors.New("duskmd: nothing to format")
	errNoteNeedsKind   = errors.New("duskmd: a note needs a kind, such as gotcha or runbook")
)

// ContentHash identifies a body, so an identical note written twice resolves to
// the one that exists rather than a duplicate.
func ContentHash(body string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(body)))
	return hex.EncodeToString(sum[:])
}

func (c *collector) checkNoteVersion(fm noteFrontmatter, lines map[string]int) {
	if fm.Dusk != SchemaVersion {
		c.at(lines["dusk"], "dusk", `is required and must be "`+SchemaVersion+`"`)
	}
}

// checkNoteRefs validates what a note attaches to, without checking the refs
// resolve: one may be about an entity another repository declares. Naming
// nothing is allowed, because an idea often is not about anything yet.
func (c *collector) checkNoteRefs(fm noteFrontmatter, lines map[string]int) []string {
	refs := make([]string, 0, len(fm.Refs))
	for i, ref := range fm.Refs {
		ref = strings.TrimSpace(ref)
		field := "refs[" + strconv.Itoa(i) + "]"
		if ref == "" {
			c.at(lines["refs"], field, "must not be empty")
			continue
		}
		if _, _, _, err := conformance.ParseRef(ref); err != nil {
			c.at(lines["refs"], field, "must be a ref of the form kind:namespace/name: "+err.Error())
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}
