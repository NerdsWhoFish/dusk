package server

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// A push payload lists what it touched, which is what lets a delivery answer
// for zero requests. Reading that list wrong in the trusting direction makes
// the catalog silently stale, so every untrustworthy shape must yield nil.
func TestTouchedFilesAreOnlyTrustedWhenComplete(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    []string
	}{
		{
			name:    "an ordinary push lists its files",
			payload: `{"commits":[{"added":["dusk.md"],"modified":["README.md"]}]}`,
			want:    []string{"dusk.md", "README.md"},
		},
		{
			name: "a file touched by two commits appears once",
			payload: `{"commits":[
				{"modified":["dusk.md"]},
				{"modified":["dusk.md"],"added":["notes/a.md"]}
			]}`,
			want: []string{"dusk.md", "notes/a.md"},
		},
		{
			name:    "a removal counts as a change",
			payload: `{"commits":[{"removed":["services/old.md"]}]}`,
			want:    []string{"services/old.md"},
		},
		// GitHub caps the list at twenty and does not say it truncated.
		{
			name:    "a truncated commit list is not trusted",
			payload: `{"commits":[` + repeatCommits(20) + `]}`,
			want:    nil,
		},
		{
			name:    "a created branch is not trusted",
			payload: `{"created":true,"commits":[{"added":["dusk.md"]}]}`,
			want:    nil,
		},
		{
			name:    "a force push is not trusted",
			payload: `{"forced":true,"commits":[{"modified":["dusk.md"]}]}`,
			want:    nil,
		},
		{
			name:    "a deleted branch is not trusted",
			payload: `{"deleted":true,"commits":[]}`,
			want:    nil,
		},
		{
			name:    "no commits at all is not trusted",
			payload: `{"commits":[]}`,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload deliveryPayload
			if err := json.Unmarshal([]byte(tt.payload), &payload); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			got := payload.touched()
			if !slices.Equal(got, tt.want) {
				t.Errorf("touched() = %v, want %v", got, tt.want)
			}
			if tt.want == nil && got != nil {
				t.Error("an untrusted payload returned a list, which would let a caller skip the read")
			}
		})
	}
}

func repeatCommits(n int) string {
	commits := make([]string, n)
	for i := range commits {
		commits[i] = `{"modified":["main.go"]}`
	}
	return strings.Join(commits, ",")
}
