package mcp_test

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/internal/store"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// proposedDiff stands in for what the write path produces. What matters here is
// that the surface hands it on whole, not how it was computed.
const proposedDiff = "--- a/services/x/dusk.md\n" +
	"+++ b/services/x/dusk.md\n" +
	"@@ -1,4 +1,4 @@\n" +
	"-title: Jellyfin\n" +
	"+title: Jellyfin Media Server\n"

// proposingWriter is a recordingWriter that may not commit, which is what read
// mode looks like from the surface.
type proposingWriter struct {
	*recordingWriter
	mode store.AccessMode
}

func (w *proposingWriter) Declare(ctx context.Context, token string, d write.Declaration) (*write.Result, error) {
	result, err := w.recordingWriter.Declare(ctx, token, d)
	if err != nil {
		return nil, err
	}
	result.Commit, result.URL = "", ""
	result.Proposed, result.Mode, result.Diff = true, w.mode, proposedDiff
	return result, nil
}

func proposingSession(t *testing.T, mode store.AccessMode) *sdk.ClientSession {
	t.Helper()

	idx := newIndex(t)
	seed(t, idx)

	tokens := &proof.Store{}
	writer := &proposingWriter{recordingWriter: &recordingWriter{tokens: tokens}, mode: mode}

	return serve(t, mcp.New(mcp.Options{Catalog: idx, Tokens: tokens, Writer: writer, Version: "test"}))
}

// ADR-0010's fourth rule: in a mode that cannot commit, a write returns the
// proposed diff instead of failing. An agent has to come away with something a
// person can apply, not a refusal it can do nothing with.
func TestADR0010_AWriteThatCannotLandReturnsTheDiff(t *testing.T) {
	session := proposingSession(t, store.ModeRead)

	token := tokenFrom(t, call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"}))
	body := call(t, session, "declare", map[string]any{
		"ref": "service:home/jellyfin", "proof": token, "title": "Jellyfin Media Server",
	})

	t.Run("it does not read as a refusal", func(t *testing.T) {
		for _, unwanted := range []string{"was not made", "E_PROOF"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("the answer reads as a failure (%q):\n%s", unwanted, body)
			}
		}
	})

	t.Run("it carries the diff, the repository and the path", func(t *testing.T) {
		for _, want := range []string{
			"```diff",
			"+title: Jellyfin Media Server",
			"services/x/dusk.md",
			"example/homelab",
			"git apply",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("the answer is missing %q:\n%s", want, body)
			}
		}
	})

	// Claiming a commit that does not exist is the one thing worse than not
	// committing, because the agent will tell somebody it is done.
	t.Run("it claims no commit", func(t *testing.T) {
		for _, unwanted := range []string{"Commit:", "commit/c0ffee", "Updated `service:home/jellyfin`"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("the answer claims a commit (%q):\n%s", unwanted, body)
			}
		}
	})
}

// The two modes that cannot commit are different problems, so the answer says
// which one it is rather than "no".
func TestAProposalSaysWhichModeRefusedIt(t *testing.T) {
	tests := []struct {
		mode store.AccessMode
		want string
	}{
		{store.ModeRead, "read mode"},
		{store.ModeProposal, "proposal mode"},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			session := proposingSession(t, tt.mode)

			token := tokenFrom(t, call(t, session, "get", map[string]any{"ref": "service:home/jellyfin"}))
			body := call(t, session, "declare", map[string]any{
				"ref": "service:home/jellyfin", "proof": token, "title": "x",
			})

			if !strings.Contains(body, tt.want) {
				t.Errorf("the answer does not say it is in %s:\n%s", tt.want, body)
			}
		})
	}
}
