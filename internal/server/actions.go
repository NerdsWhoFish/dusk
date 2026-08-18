package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/plugin"
)

// maxInvocation bounds an action's parameters. They are call arguments, not a
// payload, and nothing legitimate approaches this.
const maxInvocation = 64 << 10

// invocation is what the browser posts to run an action. It mirrors the MCP
// input, because one declaration serves both surfaces (ADR-0041).
type invocation struct {
	Params  map[string]any `json:"params,omitempty"`
	Proof   string         `json:"proof,omitempty"`
	Plugin  string         `json:"plugin,omitempty"`
	Confirm bool           `json:"confirm,omitempty"`
	Preview bool           `json:"preview,omitempty"`
	Key     string         `json:"idempotency_key,omitempty"`

	// Elicited answers a question a previous invocation returned, which is how
	// the browser resumes an action rather than starting it again (ADR-0046).
	Elicited *plugin.Answer `json:"elicited,omitempty"`
}

func (s *Server) readInvocation(w http.ResponseWriter, r *http.Request) (invocation, bool) {
	var body invocation
	if r.ContentLength == 0 {
		return body, true
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInvocation)).Decode(&body); err != nil {
		http.Error(w, `{"error":"that invocation could not be read"}`, http.StatusBadRequest)
		return body, false
	}
	return body, true
}

// handleAPIInvoke answers POST /api/entities/{ref}/actions/{action}.
func (s *Server) handleAPIInvoke(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.Error(w, `{"error":"plugins are not enabled"}`, http.StatusNotImplemented)
		return
	}

	body, ok := s.readInvocation(w, r)
	if !ok {
		return
	}
	s.invoke(w, r, plugin.Request{
		Ref:            r.PathValue("ref"),
		Action:         r.PathValue("action"),
		Plugin:         body.Plugin,
		Params:         body.Params,
		Proof:          body.Proof,
		Confirm:        body.Confirm,
		Preview:        body.Preview,
		Actor:          s.actor(r),
		IdempotencyKey: body.Key,

		CanResume: true,
		Elicited:  body.Elicited,
	})
}

// handleAPIPluginInvoke answers POST /api/plugins/{id}/actions/{action}, for an
// action about the plugin rather than about one entity.
func (s *Server) handleAPIPluginInvoke(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.Error(w, `{"error":"plugins are not enabled"}`, http.StatusNotImplemented)
		return
	}

	body, ok := s.readInvocation(w, r)
	if !ok {
		return
	}
	s.invoke(w, r, plugin.Request{
		Action:         r.PathValue("action"),
		Plugin:         r.PathValue("id"),
		Params:         body.Params,
		Proof:          body.Proof,
		Confirm:        body.Confirm,
		Preview:        body.Preview,
		Actor:          s.actor(r),
		IdempotencyKey: body.Key,

		CanResume: true,
		Elicited:  body.Elicited,
	})
}

func (s *Server) invoke(w http.ResponseWriter, r *http.Request, request plugin.Request) {
	run := s.plugins.Invoke
	if request.Preview {
		run = s.plugins.Preview
	}

	outcome, err := run(r.Context(), request)
	switch {
	case errors.Is(err, plugin.ErrNeedsApproval):
		// Not a failure: the caller is being asked, and 409 is what a client
		// branches on to offer the confirmation rather than report a problem.
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "needs_approval": true})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, outcome)
	}
}

// handleAPIActionStatus answers GET /api/plugins/{id}/handles/{handle}.
func (s *Server) handleAPIActionStatus(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.Error(w, `{"error":"plugins are not enabled"}`, http.StatusNotImplemented)
		return
	}

	outcome, err := s.plugins.Status(r.Context(), r.PathValue("id"), r.PathValue("handle"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, outcome)
}

