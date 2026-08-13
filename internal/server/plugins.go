package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/NerdsWhoFish/dusk/internal/plugin"
)

// Plugins is the slice of the plugin manager the HTTP surface needs, declared
// here so the server does not depend on how installing works.
type Plugins interface {
	Available(ctx context.Context) ([]plugin.Offer, error)
	Install(ctx context.Context, id string) (*plugin.Installed, error)
	Uninstall(id string) error
	Configure(ctx context.Context, id string, config map[string]any) error
}

// handleAPIPlugins answers GET /api/plugins with the marketplace, annotated
// with what is installed here. Reaching GitHub can fail, and a marketplace
// that cannot be listed must not hide what is already installed.
func (s *Server) handleAPIPlugins(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		writeJSON(w, http.StatusOK, map[string]any{"plugins": []any{}})
		return
	}

	offers, err := s.plugins.Available(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"plugins": []any{},
			"problem": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": offers})
}

// handleAPIInstall answers POST /api/plugins/{id}/install. Installing an
// already-installed plugin is how an update is applied, so this is also the
// endpoint the update button calls.
func (s *Server) handleAPIInstall(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.Error(w, `{"error":"plugins are not enabled"}`, http.StatusNotImplemented)
		return
	}

	record, err := s.plugins.Install(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

// handleAPIUninstall answers POST /api/plugins/{id}/uninstall. What the plugin
// observed stays in the index: uninstalling is not a way to delete history.
func (s *Server) handleAPIUninstall(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.Error(w, `{"error":"plugins are not enabled"}`, http.StatusNotImplemented)
		return
	}

	if err := s.plugins.Uninstall(r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uninstalled": r.PathValue("id")})
}

// handleAPIConfigure answers POST /api/plugins/{id}/config. Only non-sensitive
// fields arrive here: ADR-0041 keeps secrets off every surface an agent can
// reach, and this endpoint is one of them.
func (s *Server) handleAPIConfigure(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.Error(w, `{"error":"plugins are not enabled"}`, http.StatusNotImplemented)
		return
	}

	var config map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&config); err != nil {
		http.Error(w, `{"error":"that configuration could not be read"}`, http.StatusBadRequest)
		return
	}

	if err := s.plugins.Configure(r.Context(), r.PathValue("id"), config); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": r.PathValue("id")})
}
