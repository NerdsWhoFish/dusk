package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/NerdsWhoFish/dusk/internal/page"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
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

	blocks := page.Resolve(r.Context(), s.catalog, declared)
	s.mountViews(blocks)

	writeJSON(w, http.StatusOK, map[string]any{
		"title":  declared.Title,
		"prose":  prose,
		"search": declared.Searchable(),
		"blocks": blocks,

		// A page that failed to parse falls back to the default and says so,
		// rather than showing an error where the catalog should be.
		"problem": problem,
	})
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

// mountViews resolves a view block to the element and asset URL of a running
// plugin. Only the plugin manager knows what is running, which is why this
// happens here rather than in the page package.
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

		// Any kind: a homepage block names its own ref, so what the plugin
		// declared it applies to does not narrow anything here.
		var matching []plugin.View
		for _, view := range s.plugins.Views("") {
			if view.Plugin != block.Plugin {
				continue
			}
			if block.Element == "" || view.Element == block.Element {
				matching = append(matching, view)
			}
		}

		switch {
		case len(matching) == 0:
			block.Err = block.Plugin + " is not running, or contributes no such view"

		// Picking one silently would render something the page did not ask
		// for, and look like the block was wrong rather than incomplete.
		case len(matching) > 1:
			block.Err = fmt.Sprintf("%s contributes %d views, so name one with `element:`: %s",
				block.Plugin, len(matching), strings.Join(elementsOf(matching), ", "))

		default:
			block.Element, block.Source = matching[0].Element, matching[0].Source
		}
	}
}

func elementsOf(views []plugin.View) []string {
	names := make([]string, 0, len(views))
	for _, view := range views {
		names = append(names, view.Element)
	}
	return names
}
