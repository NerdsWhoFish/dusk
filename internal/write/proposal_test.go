package write_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/page"
	"github.com/NerdsWhoFish/dusk/internal/store"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

type fakeAccess struct {
	mode store.AccessMode
	err  error
}

func (f fakeAccess) Mode() (store.AccessMode, error) { return f.mode, f.err }

const homePage = `---
title: Home
blocks:
  - type: kinds
---
`

// writeCall is one path into the write package, so every one of them can be
// held to the same rule rather than only the one that was thought about.
type writeCall struct {
	name  string
	files map[string]string
	at    string
	call  func(ctx context.Context, w *write.Writer, tokens *proof.Store) (*write.Result, error)

	// wantPath is the file the change is to, and wantDiff is what has to be in
	// it for a person to be able to apply it.
	wantPath string
	wantDiff []string
}

func writeCalls() []writeCall {
	return []writeCall{
		{
			name:  "updating an entity",
			files: map[string]string{RootPath: rootFile, "services/jellyfin/dusk.md": jellyfinFile},
			at:    "services/jellyfin/dusk.md",
			call: func(ctx context.Context, w *write.Writer, tokens *proof.Store) (*write.Result, error) {
				token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
				return w.Declare(ctx, token.ID, write.Declaration{
					Ref: jellyfin, Title: "Jellyfin Media Server",
				})
			},
			wantPath: "services/jellyfin/dusk.md",
			wantDiff: []string{
				"--- a/services/jellyfin/dusk.md",
				"+++ b/services/jellyfin/dusk.md",
				"-title: Jellyfin",
				"+title: Jellyfin Media Server",
			},
		},
		{
			name:  "creating an entity",
			files: map[string]string{RootPath: rootFile},
			call: func(ctx context.Context, w *write.Writer, tokens *proof.Store) (*write.Result, error) {
				token := tokens.Issue(proof.FromSearch, nil)
				return w.Declare(ctx, token.ID, write.Declaration{
					Ref: "service:home/navidrome", Repository: repo, Description: "Music streaming.",
				})
			},
			wantPath: "services/navidrome/dusk.md",
			wantDiff: []string{
				"--- /dev/null",
				"+++ b/services/navidrome/dusk.md",
				"+name: navidrome",
				"+Music streaming.",
			},
		},
		{
			name:  "writing a note",
			files: map[string]string{RootPath: rootFile},
			call: func(ctx context.Context, w *write.Writer, _ *proof.Store) (*write.Result, error) {
				w.ConfigRepository = repo
				return w.Record(ctx, "", write.Note{
					Kind: "gotcha", Body: "Transcoding is off on purpose.",
				})
			},
			wantDiff: []string{
				"--- /dev/null",
				"+++ b/" + write.NoteDir + "/gotcha-",
				"+Transcoding is off on purpose.",
			},
		},
		{
			name:  "rewriting the homepage",
			files: map[string]string{RootPath: rootFile, page.Path: homePage},
			call: func(ctx context.Context, w *write.Writer, tokens *proof.Store) (*write.Result, error) {
				w.ConfigRepository = repo
				token := tokens.Issue(proof.FromPage, map[string]string{page.Path: duskmd.ContentHash(homePage)})
				return w.SetHome(ctx, token.ID, []byte(homePage+"\nSomething about this estate.\n"))
			},
			wantPath: page.Path,
			wantDiff: []string{"+Something about this estate."},
		},
	}
}

// ADR-0048: a mode Dusk was not granted a commit in answers with the change it
// would have made, which is what makes read-only first class rather than an
// error an agent can do nothing with.
func TestADR0048_AModeThatCannotCommitProposes(t *testing.T) {
	for _, mode := range []store.AccessMode{store.ModeRead, store.ModeProposal} {
		for _, tt := range writeCalls() {
			t.Run(string(mode)+" "+tt.name, func(t *testing.T) {
				writer, target, tokens := newWriter(t, locatedOrNot(tt.at), tt.files)
				writer.Access = fakeAccess{mode: mode}

				result, err := tt.call(t.Context(), writer, tokens)
				if err != nil {
					t.Fatalf("the write failed rather than proposing: %v", err)
				}

				if len(target.commits) != 0 {
					t.Errorf("it committed in %s mode: %+v", mode, target.commits)
				}
				if !result.Proposed {
					t.Error("Proposed = false, so the caller reads this as a commit")
				}
				if result.Commit != "" || result.URL != "" {
					t.Errorf("result claims a commit at %q: %+v", result.URL, result)
				}

				// Repository and path, so the answer names where the change goes
				// rather than only what it is.
				if result.Repository != repo {
					t.Errorf("repository = %q, want %q", result.Repository, repo)
				}
				if tt.wantPath != "" && result.Path != tt.wantPath {
					t.Errorf("path = %q, want %q", result.Path, tt.wantPath)
				}
				for _, want := range tt.wantDiff {
					if !strings.Contains(result.Diff, want) {
						t.Errorf("the diff cannot be applied, %q is missing:\n%s", want, result.Diff)
					}
				}
			})
		}
	}
}

func TestWriteModeStillCommits(t *testing.T) {
	for _, tt := range writeCalls() {
		t.Run(tt.name, func(t *testing.T) {
			writer, target, tokens := newWriter(t, locatedOrNot(tt.at), tt.files)
			writer.Access = fakeAccess{mode: store.ModeWrite}

			result, err := tt.call(t.Context(), writer, tokens)
			if err != nil {
				t.Fatalf("the write failed: %v", err)
			}
			if result.Proposed || result.Diff != "" {
				t.Errorf("write mode proposed instead of committing: %+v", result)
			}
			if len(target.commits) != 1 {
				t.Fatalf("commits = %d, want 1", len(target.commits))
			}
			if result.Commit == "" || result.URL == "" {
				t.Error("nothing to hand a human, so the write is only an assertion")
			}
		})
	}
}

// A proposal is still a write an agent asked for, so the read-before-write gate
// applies to it. Otherwise read mode would be the way around ADR-0009.
func TestADR0009_AProposalStillNeedsProof(t *testing.T) {
	files := map[string]string{RootPath: rootFile, "services/jellyfin/dusk.md": jellyfinFile}
	writer, _, _ := newWriter(t, located("services/jellyfin/dusk.md"), files)
	writer.Access = fakeAccess{mode: store.ModeRead}

	_, err := writer.Declare(t.Context(), "", write.Declaration{Ref: jellyfin, Title: "x"})

	var rejection *proof.Rejection
	if !errors.As(err, &rejection) {
		t.Fatalf("err = %v, want a proof rejection", err)
	}
}

// Not knowing the mode is a failure to answer, not permission to commit.
func TestAnUnreadableModeRefusesTheWrite(t *testing.T) {
	files := map[string]string{RootPath: rootFile, "services/jellyfin/dusk.md": jellyfinFile}
	writer, target, tokens := newWriter(t, located("services/jellyfin/dusk.md"), files)
	writer.Access = fakeAccess{err: errors.New("no credentials, Dusk is not onboarded")}

	token := tokens.Issue(proof.FromGet, map[string]string{jellyfin: "v1"})
	_, err := writer.Declare(t.Context(), token.ID, write.Declaration{Ref: jellyfin, Title: "x"})

	if err == nil {
		t.Fatal("the write went ahead without knowing what Dusk may do")
	}
	if len(target.commits) != 0 {
		t.Errorf("it committed anyway: %+v", target.commits)
	}
}

func locatedOrNot(path string) *index.Location {
	if path == "" {
		return nil
	}
	return located(path)
}
