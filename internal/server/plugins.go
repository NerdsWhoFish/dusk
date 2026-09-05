package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// Plugins is the slice of the plugin manager the HTTP surface needs, declared
// here so the server does not depend on how installing works.
type Plugins interface {
	Available(ctx context.Context) ([]plugin.Offer, error)
	Refresh(ctx context.Context) ([]plugin.Offer, error)
	Checked() time.Time
	Install(ctx context.Context, id string) (*plugin.Installed, error)
	Uninstall(id string) error
	Configure(ctx context.Context, id, instance string, config map[string]any, expectedVersion string) error

	// Restart is the way back from a plugin the supervisor gave up on, and the
	// only one: configuring a plugin needs it running (ADR-0054).
	Restart(ctx context.Context, id string) error

	// Views is what every running plugin contributes to an entity page of one
	// kind, and Contributions is everything one plugin contributes, in both
	// slots, for a surface that narrows differently (ADR-0064).
	Views(kind string) []plugin.View
	Contributions(id string) []plugin.View

	// Asset is a plugin's JavaScript, which Dusk serves from its own origin so
	// nothing on the page is fetched from anywhere else (ADR-0020).
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
	s.protectConfigurations(offers)

	// The error rides alongside the offers rather than replacing them: a rate
	// limit must not hide the plugins somebody has installed and is running.
	answer := map[string]any{"plugins": offers, "checked": s.plugins.Checked()}
	if err != nil {
		answer["problem"] = err.Error()
	}
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) protectConfigurations(offers []plugin.Offer) {
	if s.tokens == nil {
		return
	}
	for i := range offers {
		offer := &offers[i]
		offer.ConfigProofs = map[string]string{}
		for instance, version := range offer.ConfigVersions {
			subject := proof.Configuration(offer.ID, instance)
			offer.ConfigProofs[instance] = s.tokens.Issue(proof.FromConfigure, map[string]string{subject.Ref: version}).ID
		}
	}
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
	s.protectConfigurations(offers)
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
		if errors.Is(err, plugin.ErrInvalidID) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uninstalled": r.PathValue("id")})
}

// handleAPIRestart answers POST /api/plugins/{id}/restart. The supervisor gives
// up on a plugin that will not stay up, and this is how somebody who has fixed
// whatever it was tells it to try again.
func (s *Server) handleAPIRestart(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.Error(w, `{"error":"plugins are not enabled"}`, http.StatusNotImplemented)
		return
	}

	id := r.PathValue("id")
	if err := s.plugins.Restart(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restarted": id})
}

// handleAPIConfigure answers the config routes, for a plugin's own
// configuration and for a named instance. It is the one surface a sensitive
// value may be entered on, being the one that records it nowhere (ADR-0041).
func (s *Server) handleAPIConfigure(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.Error(w, `{"error":"plugins are not enabled"}`, http.StatusNotImplemented)
		return
	}

	var body struct {
		Settings map[string]any `json:"settings"`
		Version  string         `json:"version"`
		Proof    string         `json:"proof"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, `{"error":"that configuration could not be read"}`, http.StatusBadRequest)
		return
	}

	id, instance := r.PathValue("id"), r.PathValue("instance")
	if s.tokens == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "configuration writes need a proof store"})
		return
	}
	if err := s.tokens.AuthorizeUpdateFrom(body.Proof, proof.Configuration(id, instance), body.Version); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	if err := s.plugins.Configure(r.Context(), id, instance, body.Settings, body.Version); err != nil {
		writeError(w, err)
		return
	}

	answer := map[string]any{"configured": id}
	if instance != "" {
		answer["instance"] = instance
	}
	writeJSON(w, http.StatusOK, answer)
}
