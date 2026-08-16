package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/page"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// Pages supplies the declared portal page, if the config repository has one.
type Pages interface {
	// Home returns the declared page. A missing one satisfies fs.ErrNotExist
	// and gets the default, which ADR-0013 requires to be good enough that
	// declaring stays optional.
	Home(ctx context.Context) ([]byte, error)
}

// handleAPIHome answers GET /api/home with the portal page, blocks resolved.
// Server side, because a browser resolving them would mean a request per block
// and a second implementation of every query.
func (s *Server) handleAPIHome(w http.ResponseWriter, r *http.Request) {
	declared, prose, problem := s.homePage(r.Context())

	blocks := page.Resolve(r.Context(), s.catalog, declared, s.visibilityFor(r))
	s.mountViews(blocks)

	// A block whose query counts is filtered where it is computed; one that
	// lists is filtered here, so a hidden ref cannot arrive under a count that
	// already excluded it (ADR-0051).
	if err := s.hideEntities(r, blocks); err != nil {
		writeError(w, err)
		return
	}

	answer := map[string]any{
		"title":  declared.Title,
		"prose":  prose,
		"search": declared.Searchable(),
		"blocks": blocks,

		// A page that failed to parse falls back to the default and says so,
		// rather than showing an error where the catalog should be.
		"problem": problem,
	}

	// The notes the page showed go in a token, so closing one is proof of the
	// read that surfaced it rather than a write from nowhere (ADR-0009).
	if s.tokens != nil {
		seen := map[string]string{}
		for _, block := range blocks {
			for _, note := range block.Notes {
				seen[note.GetId()] = note.GetContentHash()
			}
		}
		answer["proof"] = s.tokens.Issue(proof.FromGet, seen).ID
	}
	writeJSON(w, http.StatusOK, answer)
}

// hideEntities drops the entities a viewer may not see from every resolved
// block. The limit was applied before this, so a block can come back shorter
// than it asked for, which is the same trade search already makes.
func (s *Server) hideEntities(r *http.Request, blocks []page.Resolved) error {
	if !s.visibilityFor(r).Restricted() {
		return nil
	}

	visible, err := s.visible(r)
	if err != nil {
		return err
	}

	for i := range blocks {
		block := &blocks[i]
		kept := make([]*duskv1alpha1.Entity, 0, len(block.Entities))
		for _, entity := range block.Entities {
			if visible(entity.GetRef()) {
				kept = append(kept, entity)
			}
		}
		block.Entities = kept
	}
	return nil
}

func (s *Server) homePage(ctx context.Context) (page.Page, string, string) {
	if s.pages == nil {
		return page.Default(), "", ""
	}

	body, err := s.pages.Home(ctx)
	if errors.Is(err, fs.ErrNotExist) {
		return page.Default(), "", ""
	}
	if err != nil {
		s.log.Warn("could not read the declared home page, using the default", "error", err)
		return page.Default(), "", err.Error()
	}

	declared, prose, err := page.Parse(body)
	if err != nil {
		s.log.Warn("the declared home page does not parse, using the default", "error", err)
		return page.Default(), "", err.Error()
	}
	return declared, prose, ""
}

// mountViews resolves a view block to a contribution of a running plugin: an
// element and its asset URL, or a declared spec Dusk draws itself. Only the
// plugin manager knows what is running, hence here rather than in `page`.
func (s *Server) mountViews(blocks []page.Resolved) {
	for i := range blocks {
		block := &blocks[i]
		if block.Type != page.TypeView || block.Err != "" {
			continue
		}
		if s.plugins == nil {
			block.Err = "plugins are not enabled"
			continue
		}

		matching := candidateViews(s.plugins.Contributions(block.Plugin), block.Element)
		switch {
		case len(matching) == 0:
			block.Err = block.Plugin + " is not running, or contributes no such view"

		// Picking one silently would render something the page did not ask
		// for, and look like the block was wrong rather than incomplete.
		case len(matching) > 1:
			block.Err = fmt.Sprintf("%s contributes %d views, so name one with `element:`: %s",
				block.Plugin, len(matching), strings.Join(namesOf(matching), ", "))

		default:
			block.Element, block.Source, block.Spec = matching[0].Element, matching[0].Source, matching[0].Spec
		}
	}
}

// candidateViews narrows a plugin's contributions to the ones a page may mount.
// The kinds a contribution declares are ignored on purpose: they say where it
// mounts on an entity page, and a block supplies its own ref or query.
func candidateViews(views []plugin.View, named string) []plugin.View {
	var matching []plugin.View
	for _, view := range views {
		if view.Slot != plugin.SlotEntity {
			continue
		}
		if named == "" || view.Element == named || view.Title == named {
			matching = append(matching, view)
		}
	}
	return matching
}

// namesOf is what `element:` accepts for each candidate. A declared view has no
// tag, so its title is the only name it has, and an ambiguity that lists a name
// nothing selects is worse than one that lists none.
func namesOf(views []plugin.View) []string {
	names := make([]string, 0, len(views))
	for _, view := range views {
		if view.Element != "" {
			names = append(names, view.Element)
			continue
		}
		names = append(names, strconv.Quote(view.Title))
	}
	return names
}
