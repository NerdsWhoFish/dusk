package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/internal/server"
)

// contributing is a plugin manager offering exactly these contributions. It
// narrows the way the real one documents: Views answers for one entity kind,
// and a contribution naming kinds is not offered for any other.
type contributing struct {
	server.Plugins
	views []plugin.View
}

func (c contributing) Views(kind string) []plugin.View {
	var out []plugin.View
	for _, view := range c.views {
		if view.Slot != plugin.SlotEntity {
			continue
		}
		if len(view.Kinds) == 0 || slices.Contains(view.Kinds, kind) {
			out = append(out, view)
		}
	}
	return out
}

func (c contributing) Contributions(id string) []plugin.View {
	var out []plugin.View
	for _, view := range c.views {
		if view.Plugin == id {
			out = append(out, view)
		}
	}
	return out
}

// declaredPage is a config repository whose homepage is this markdown.
type declaredPage string

func (d declaredPage) Home(context.Context) ([]byte, error) { return []byte(d), nil }

// flightCatalog is an index holding one observed flight, so a view block's
// query has something to resolve over.
func flightCatalog(t *testing.T) server.Catalog {
	t.Helper()

	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const repository, gitRef = "example/flights", "refs/heads/main"
	declarations := []index.Declaration{{
		Path: "flights.md",
		Entity: &duskv1alpha1.Entity{
			Ref: "flight:airtrail/dl1", Kind: "flight",
			Namespace: "airtrail", Name: "dl1", Title: "DL1",
		},
	}}
	if err := db.Put(t.Context(), repository, gitRef, declarations, nil, nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), repository, gitRef); err != nil {
		t.Fatalf("default view: %v", err)
	}
	return db
}

// homeBlocks reads GET /api/home and returns its blocks as decoded JSON, which
// is what the browser actually renders from.
func homeBlocks(t *testing.T, handler http.Handler) []map[string]any {
	t.Helper()

	rec := get(t, handler, "/api/home")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/home = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var answer struct {
		Blocks []map[string]any `json:"blocks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode the home page: %v", err)
	}
	return answer.Blocks
}

const flightPage = `---
title: Home
blocks:
  - type: view
    title: Recent flights
    plugin: airtrail
    query: kind:flight
---
`

// Mounting resolved a contribution to an element and an asset URL and dropped
// the spec, so ADR-0020's default tier could not be used on a page at all,
// even though a view block is where the result set to draw comes from.
func TestADR0064_APageMountsADeclaredViewAsWellAsADrawnOne(t *testing.T) {
	spec := &plugin.ViewSpec{
		Layout: "table",
		Empty:  "No flights are recorded.",
		Fields: []plugin.ViewField{{Source: "name", Label: "Flight"}},
	}

	handler := build(t, setup{
		store:   registered(),
		catalog: flightCatalog(t),
		pages:   declaredPage(flightPage),
		plugins: contributing{views: []plugin.View{{
			Plugin: "airtrail", Title: "Flights", Slot: plugin.SlotEntity,
			Kinds: []string{"flight"}, Spec: spec,
		}}},
		env: map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})

	blocks := homeBlocks(t, handler)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}

	block := blocks[0]
	if problem, ok := block["error"].(string); ok && problem != "" {
		t.Fatalf("the block carries %q, so nothing was mounted", problem)
	}
	if block["spec"] == nil {
		t.Error("the block carries no spec, so the browser has nothing to render it with")
	}
	if entities, _ := block["entities"].([]any); len(entities) != 1 {
		t.Errorf("entities = %d, want the one flight the query matched", len(entities))
	}
}

// Mounting asked for the contributions that apply to every kind, which is not
// the same question as "do not narrow by kind": a real plugin names the kind
// each of its views is about, so none of them was ever offered to a page.
func TestAViewBlockMountsAContributionScopedToAKind(t *testing.T) {
	handler := build(t, setup{
		store:   registered(),
		catalog: flightCatalog(t),
		pages:   declaredPage(flightPage),
		plugins: contributing{views: []plugin.View{{
			Plugin: "airtrail", Element: "airtrail-flights", Title: "Flights",
			Source: "/plugin-assets/airtrail/abc.js", Slot: plugin.SlotEntity,
			Kinds: []string{"flight"},
		}}},
		env: map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})

	block := homeBlocks(t, handler)[0]
	if problem, ok := block["error"].(string); ok && problem != "" {
		t.Fatalf("the block carries %q, so a contribution about flights was not offered to a block about flights", problem)
	}
	if block["element"] != "airtrail-flights" {
		t.Errorf("element = %v, want airtrail-flights", block["element"])
	}
	if block["source"] != "/plugin-assets/airtrail/abc.js" {
		t.Errorf("source = %v, want the asset URL", block["source"])
	}
}

// A plugin contributing both tiers: `element:` names which, and a declared view
// has no tag, so its title is the name it is selected by.
func TestAViewBlockNamesTheContributionItWants(t *testing.T) {
	plugins := contributing{views: []plugin.View{
		{
			Plugin: "airtrail", Element: "airtrail-flights", Title: "Flights",
			Source: "/plugin-assets/airtrail/abc.js", Slot: plugin.SlotEntity,
			Kinds: []string{"airport"},
		},
		{
			Plugin: "airtrail", Title: "This flight", Slot: plugin.SlotEntity,
			Kinds: []string{"flight"},
			Spec: &plugin.ViewSpec{
				Layout: "badges",
				Fields: []plugin.ViewField{{Source: "name"}},
			},
		},
		// Never a candidate: it is about the plugin, and this page is not
		// the plugin's page.
		{
			Plugin: "airtrail", Title: "Record a flight", Slot: plugin.SlotPlugin,
			Element: "airtrail-flight-form", Source: "/plugin-assets/airtrail/def.js",
		},
	}}

	for _, test := range []struct {
		name        string
		element     string
		wantElement string
		wantSpec    bool
		wantErr     string
	}{
		{
			name:    "ambiguous, so it names every choice",
			wantErr: `airtrail contributes 2 views, so name one with ` + "`element:`" + `: airtrail-flights, "This flight"`,
		},
		{name: "by element tag", element: "airtrail-flights", wantElement: "airtrail-flights"},
		{name: "by declared title", element: "This flight", wantSpec: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := build(t, setup{
				store:   registered(),
				catalog: flightCatalog(t),
				pages:   declaredPage(selectingPage(test.element)),
				plugins: plugins,
				env:     map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
			})

			block := homeBlocks(t, handler)[0]
			problem, _ := block["error"].(string)
			if problem != test.wantErr {
				t.Fatalf("error = %q, want %q", problem, test.wantErr)
			}
			if got, _ := block["element"].(string); got != test.wantElement {
				t.Errorf("element = %q, want %q", got, test.wantElement)
			}
			if got := block["spec"] != nil; got != test.wantSpec {
				t.Errorf("carries a spec = %v, want %v", got, test.wantSpec)
			}
		})
	}
}

func selectingPage(element string) string {
	page := "---\ntitle: Home\nblocks:\n  - type: view\n    title: Flights\n    plugin: airtrail\n    query: kind:flight\n"
	if element != "" {
		page += "    element: " + element + "\n"
	}
	return page + "---\n"
}
