package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"

	"github.com/NerdsWhoFish/dusk/internal/page"
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

	writeJSON(w, http.StatusOK, map[string]any{
		"title":  declared.Title,
		"prose":  prose,
		"blocks": page.Resolve(r.Context(), s.catalog, declared),

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
