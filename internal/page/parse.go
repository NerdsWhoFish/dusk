package page

import (
	"bytes"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/NerdsWhoFish/dusk/pkg/catalogfs"
)

// byteOrderMark is UTF-8's, written as bytes because Go source cannot hold it
// as a literal.
var byteOrderMark = []byte{0xEF, 0xBB, 0xBF}

// Path is where the config repository declares its portal page. `catalogfs`
// owns the constant, because that is what keeps a reconcile from reading it as
// catalog content (ADR-0048).
const Path = catalogfs.HomePath

// Parse reads a declared page. The prose below the frontmatter is the page's
// own introduction, and is returned separately so a renderer can place it.
func Parse(data []byte) (Page, string, error) {
	front, body, ok := split(data)
	if !ok {
		return Page{}, "", fmt.Errorf("page: %s must open with a YAML frontmatter block delimited by ---", Path)
	}

	var p Page
	decoder := yaml.NewDecoder(strings.NewReader(front))
	decoder.KnownFields(true)
	if err := decoder.Decode(&p); err != nil {
		return Page{}, "", fmt.Errorf("page: %s: %w", Path, err)
	}

	if len(p.Blocks) == 0 {
		return Page{}, "", fmt.Errorf("page: %s declares no blocks, so it would render as an empty page. Remove the file to get the default one", Path)
	}
	for i, block := range p.Blocks {
		if !valid(block.Type) {
			return Page{}, "", fmt.Errorf("page: %s block %d has type %q, expected one of %s", Path, i+1, block.Type, joinTypes())
		}
	}

	return p, strings.TrimSpace(body), nil
}

func valid(t Type) bool {
	for _, candidate := range Types {
		if candidate == t {
			return true
		}
	}
	return false
}

// split separates the frontmatter from the prose.
func split(data []byte) (front, body string, ok bool) {
	// An editor that writes a byte order mark would otherwise make the file
	// look like it has no frontmatter at all.
	text := strings.TrimLeft(string(bytes.TrimPrefix(data, byteOrderMark)), " \t\r\n")
	rest, found := strings.CutPrefix(text, "---")
	if !found {
		return "", "", false
	}

	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", false
	}

	front = rest[:end]
	body = strings.TrimPrefix(rest[end+len("\n---"):], "\n")
	return front, body, true
}
