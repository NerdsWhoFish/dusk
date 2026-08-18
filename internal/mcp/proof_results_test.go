package mcp_test

import (
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A structured client must receive the same proof as a client reading the
// Markdown. Otherwise the advertised read-before-write workflow dead-ends.
func TestWritableReadsCarryProofInStructuredData(t *testing.T) {
	session, _, _ := noting(t, configRepo)

	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{name: "search results", tool: "search", args: map[string]any{"query": "jellyfin"}},
		{name: "search absence", tool: "search", args: map[string]any{"query": "not-in-the-catalog"}},
		{name: "entity", tool: "get", args: map[string]any{"ref": "service:home/jellyfin"}},
		{name: "neighbors", tool: "neighbors", args: map[string]any{"ref": "service:home/jellyfin"}},
		{name: "note", tool: "note", args: map[string]any{"id": transcodingNote}},
		{name: "kinds", tool: "kinds", args: map[string]any{}},
		{name: "page", tool: "page", args: map[string]any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: tt.tool, Arguments: tt.args})
			if err != nil {
				t.Fatalf("CallTool(%s): %v", tt.tool, err)
			}

			var body strings.Builder
			for _, content := range result.Content {
				if text, ok := content.(*sdk.TextContent); ok {
					body.WriteString(text.Text)
				}
			}
			markdownProof := tokenFrom(t, body.String())

			structured, ok := result.StructuredContent.(map[string]any)
			if !ok {
				t.Fatalf("structured result = %#v, want an object", result.StructuredContent)
			}
			data, ok := structured["data"].(map[string]any)
			if !ok {
				t.Fatalf("structured data = %#v, want an object", structured["data"])
			}
			if got, _ := data["proof"].(string); got != markdownProof {
				t.Fatalf("structured proof = %q, Markdown proof = %q", got, markdownProof)
			}
		})
	}
}
