package mcp_test

import (
	"io/fs"

	"context"
	"github.com/NerdsWhoFish/dusk/internal/page"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// recordingWriter accepts anything, so these tests are about the surface rather
// than the write path, which has its own.
type recordingWriter struct {
	tokens *proof.Store
	got    []write.Declaration
	notes  []write.Note
	home   []byte
	wrote  []byte
	kinds  []byte
	minted []vocab.Kind

	// noteBodies is what each note says today, which is what a replacement is
	// authorized against, the same as the real writer reads from the file.
	noteBodies map[string]string

	// notesGo is where notes land, and empty means the tool is not offered.
	notesGo string
}

func (w *recordingWriter) NoteDestination() string { return w.notesGo }

func (w *recordingWriter) Record(_ context.Context, token string, n write.Note) (*write.Result, error) {
	// Replacing a note is a write over something the agent should have read.
	// Creating one is not, so only the first needs a token.
	if n.Id != "" {
		if err := w.tokens.AuthorizeUpdate(token, n.Id, duskmd.ContentHash(w.noteBodies[n.Id])); err != nil {
			return nil, err
		}
	}
	w.notes = append(w.notes, n)

	path := n.Id
	if path == "" {
		path = ".dusk/gotcha-abcd1234.md"
	}
	return &write.Result{
		Ref: path, Repository: w.notesGo, Path: path, Created: n.Id == "",
		Commit: "c0ffee", URL: "https://github.com/example/config/commit/c0ffee",
	}, nil
}

func (w *recordingWriter) Declare(_ context.Context, token string, d write.Declaration) (*write.Result, error) {
	// The surface has to pass the token through, so check that it resolves.
	if w.tokens.Lookup(token) == nil {
		return nil, &proof.Rejection{
			Code: proof.CodeRequired, Ref: d.Ref,
			Detail: "no valid proof token was presented", Fix: `get("` + d.Ref + `")`,
		}
	}
	w.got = append(w.got, d)
	return &write.Result{
		Ref: d.Ref, Repository: "example/homelab", Path: "services/x/dusk.md",
		Commit: "c0ffee", URL: "https://github.com/example/homelab/commit/c0ffee",
	}, nil
}

var tokenPattern = regexp.MustCompile("Proof token `([^`]+)`")

func tokenFrom(t *testing.T, body string) string {
	t.Helper()
	match := tokenPattern.FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("no proof token in the answer:\n%s", body)
	}
	return match[1]
}

func writableSession(t *testing.T) (*sdk.ClientSession, *recordingWriter) {
	t.Helper()

	idx := newIndex(t)
	seed(t, idx)

	tokens := &proof.Store{}
	writer := &recordingWriter{tokens: tokens}

	server := mcp.New(mcp.Options{Catalog: idx, Tokens: tokens, Writer: writer, Version: "test"})
	return serve(t, server), writer
}

// Read-before-write is an unusual contract, so the token has to arrive without
// being asked for or no agent discovers it.
func TestEveryReadIssuesAProofToken(t *testing.T) {
	session, _ := writableSession(t)

	for _, read := range []struct {
		tool string
		args map[string]any
	}{
		{"search", map[string]any{"query": "jellyfin"}},
		{"get", map[string]any{"ref": "service:home/jellyfin"}},
		{"neighbors", map[string]any{"ref": "host:home/nas"}},
	} {
		t.Run(read.tool, func(t *testing.T) {
			body := call(t, session, read.tool, read.args)
			if !strings.Contains(body, "Proof token") {
				t.Errorf("%s issued no token:\n%s", read.tool, body)
			}
			if !strings.Contains(body, "declare") {
				t.Errorf("%s does not say what the token is for:\n%s", read.tool, body)
			}
		})
	}
}

// A search that found nothing is what authorizes creating, so it issues a token
// too. The absence is the evidence.
func TestAnEmptySearchStillIssuesAToken(t *testing.T) {
	session, _ := writableSession(t)

	body := call(t, session, "search", map[string]any{"query": "nothinglikethis"})
	if !strings.Contains(body, "Proof token") {
		t.Errorf("an empty search issued no token, so nothing could ever be created:\n%s", body)
	}
}

func TestDeclareCarriesTheTokenThrough(t *testing.T) {
	session, writer := writableSession(t)

	token := tokenFrom(t, call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"}))
	body := call(t, session, "declare", map[string]any{
		"ref":   "service:home/jellyfin",
		"proof": token,
		"title": "Jellyfin Media Server",
	})

	if len(writer.got) != 1 {
		t.Fatalf("the writer saw %d declarations, want 1", len(writer.got))
	}
	if got := writer.got[0].Title; got != "Jellyfin Media Server" {
		t.Errorf("title = %q, want it passed through", got)
	}
	// An agent must be able to hand a human a link rather than assert success.
	if !strings.Contains(body, "commit/c0ffee") {
		t.Errorf("answer carries no link to the commit:\n%s", body)
	}
}

func TestDeclareWithoutATokenIsRefusedAndExplained(t *testing.T) {
	session, writer := writableSession(t)

	body := call(t, session, "declare", map[string]any{
		"ref": "service:home/jellyfin", "proof": "made-up", "title": "x",
	})

	if len(writer.got) != 0 {
		t.Errorf("a write happened without a token: %+v", writer.got)
	}
	// A refusal is an answer, not a transport error, so the agent can read it
	// and do the thing it names.
	for _, want := range []string{"was not made", proof.CodeRequired, "call get("} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal missing %q:\n%s", want, body)
		}
	}
}

// A deployment with no writer is read-only, which ADR-0005 treats as supported
// rather than degraded. The tool must not be offered at all.
func TestDeclareIsAbsentWhenReadOnly(t *testing.T) {
	session := serve(t, mcp.New(mcp.Options{Catalog: newIndex(t), Version: "test"}))

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "declare" {
			t.Fatal("declare is offered by a read-only deployment")
		}
	}

	if _, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "declare", Arguments: map[string]any{"ref": "x", "proof": "y"},
	}); err == nil {
		t.Error("calling declare succeeded on a read-only deployment")
	}
}

// Reads must not offer a token a read-only deployment cannot honour, or an
// agent will try to write and be told nothing useful.
func TestReadOnlyReadsIssueNoToken(t *testing.T) {
	idx := newIndex(t)
	seed(t, idx)
	session := serve(t, mcp.New(mcp.Options{Catalog: idx, Version: "test"}))

	body := call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"})
	if strings.Contains(body, "Proof token") {
		t.Errorf("a read-only deployment offered a write token:\n%s", body)
	}
}

// Home and SetHome back the page tool. The page is a file like any other, so
// the fake keeps one in memory.
func (w *recordingWriter) Home(context.Context) ([]byte, error) {
	if len(w.home) == 0 {
		return nil, fs.ErrNotExist
	}
	return w.home, nil
}

func (w *recordingWriter) SetHome(_ context.Context, token string, body []byte) (*write.Result, error) {
	if err := w.tokens.AuthorizeUpdate(token, page.Path, duskmd.ContentHash(string(w.home))); err != nil {
		return nil, err
	}
	w.wrote = body
	w.home = body
	return &write.Result{Path: page.Path, Repository: "example/config", URL: "https://example.com/commit"}, nil
}
