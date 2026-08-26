package write_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/contextconfig"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

const contextProfile = `---
dusk: context/v1
budget: 8000
inventory: full
---
Read every pinned note.
`

func TestSetContextReplacesTheProfileItRead(t *testing.T) {
	files := map[string]string{contextconfig.Path: contextProfile}
	writer, target, tokens := newWriter(t, nil, files)
	writer.ConfigRepository = repo
	token := tokens.Issue(proof.FromContext, map[string]string{
		contextconfig.Path: duskmd.ContentHash(contextProfile),
	})
	replacement := strings.Replace(contextProfile, "budget: 8000", "budget: 12000", 1)

	result, err := writer.SetContext(t.Context(), token.ID, []byte(replacement))
	if err != nil {
		t.Fatalf("SetContext: %v", err)
	}
	if result.Created || result.Path != contextconfig.Path {
		t.Fatalf("result = %+v", result)
	}
	if len(target.commits) != 1 || string(target.commits[0].Content) != replacement {
		t.Fatalf("commits = %+v", target.commits)
	}
	if target.commits[0].ReplacingSHA == "" {
		t.Error("the update did not present the blob it read")
	}
}

func TestSetContextCreatesOnlyAfterItsOwnRead(t *testing.T) {
	writer, target, tokens := newWriter(t, nil, map[string]string{})
	writer.ConfigRepository = repo
	token := tokens.Issue(proof.FromContext, nil)

	result, err := writer.SetContext(t.Context(), token.ID, []byte(contextProfile))
	if err != nil {
		t.Fatalf("SetContext: %v", err)
	}
	if !result.Created || len(target.commits) != 1 {
		t.Fatalf("result = %+v, commits = %+v", result, target.commits)
	}
}

func TestSetContextRejectsMalformedOrUnprovenProfiles(t *testing.T) {
	for _, tt := range []struct {
		name  string
		body  string
		token func(*proof.Store) string
	}{
		{name: "malformed", body: "not frontmatter", token: func(tokens *proof.Store) string {
			return tokens.Issue(proof.FromContext, nil).ID
		}},
		{name: "wrong read", body: contextProfile, token: func(tokens *proof.Store) string {
			return tokens.Issue(proof.FromPage, nil).ID
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writer, target, tokens := newWriter(t, nil, map[string]string{})
			writer.ConfigRepository = repo
			_, err := writer.SetContext(t.Context(), tt.token(tokens), []byte(tt.body))
			if err == nil {
				t.Fatal("SetContext succeeded")
			}
			if len(target.commits) != 0 {
				t.Fatalf("committed an invalid write: %+v", target.commits)
			}
			if tt.name == "wrong read" {
				var rejection *proof.Rejection
				if !errors.As(err, &rejection) {
					t.Fatalf("error = %v, want a proof rejection", err)
				}
			}
		})
	}
}
