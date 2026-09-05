package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

func (s *Server) previewReads(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ref := r.URL.Query().Get("ref")
		if !strings.HasPrefix(ref, "refs/pull/") {
			next.ServeHTTP(w, r)
			return
		}
		if !index.IsPreviewRef(ref) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid pull request preview ref"})
			return
		}
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || liveOnly(r.URL.Path) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "pull request previews are read-only; return to the live catalog for this operation"})
			return
		}
		resolved, err := s.catalog.ResolvePreview(r.Context(), ref)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, index.ErrNotFound) {
				status = http.StatusNotFound
			} else if errors.Is(err, index.ErrPreviewAmbiguous) {
				status = http.StatusConflict
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		query := r.URL.Query()
		query.Set("ref", resolved)
		r.URL.RawQuery = query.Encode()
		next.ServeHTTP(w, r)
	})
}

func liveOnly(path string) bool {
	return path == "/context" || path == "/repository" || path == "/plugins" || strings.HasPrefix(path, "/plugins/")
}
