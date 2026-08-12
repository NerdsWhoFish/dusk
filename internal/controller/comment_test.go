package controller

import (
	"strings"
	"testing"

	"github.com/FetchHQ/dusk/internal/index"
)

// A pull request that changes files but not the catalog has to say so.
// Silence would read as "the bot did not run".
func TestCommentSaysWhenNothingChanged(t *testing.T) {
	body := renderComment(nil, "")

	if !strings.Contains(body, "changes nothing in the catalog") {
		t.Errorf("comment = %q", body)
	}
}

// Removal is the change worth interrupting somebody for: merging it takes
// everything anybody had written down about the thing.
func TestCommentFlagsARemoval(t *testing.T) {
	body := renderComment([]index.Change{
		{Kind: index.ChangeAdded, Ref: "service:home/new", After: "service New"},
		{Kind: index.ChangeRemoved, Ref: "service:home/old", Before: "service Old"},
	}, "")

	if !strings.Contains(body, "stop existing") {
		t.Errorf("a removal was not flagged:\n%s", body)
	}
	if !strings.Contains(body, "service:home/old") {
		t.Errorf("the removed entity was not named:\n%s", body)
	}
	if !strings.Contains(body, "service:home/new") {
		t.Errorf("the added entity was not named:\n%s", body)
	}
}

// A comment longer than the diff it describes is one nobody reads.
func TestCommentTruncatesALongList(t *testing.T) {
	var changes []index.Change
	for i := range 60 {
		changes = append(changes, index.Change{
			Kind: index.ChangeAdded, Ref: string(rune('a'+i%26)) + "-service", After: "service",
		})
	}

	body := renderComment(changes, "")
	if !strings.Contains(body, "and 35 more") {
		t.Errorf("a long list was not truncated:\n%s", body)
	}
}

// A multi-paragraph description would otherwise become a wall inside a bullet.
func TestCommentCollapsesProse(t *testing.T) {
	body := renderComment([]index.Change{{
		Kind: index.ChangeModified, Ref: "service:home/x", Field: "description",
		Before: "One line.\n\nAnd another.",
		After:  strings.Repeat("long ", 40),
	}}, "")

	if strings.Contains(body, "\n\nAnd another") {
		t.Errorf("prose was not collapsed onto one line:\n%s", body)
	}
	if !strings.Contains(body, "…") {
		t.Errorf("a long value was not shortened:\n%s", body)
	}
}

// The link is the point of a preview, but a deployment with no host to link
// to should get no link rather than a broken one.
func TestCommentLinksOnlyWhenThereIsSomewhereToLink(t *testing.T) {
	change := []index.Change{{Kind: index.ChangeAdded, Ref: "service:home/x", After: "service"}}

	if strings.Contains(renderComment(change, ""), "http") {
		t.Error("a comment linked somewhere with no host configured")
	}
	if !strings.Contains(renderComment(change, "https://dusk.example.com/?ref=x"), "https://dusk.example.com") {
		t.Error("a comment did not link to the preview")
	}
}
