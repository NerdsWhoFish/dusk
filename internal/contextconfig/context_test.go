package contextconfig_test

import (
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/contextconfig"
)

func TestParseContextProfile(t *testing.T) {
	profile, err := contextconfig.Parse([]byte(`---
dusk: context/v1
budget: 12000
sections: [inventory, repository-notes]
inventory: counts
kind_order: [service, host]
full_note_kinds: [reference, idea]
---
Always read the runbook before changing storage.
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if profile.Budget != 12000 || profile.Inventory != "counts" || len(profile.Sections) != 2 {
		t.Errorf("profile = %+v", profile)
	}
	if strings.Join(profile.FullNoteKinds, ",") != "reference,idea" {
		t.Errorf("full note kinds = %v", profile.FullNoteKinds)
	}
	if !strings.Contains(profile.Instructions, "runbook") {
		t.Errorf("instructions = %q", profile.Instructions)
	}
}

func TestParseRejectsPolicyThatWouldSilentlyDoNothing(t *testing.T) {
	for _, body := range []string{
		"---\ndusk: context/v2\n---\n",
		"---\ndusk: context/v1\nsections: [mystery]\n---\n",
		"---\ndusk: context/v1\ninventory: everything\n---\n",
		"---\ndusk: context/v1\nfull_note_kinds: [idea, idea]\n---\n",
		"---\ndusk: context/v1\nfull_note_kinds: ['']\n---\n",
	} {
		if _, err := contextconfig.Parse([]byte(body)); err == nil {
			t.Errorf("Parse(%q) succeeded", body)
		}
	}
}

func TestFormatRoundTripsAContextProfile(t *testing.T) {
	want := contextconfig.Profile{
		Budget: 12000, Sections: []string{"estate-notes", "inventory"},
		Inventory: "counts", KindOrder: []string{"service", "host"},
		FullNoteKinds: []string{},
		Instructions:  "Read the storage runbook first.",
	}

	body, err := contextconfig.Format(want)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	got, err := contextconfig.Parse(body)
	if err != nil {
		t.Fatalf("Parse formatted profile: %v", err)
	}
	if got.Budget != want.Budget || got.Inventory != want.Inventory || got.Instructions != want.Instructions {
		t.Fatalf("profile = %+v, want %+v", got, want)
	}
	if strings.Join(got.Sections, ",") != strings.Join(want.Sections, ",") {
		t.Errorf("sections = %v, want %v", got.Sections, want.Sections)
	}
	if strings.Join(got.KindOrder, ",") != strings.Join(want.KindOrder, ",") {
		t.Errorf("kind order = %v, want %v", got.KindOrder, want.KindOrder)
	}
	if got.FullNoteKinds == nil || len(got.FullNoteKinds) != 0 {
		t.Errorf("full note kinds = %#v, want an explicit empty list", got.FullNoteKinds)
	}
}

func TestOmittedFullNoteKindsUseTheTokenSafeDefault(t *testing.T) {
	profile, err := contextconfig.Parse([]byte("---\ndusk: context/v1\n---\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := strings.Join(profile.FullNoteKinds, ","); got != "reference,todo,idea" {
		t.Errorf("full note kinds = %q", got)
	}
}
