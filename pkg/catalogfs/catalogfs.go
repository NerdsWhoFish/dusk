// Package catalogfs holds the file semantics of a catalog repository: what
// counts as a catalog file, how include patterns match, and the tree a
// reconcile reads from.
//
// It exists because these rules were previously spread across three readers
// that each implemented them slightly differently. A pattern that resolves one
// way locally and another way on the server makes a local check worse than no
// check, so there is one implementation and every reader shares it.
package catalogfs

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
)

// IsCatalogFile reports whether a path is worth carrying: markdown, and not
// git's own directory, whose packfiles can contain anything including a name
// ending in .md and are never catalog content.
func IsCatalogFile(name string) bool {
	if name == gitDir || strings.HasPrefix(name, gitDir+"/") {
		return false
	}
	return strings.HasSuffix(strings.ToLower(name), ".md")
}

const gitDir = ".git"

// IsGitDir reports whether a directory is git's, so a walk can skip it whole
// rather than testing every path inside it.
func IsGitDir(name string) bool { return name == gitDir }

// DuskDir is read whenever it exists, with no include needed (ADR-0031).
const DuskDir = ".dusk"

// The paths inside DuskDir that are Dusk's own declared configuration rather
// than catalog content. They are markdown in the directory a reconcile always
// reads, so without this they parse as an entity and fail (ADR-0048).
const (
	// HomePath is the portal page (ADR-0013).
	HomePath = DuskDir + "/home.md"

	// KindsPath is the minted kind vocabulary (ADR-0048).
	KindsPath = DuskDir + "/kinds.md"
)

// reserved is the whole list, kept here rather than assembled from the packages
// that own each file, because two owners of one rule is how they drift apart.
var reserved = map[string]bool{HomePath: true, KindsPath: true}

// IsReserved reports whether a path is Dusk's own configuration. A reconcile
// skips these: they are read by whatever declared them, not as catalog content.
func IsReserved(name string) bool { return reserved[path.Clean(name)] }

// Reserved lists the configuration paths, in lexical order, so an error can say
// which names are taken rather than only that this one is.
func Reserved() []string { return slices.Sorted(maps.Keys(reserved)) }

// Tree is a repository's catalog files at one commit, held in memory.
type Tree struct {
	commit string
	files  map[string][]byte
}

// New returns a tree holding files, which it takes ownership of.
func New(commit string, files map[string][]byte) *Tree {
	return &Tree{commit: commit, files: files}
}

// Commit is the revision this tree was read at.
func (t *Tree) Commit() string { return t.commit }

// Paths returns every file held, in lexical order.
func (t *Tree) Paths() []string {
	return slices.Sorted(maps.Keys(t.files))
}

// Read returns one file's contents, reporting a miss the way a filesystem does
// so a caller can tell "not declared" from "could not look".
func (t *Tree) Read(filePath string) ([]byte, error) {
	data, ok := t.files[filePath]
	if !ok {
		return nil, fmt.Errorf("catalogfs: %q at %s: %w", filePath, Short(t.commit), fs.ErrNotExist)
	}
	return data, nil
}

// Has reports whether a file is present without reading it.
func (t *Tree) Has(filePath string) bool {
	_, ok := t.files[filePath]
	return ok
}

// Glob returns the paths matching pattern, in lexical order.
func (t *Tree) Glob(pattern string) ([]string, error) {
	var matches []string
	for _, candidate := range t.Paths() {
		ok, err := Match(pattern, candidate)
		if err != nil {
			return nil, fmt.Errorf("catalogfs: include pattern %q is malformed: %w", pattern, err)
		}
		if ok {
			matches = append(matches, candidate)
		}
	}
	return matches, nil
}

// Match reports whether candidate matches pattern, supporting `**` for any
// number of directories, which path.Match does not and a documentation tree
// needs.
func Match(pattern, candidate string) (bool, error) {
	before, after, recursive := splitRecursive(pattern)
	if !recursive {
		return path.Match(pattern, candidate)
	}

	// `a/**/b.md` also matches `a/b.md`, because requiring an intermediate
	// directory surprises everybody who writes it.
	for _, collapsed := range []string{before + "/" + after, before + "/*/" + after} {
		if ok, err := path.Match(collapsed, candidate); err != nil || ok {
			return ok, err
		}
	}
	if !hasPrefixDir(candidate, before) {
		return false, nil
	}
	return path.Match(after, path.Base(candidate))
}

func splitRecursive(pattern string) (before, after string, ok bool) {
	for i := range len(pattern) - 2 {
		if pattern[i:i+2] == "**" {
			return path.Clean(pattern[:max(i-1, 0)]), pattern[min(i+3, len(pattern)):], true
		}
	}
	return "", "", false
}

func hasPrefixDir(candidate, dir string) bool {
	if dir == "" || dir == "." {
		return true
	}
	return len(candidate) > len(dir) && candidate[:len(dir)] == dir && candidate[len(dir)] == '/'
}

// Short abbreviates a commit for a log line or an error.
func Short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
