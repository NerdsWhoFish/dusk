package plugin_test

import (
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/plugin"
)

// contributed starts a stand-in declaring these views and returns what its
// offer carries, which is what the plugin's own page renders from.
func contributed(t *testing.T, id string, views []standInView) []plugin.View {
	t.Helper()

	manager, _ := manager(t)
	install(t, manager.Store, standIn{ID: id, Kinds: []string{"thing"}, Views: views})

	manager.Restore(t.Context())
	t.Cleanup(manager.Stop)

	offers, err := manager.Available(t.Context())
	if err != nil {
		t.Fatalf("available: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offers = %d, want the one installed plugin", len(offers))
	}
	return offers[0].Views
}

// A declared view draws a result set and a plugin's own page has none, so one
// mounted there fell through to its own empty text: an author following
// ADR-0020's default tier got a blank panel and no error to search for.
func TestADR0064_ADeclaredViewIsRefusedWhereThereIsNoResultSet(t *testing.T) {
	views := contributed(t, "declaring", []standInView{{
		Title: "Everything", Declared: true,
		Slot: int32(duskv1alpha1.UISlot_UI_SLOT_PLUGIN),
	}})

	if len(views) != 1 {
		t.Fatalf("views = %d, want the one contribution", len(views))
	}
	if views[0].Spec != nil {
		t.Error("the contribution still carries a spec, so the page will try to render it")
	}
	if views[0].Problem == "" {
		t.Error("nothing says why, so the page renders the plugin's empty text as though there were simply nothing to show")
	}
}

// The same spec on an entity page has the entity to render over, so nothing
// about it is refused: only the slot with no result set is.
func TestADeclaredViewOnAnEntityPageIsUntouched(t *testing.T) {
	views := contributed(t, "entitydeclaring", []standInView{{
		Title: "This thing", Declared: true, Kinds: []string{"thing"},
	}})

	if len(views) != 0 {
		t.Fatalf("the plugin's own page offers %d views, want none: this one is for an entity page", len(views))
	}
}
