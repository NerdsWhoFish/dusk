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
---
Always read the runbook before changing storage.
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if profile.Budget != 12000 || profile.Inventory != "counts" || len(profile.Sections) != 2 {
		t.Errorf("profile = %+v", profile)
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
	} {
		if _, err := contextconfig.Parse([]byte(body)); err == nil {
			t.Errorf("Parse(%q) succeeded", body)
		}
	}
}
