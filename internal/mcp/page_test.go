package mcp_test

import (
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// pageSession serves the page tool over a writer holding one page in memory.
func pageSession(t *testing.T, home string) (*sdk.ClientSession, *recordingWriter) {
	t.Helper()

	idx := newIndex(t)
	seed(t, idx)

	tokens := &proof.Store{}
	writer := &recordingWriter{tokens: tokens, notesGo: configRepo, home: []byte(home)}

	server := mcp.New(mcp.Options{Catalog: idx, Tokens: tokens, Writer: writer, Version: "test"})
	return serve(t, server), writer
}

const declared = `---
title: Home
blocks:
  - type: kinds
---
`

// Reading a page an operator has not declared still has to be useful: the
// answer is the default, written out, so an agent can edit it rather than
// inventing one from nothing.
func TestPageReadsTheDefaultWhenNoneIsDeclared(t *testing.T) {
	session, _ := pageSession(t, "")
	answer := call(t, session, "page", map[string]any{})

	if !strings.Contains(answer, "No page is declared") {
		t.Errorf("answer = %q, want it to say none is declared", answer)
	}
	if !strings.Contains(answer, "type: kinds") {
		t.Errorf("answer = %q, want the default written out", answer)
	}
}

// A write replaces the whole page, so the read that authorizes it has to have
// seen the whole page. That is the read-before-write contract (ADR-0009).
func TestPageRefusesAWriteWithoutAProofToken(t *testing.T) {
	session, writer := pageSession(t, declared)

	answer := call(t, session, "page", map[string]any{"body": declared})
	if !strings.Contains(strings.ToLower(answer), "not written") {
		t.Errorf("answer = %q, want the write refused", answer)
	}
	if writer.wrote != nil {
		t.Error("a page was written with no proof token")
	}
}

// A repository with no page has to be able to get one, and the only read that
// can witness there is none is `page` itself: a search cannot name a file.
func TestPageCanBeDeclaredForTheFirstTime(t *testing.T) {
	session, writer := pageSession(t, "")

	token := tokenFrom(t, call(t, session, "page", map[string]any{}))
	answer := call(t, session, "page", map[string]any{"body": declared, "proof": token})

	if !strings.Contains(answer, "Rewrote") {
		t.Fatalf("the first page could not be declared:\n%s", answer)
	}
	if string(writer.wrote) != declared {
		t.Errorf("wrote %q, want exactly what was sent", writer.wrote)
	}
}

// Reading issues the token, and that token authorizes the rewrite.
func TestPageWritesAfterReading(t *testing.T) {
	session, writer := pageSession(t, declared)

	read := call(t, session, "page", map[string]any{})
	token := tokenFrom(t, read)
	if token == "" {
		t.Fatalf("reading the page issued no proof token: %q", read)
	}

	replacement := declared + "\nSomething about this estate.\n"
	answer := call(t, session, "page", map[string]any{"body": replacement, "proof": token})

	if !strings.Contains(answer, "Rewrote") {
		t.Fatalf("answer = %q, want the page rewritten", answer)
	}
	if string(writer.wrote) != replacement {
		t.Errorf("wrote %q, want exactly what was sent", writer.wrote)
	}
}
