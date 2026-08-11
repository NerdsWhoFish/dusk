package duskmd

import (
	"fmt"
	"strings"
)

// Error is one schema violation, located well enough for the author to fix it
// without reading the parser: the file, the field, and the expectation.
type Error struct {
	// Path is the catalog file the violation was found in.
	Path string

	// Field is the frontmatter field at fault, empty when the whole file is.
	Field string

	// Line is the 1-based line within the file, or 0 when it is not known.
	Line int

	// Message states what was expected.
	Message string
}

func (e Error) Error() string {
	location := e.Path
	if e.Line > 0 {
		location = fmt.Sprintf("%s:%d", e.Path, e.Line)
	}
	if e.Field == "" {
		return fmt.Sprintf("duskmd: %s: %s", location, e.Message)
	}
	return fmt.Sprintf("duskmd: %s: field %q %s", location, e.Field, e.Message)
}

// Errors is every violation found in one file, reported together so that an
// author gets the whole list rather than one error per attempt.
type Errors []Error

func (e Errors) Error() string {
	lines := make([]string, len(e))
	for i, violation := range e {
		lines[i] = violation.Error()
	}
	return strings.Join(lines, "\n")
}

// collector accumulates violations so that one parse reports every problem.
type collector struct {
	path string
	errs Errors
}

func (c *collector) at(line int, field, message string) {
	c.errs = append(c.errs, Error{Path: c.path, Field: field, Line: line, Message: message})
}

func (c *collector) err() error {
	if len(c.errs) == 0 {
		return nil
	}
	return c.errs
}
