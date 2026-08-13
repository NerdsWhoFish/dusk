package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/NerdsWhoFish/dusk/internal/plugin"
)

// Plugins is the slice of the plugin manager the HTTP surface needs, declared
// here so the server does not depend on how installing works.
type Plugins interface {
	Available(ctx context.Context) ([]plugin.Offer, error)
	Refresh(ctx context.Context) ([]plugin.Offer, error)
	Checked() time.Time
	Install(ctx context.Context, id string) (*plugin.Installed, error)
	Uninstall(id string) error
	Configure(ctx context.Context, id, instance string, config map[string]any) error

	// Views and Asset are how a plugin renders itself: Dusk mounts the element
	// and serves its JavaScript from its own origin (ADR-0020).
	Views(kind string) []plugin.View
	Asset(id, sha string) (plugin.Asset, bool)

	// Actions and PluginActions are the same declaration the UI turns into a
	// button and an agent invokes, so an author declares a capability once and
	// does not choose an audience (ADR-0041).
	Actions(kind string) []plugin.Action
	PluginActions(id string) []plugin.Action
	Enable(id, action string, on bool) error

	Invoke(ctx context.Context, request plugin.Request) (*plugin.Outcome, error)
	Preview(ctx context.Context, request plugin.Request) (*plugin.Outcome, error)
	Status(ctx context.Context, id, handle string) (*plugin.Outcome, error)

	// Output is what a plugin printed, so diagnosing it does not mean reading
	// the pod that runs Dusk.
	Output(id string) []plugin.Line
}

// handlePluginAsset answers GET /plugin-assets/{plugin}/{name}. Content
// addressed, so immutable is safe here in a way it is not under a fixed name:
// a different asset has a different URL.
func (s *Server) handlePluginAsset(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.NotFound(w, r)
		return
	}

	sha := strings.TrimSuffix(r.PathValue("name"), ".js")
	asset, ok := s.plugins.Asset(r.PathValue("plugin"), sha)
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", assetMaxAge)
	if _, err := w.Write(asset.Body); err != nil {
		s.log.Debug("the browser went away mid-asset", "error", err)
	}
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

	// The error rides alongside the offers rather than replacing them: a rate
	// limit must not hide the plugins somebody has installed and is running.
	answer := map[string]any{"plugins": offers, "checked": s.plugins.Checked()}
	if err != nil {
		answer["problem"] = err.Error()
	}
	writeJSON(w, http.StatusOK, answer)
}

// handleAPIRefresh answers POST /api/plugins/refresh by asking GitHub now.
// The listing is cached for a day, so this is how somebody checks for an
// update without waiting for it.
func (s *Server) handleAPIRefresh(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		writeJSON(w, http.StatusOK, map[string]any{"plugins": []any{}})
		return
	}

	offers, err := s.plugins.Refresh(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": offers, "checked": s.plugins.Checked()})
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

// handleAPIConfigure answers the config routes, for a plugin's own
// configuration and for a named instance. It is the one surface a sensitive
// value may be entered on, being the one that records it nowhere (ADR-0041).
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

	id, instance := r.PathValue("id"), r.PathValue("instance")
	if err := s.plugins.Configure(r.Context(), id, instance, config); err != nil {
		writeError(w, err)
		return
	}

	answer := map[string]any{"configured": id}
	if instance != "" {
		answer["instance"] = instance
	}
	writeJSON(w, http.StatusOK, answer)
}