// handleAPIEnableAction answers POST on an action's enabled route. Enabling is
// its own route because it decides capability, not configuration (ADR-0015).
func (s *Server) handleAPIEnableAction(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.Error(w, `{"error":"plugins are not enabled"}`, http.StatusNotImplemented)
		return
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		http.Error(w, `{"error":"that could not be read"}`, http.StatusBadRequest)
		return
	}

	id, action := r.PathValue("id"), r.PathValue("action")
	if err := s.plugins.Enable(id, action, body.Enabled); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugin": id, "action": action, "enabled": body.Enabled})
}

// handleAPIOutput answers with what a plugin printed. Its error string says
// roughly what went wrong; this is everything it left out.
func (s *Server) handleAPIOutput(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		http.Error(w, `{"error":"plugins are not enabled"}`, http.StatusNotImplemented)
		return
	}

	id := r.PathValue("id")
	writeJSON(w, http.StatusOK, map[string]any{"plugin": id, "output": s.plugins.Output(id)})
}

// handleAPIEvents answers GET /api/events with what has been run.
func (s *Server) handleAPIEvents(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = min(parsed, 500)
		}
	}

	recorded := s.events.Recent(limit)
	if ref := r.URL.Query().Get("ref"); ref != "" {
		recorded = s.events.RecentFor(ref, limit)
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": asEvents(recorded)})
}

// eventJSON is the wire shape, hand-written for the same reason every other one
// here is: protojson names fields for machines and this is read by a person.
type eventJSON struct {
	ID       string         `json:"id"`
	Chain    string         `json:"chain,omitempty"`
	Plugin   string         `json:"plugin,omitempty"`
	Ref      string         `json:"ref,omitempty"`
	Action   string         `json:"action"`
	Actor    string         `json:"actor,omitempty"`
	Status   string         `json:"status"`
	Started  string         `json:"started_at,omitempty"`
	Finished string         `json:"finished_at,omitempty"`
	Message  string         `json:"message,omitempty"`
	Detail   map[string]any `json:"detail,omitempty"`
}

func asEvents(recorded []*duskv1alpha1.Event) []eventJSON {
	events := make([]eventJSON, 0, len(recorded))
	for _, event := range recorded {
		entry := eventJSON{
			ID:      event.GetId(),
			Chain:   event.GetChain(),
			Plugin:  event.GetPlugin(),
			Ref:     event.GetRef(),
			Action:  event.GetAction(),
			Actor:   event.GetActor(),
			Status:  statusOf(event.GetStatus()),
			Message: event.GetMessage(),
		}
		if at := event.GetStartedAt(); at != nil {
			entry.Started = at.AsTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		if at := event.GetFinishedAt(); at != nil {
			entry.Finished = at.AsTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		if detail := event.GetDetail(); detail != nil {
			entry.Detail = detail.AsMap()
		}
		events = append(events, entry)
	}
	return events
}

func statusOf(status duskv1alpha1.EventStatus) string {
	switch status {
	case duskv1alpha1.EventStatus_EVENT_STATUS_STARTED:
		return "started"
	case duskv1alpha1.EventStatus_EVENT_STATUS_SUCCEEDED:
		return "succeeded"
	case duskv1alpha1.EventStatus_EVENT_STATUS_FAILED:
		return "failed"
	case duskv1alpha1.EventStatus_EVENT_STATUS_DENIED:
		return "denied"
	case duskv1alpha1.EventStatus_EVENT_STATUS_WAITING:
		return "waiting"
	case duskv1alpha1.EventStatus_EVENT_STATUS_UNSPECIFIED:
		return "unknown"
	}
	return "unknown"
}

// actor is who a run is recorded against. A browser session carries a GitHub
// login; an agent presents a shared bearer token and has no identity at all,
// which is worth recording honestly rather than leaving blank.
func (s *Server) actor(r *http.Request) string {
	if identity, ok := s.oauth.Identify(r); ok && identity.Login != "" {
		return identity.Login
	}
	if _, bearer := bearerOf(r); bearer {
		return "agent"
	}
	return "unknown"
}

func bearerOf(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	value := r.Header.Get("Authorization")
	if len(value) > len(prefix) && value[:len(prefix)] == prefix {
		return value[len(prefix):], true
	}
	return "", false
}
