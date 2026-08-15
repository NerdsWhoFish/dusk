package plugin

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/NerdsWhoFish/dusk/internal/events"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
)

// maxChain bounds a composition. A plugin returning a step that leads back to
// itself would otherwise run until something else broke.
const maxChain = 8

// Request is one invocation, from an agent or from the browser.
type Request struct {
	// Ref is the entity acted on. Empty means a plugin-scoped action, which is
	// how something the catalog does not know about yet gets created.
	Ref    string
	Action string

	// Plugin disambiguates when several offer the same action on one kind, and
	// is required for a plugin-scoped action.
	Plugin string

	Params map[string]any

	// Proof is the token from the read the action names, unless it only reads.
	Proof string

	// Actor is who asked, recorded on the event.
	Actor string

	// Confirm agrees to this particular run, which a chain containing anything
	// destructive requires.
	Confirm bool

	// Preview asks what would happen instead of doing it.
	Preview bool

	// Elicit answers a plugin asking the human something mid-action. Only a
	// surface with somebody attached sets it; the rest leave it nil and the
	// plugin is told nobody can answer (ADR-0046).
	Elicit Elicitor

	// CanResume says the caller will come back with an answer rather than
	// wanting one inline. A request cannot hold a browser open, so the question
	// is returned on the Outcome and the same action is invoked again.
	CanResume bool

	// Elicited is the answer to a question a previous invocation returned. It
	// is what makes a resumed action continue rather than start over.
	Elicited *Answer
}

// Elicitor puts a plugin's question to whoever invoked the action.
type Elicitor func(ctx context.Context, ask Ask) (Answer, error)

// Ask is a plugin's question, as a prompt and the shape of the reply.
type Ask struct {
	Prompt string `json:"prompt"`

	// Schema is a JSON Schema object, flat and of primitives, which is all the
	// MCP elicitation contract carries.
	Schema map[string]any `json:"schema,omitempty"`

	// Token is the plugin's own, returned verbatim so it can resume. Opaque
	// here and to whoever answers.
	Token string `json:"token,omitempty"`
}

// Answer is what came back, including the two ways of saying no.
type Answer struct {
	// Outcome is accept, decline, cancel, or unsupported when there was nobody
	// to ask. A plugin may treat a considered no differently from walking away.
	Outcome string `json:"outcome"`

	Values map[string]any `json:"values,omitempty"`

	// Token is the one the question carried, and is how a plugin resumes.
	// Unused when the answer is given inline, since the question is in hand.
	Token string `json:"token,omitempty"`
}

// How an elicitation ended. The first three are MCP's own vocabulary; the
// fourth is Dusk saying there was nobody to ask.
const (
	Accepted     = "accept"
	Declined     = "decline"
	Cancelled    = "cancel"
	Unanswerable = "unsupported"
)

// maxElicit bounds how many times one action may ask before Dusk stops it. A
// plugin still asking after this is looping rather than gathering.
const maxElicit = 4

// Outcome is what happened, including every step of a composition.
type Outcome struct {
	Event  string `json:"event"`
	Chain  string `json:"chain"`
	Plugin string `json:"plugin"`
	Action string `json:"action"`
	Ref    string `json:"ref,omitempty"`
	Class  string `json:"class"`

	Done bool `json:"done"`
	OK   bool `json:"ok"`

	// Handle polls an asynchronous action through Status.
	Handle string `json:"handle,omitempty"`

	Message string         `json:"message"`
	Detail  map[string]any `json:"detail,omitempty"`

	// Ask is a question the plugin returned that nobody could answer inline.
	// The action has not run. Answer it and invoke again with Elicited set.
	Ask *Ask `json:"ask,omitempty"`

	// Preview is what a dry run said, and whether the plugin supports one at
	// all. Surfaced at approval time, because "this cannot be previewed" is
	// what somebody needs before agreeing to something destructive.
	Preview   string `json:"preview,omitempty"`
	Previewed bool   `json:"previewed"`

	Changed []string `json:"changed,omitempty"`

	// Steps are the composition this invocation asked for, in the order they
	// ran, so what actually happened is answerable afterwards.
	Steps []Outcome `json:"steps,omitempty"`
}

// ErrNeedsApproval refuses a run that has not been agreed to. It is a distinct
// error so a caller can offer the confirmation rather than reporting a failure.
var ErrNeedsApproval = errors.New("plugin: this action has not been approved for this run")

// Preview says what an action would do without doing it. It needs no proof
// token: previewing is not mutating, so the read-before-write gate does not
// apply (ADR-0015).
func (m *Manager) Preview(ctx context.Context, request Request) (*Outcome, error) {
	target, err := m.resolve(ctx, request)
	if err != nil {
		return nil, err
	}
	if !target.action.Enabled {
		return nil, notEnabled(target.action)
	}
	return m.preview(ctx, *target, request)
}

func (m *Manager) preview(ctx context.Context, target chosen, request Request) (*Outcome, error) {
	if err := target.running.up(); err != nil {
		return nil, err
	}

	config, err := m.config(target.running.ID, target.instance)
	if err != nil {
		return nil, err
	}

	params, err := structpb.NewStruct(request.Params)
	if err != nil {
		return nil, fmt.Errorf("plugin: those parameters cannot be sent: %w", err)
	}

	answer, err := target.running.client.DryRun(ctx, &duskv1alpha1.DryRunRequest{
		Ref:    request.Ref,
		Action: request.Action,
		Params: params,
		Config: config,
	})
	if err != nil {
		return nil, fmt.Errorf("plugin: %s could not preview %s: %w", target.running.ID, request.Action, err)
	}

	outcome := &Outcome{
		Plugin:    target.running.ID,
		Action:    target.action.Name,
		Ref:       request.Ref,
		Class:     target.action.Class,
		Done:      true,
		OK:        true,
		Previewed: answer.GetSupported(),
		Preview:   answer.GetSummary(),
		Message:   answer.GetSummary(),
	}
	if !answer.GetSupported() {
		outcome.Message = "this action cannot be previewed"
		if answer.GetSummary() != "" {
			outcome.Message = answer.GetSummary()
		}
	}
	if detail := answer.GetDetail(); detail != nil {
		outcome.Detail = detail.AsMap()
	}
	return outcome, nil
}

// Invoke runs an action, and then whatever it asks Dusk to run after it.
func (m *Manager) Invoke(ctx context.Context, request Request) (*Outcome, error) {
	target, err := m.resolve(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := m.permit(ctx, *target, request); err != nil {
		return nil, err
	}
	return m.run(ctx, *target, request, newID(), 0)
}

// permit is every reason a resolved action may still not run.
func (m *Manager) permit(ctx context.Context, target chosen, request Request) error {
	if !target.action.Enabled {
		return notEnabled(target.action)
	}

	if err := m.proven(target, request); err != nil {
		return err
	}

	if target.action.Approval != ApprovalConfirm || request.Confirm {
		return nil
	}
	return m.refuseUnconfirmed(ctx, target, request)
}

// refuseUnconfirmed explains what agreeing would allow, with the preview or
// the absence of one, which is the whole point of asking.
func (m *Manager) refuseUnconfirmed(ctx context.Context, target chosen, request Request) error {
	said := "and it cannot be previewed"
	if preview, err := m.preview(ctx, target, request); err == nil && preview.Previewed {
		said = "and would " + preview.Preview
	}

	chain := ""
	if len(target.action.Then) > 0 {
		chain = fmt.Sprintf(", then %s,", strings.Join(target.action.Then, " and "))
	}
	return fmt.Errorf("%w: %s%s is %s %s. Ask again with confirm set",
		ErrNeedsApproval, target.action.Name, chain, target.action.Class, said)
}

func (m *Manager) proven(target chosen, request Request) error {
	if !mutates(target.action.Class) {
		return nil
	}
	if m.Proof == nil {
		return fmt.Errorf("plugin: no proof store is wired up, so %s cannot be authorized", target.action.Name)
	}

	from := proof.Origin(target.action.ProofFrom)

	// A plugin-scoped action names nothing to have read, so the token proves
	// the caller looked at the catalog rather than at the thing. There is no
	// thing yet: that is what the action is for.
	if !target.action.EntityScoped() {
		if token := m.Proof.Lookup(request.Proof); token == nil || (from != "" && token.Origin != from) {
			return fmt.Errorf("plugin: %s needs the proof token a %s returns, because it changes something outside the catalog",
				target.action.Name, orDefault(target.action.ProofFrom, "read"))
		}
		return nil
	}
	return m.Proof.AuthorizeAction(request.Proof, request.Ref, target.entity.GetProvenance().GetVersion(), from)
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func notEnabled(action Action) error {
	return fmt.Errorf("plugin: %s declares %s but it is not enabled. Enabling it is a deliberate act, on the plugin's page",
		action.Plugin, action.Name)
}

// run invokes one action and follows the composition it returns.
func (m *Manager) run(ctx context.Context, target chosen, request Request, chain string, depth int) (*Outcome, error) {
	if request.Preview {
		return m.preview(ctx, target, request)
	}
	if err := target.running.up(); err != nil {
		return nil, err
	}

	config, err := m.config(target.running.ID, target.instance)
	if err != nil {
		return nil, err
	}
	params, err := structpb.NewStruct(request.Params)
	if err != nil {
		return nil, fmt.Errorf("plugin: those parameters cannot be sent: %w", err)
	}

	event := events.Started(newID(), chain, target.running.ID, request.Ref, target.action.Name, request.Actor, m.now())
	m.Events.Emit(event)

	answer, err := m.converse(ctx, target, request, &duskv1alpha1.InvokeRequest{
		Ref:        request.Ref,
		Action:     target.action.Name,
		Params:     params,
		ProofToken: request.Proof,
		Config:     config,
	})
	if err != nil {
		err = interrupted(target, err)
		m.Events.Settle(events.Finish(event, duskv1alpha1.EventStatus_EVENT_STATUS_FAILED, err.Error(), nil, m.now()))
		return nil, fmt.Errorf("plugin: %s could not run %s: %w", target.running.ID, target.action.Name, err)
	}

	// A question means the action has not run. It is not a failure and it is
	// not a result, so it is settled as its own thing and nothing composes.
	if ask := answer.GetElicit(); ask != nil {
		m.Events.Settle(events.Finish(event, duskv1alpha1.EventStatus_EVENT_STATUS_WAITING,
			"waiting on "+ask.GetPrompt(), nil, m.now()))
		return &Outcome{
			Event: event.GetId(), Chain: chain, Plugin: target.running.ID,
			Action: target.action.Name, Ref: request.Ref, Class: target.action.Class,
			Done: false, OK: true,
			Message: ask.GetPrompt(),
			Ask: &Ask{
				Prompt: ask.GetPrompt(),
				Schema: ask.GetSchema().AsMap(),
				Token:  ask.GetToken(),
			},
		}, nil
	}

	outcome := &Outcome{
		Event:   event.GetId(),
		Chain:   chain,
		Plugin:  target.running.ID,
		Action:  target.action.Name,
		Ref:     request.Ref,
		Class:   target.action.Class,
		Done:    answer.GetDone(),
		OK:      answer.GetOk(),
		Handle:  answer.GetHandle(),
		Message: answer.GetMessage(),
		Changed: answer.GetChanged(),
	}
	if detail := answer.GetDetail(); detail != nil {
		outcome.Detail = detail.AsMap()
	}

	status := duskv1alpha1.EventStatus_EVENT_STATUS_SUCCEEDED
	if !answer.GetOk() {
		status = duskv1alpha1.EventStatus_EVENT_STATUS_FAILED
	}
	m.Events.Settle(events.Finish(event, status, answer.GetMessage(), answer.GetDetail(), m.now()))

	if !answer.GetOk() {
		return outcome, nil
	}

	// What the plugin just changed is not what the catalog last saw, so the
	// instance that watches it is asked to look again rather than staying wrong
	// until its interval comes round.
	if mutates(target.action.Class) {
		m.reobserve(target)
	}

	outcome.Steps = m.compose(ctx, target, request, answer.GetThen(), chain, depth)
	return outcome, nil
}

// exitGrace is how long an unavailable call waits for the process to be reaped
// before deciding it is merely unreachable. gRPC sees the socket close well
// before Wait returns, and the difference decides which error the caller gets.
const exitGrace = 500 * time.Millisecond

// interrupted replaces a transport error with what actually happened when the
// cause was the plugin's process going away. A mutating action cut off this
// way may or may not have landed, and there is nobody left to ask (ADR-0054).
func interrupted(target chosen, err error) error {
	if status.Code(err) != codes.Unavailable {
		return err
	}

	select {
	case <-target.running.exited:
	case <-time.After(exitGrace):
	}

	gone := target.running.up()
	switch {
	case gone == nil:
		return err
	case !mutates(target.action.Class):
		return gone
	default:
		return fmt.Errorf("%w, so whether %s took effect is not known: nothing survived to say",
			gone, target.action.Name)
	}
}

// aboutOf decides what a chained step is about. An action applying to no kind
// has no entity to inherit, so it belongs to the plugin that declared it
// rather than to the parent's ref.
func (m *Manager) aboutOf(from chosen, step *duskv1alpha1.Next, request Request) (ref, owner string) {
	if named := step.GetRef(); named != "" {
		return named, ""
	}
	for _, action := range m.actionsFor(from.running) {
		if action.Name == step.GetAction() && len(action.Kinds) == 0 {
			return "", from.running.ID
		}
	}
	return request.Ref, ""
}

// converse invokes an action and answers whatever it asks for, until it stops
// asking or runs out of turns. An elicitation ends a turn: nothing else in that
// response is acted on, because the action has not finished (ADR-0046).
func (m *Manager) converse(ctx context.Context, target chosen, request Request, call *duskv1alpha1.InvokeRequest) (*duskv1alpha1.InvokeResponse, error) {
	if given := request.Elicited; given != nil {
		values, err := structpb.NewStruct(given.Values)
		if err != nil {
			return nil, fmt.Errorf("plugin: that answer cannot be sent: %w", err)
		}
		call.Elicited = &duskv1alpha1.Elicited{Token: given.Token, Values: values, Outcome: given.Outcome}
	}

	for turn := 0; ; turn++ {
		answer, err := target.running.client.Invoke(ctx, call)
		if err != nil {
			return nil, err
		}

		ask := answer.GetElicit()
		if ask == nil {
			return answer, nil
		}

		// A request cannot hold a browser open, so the question goes back to
		// whoever asked and the same action is invoked again with the answer.
		if request.Elicit == nil && request.CanResume {
			return answer, nil
		}
		if turn >= maxElicit {
			return nil, fmt.Errorf("plugin: %s asked for input more than %d times running %s, which is a loop rather than a conversation",
				target.running.ID, maxElicit, target.action.Name)
		}

		given, err := m.ask(ctx, request, ask)
		if err != nil {
			return nil, err
		}
		call.Elicited = given
	}
}

// ask puts one question to the invoker. Nobody attached is answered rather than
// failed, so the plugin chooses between a default and its own refusal.
func (m *Manager) ask(ctx context.Context, request Request, elicit *duskv1alpha1.Elicit) (*duskv1alpha1.Elicited, error) {
	if request.Elicit == nil {
		return &duskv1alpha1.Elicited{Token: elicit.GetToken(), Outcome: Unanswerable}, nil
	}

	given, err := request.Elicit(ctx, Ask{Prompt: elicit.GetPrompt(), Schema: elicit.GetSchema().AsMap()})
	if err != nil {
		return nil, fmt.Errorf("plugin: that question could not be put to anybody: %w", err)
	}

	values, err := structpb.NewStruct(given.Values)
	if err != nil {
		return nil, fmt.Errorf("plugin: that answer cannot be sent back: %w", err)
	}
	return &duskv1alpha1.Elicited{Token: elicit.GetToken(), Values: values, Outcome: given.Outcome}, nil
}

// compose runs what an invocation asked for next. Only an action its descriptor
// already declared is accepted: approving a chain has to mean approving what
// was declared, not what the plugin appended once it was running.
func (m *Manager) compose(ctx context.Context, from chosen, request Request, steps []*duskv1alpha1.Next, chain string, depth int) []Outcome {
	if len(steps) == 0 {
		return nil
	}
	if depth >= maxChain {
		return []Outcome{{Chain: chain, Done: true, OK: false,
			Message: fmt.Sprintf("the composition is more than %d steps deep and was stopped", maxChain)}}
	}

	// A step cannot hand a question back: the caller is already holding the
	// result of the action that started the chain. One that asks is told nobody
	// can answer, and decides for itself (ADR-0046).
	request.CanResume = false
	request.Elicited = nil

	var ran []Outcome
	for _, step := range steps {
		outcome := m.step(ctx, from, request, step, chain, depth)
		ran = append(ran, outcome)

		// A chain stops at its first failure. Running the rest of a sequence
		// whose earlier step did not happen is how one bad state becomes two.
		if !outcome.OK {
			break
		}
	}
	return ran
}

func (m *Manager) step(ctx context.Context, from chosen, request Request, step *duskv1alpha1.Next, chain string, depth int) Outcome {
	refused := func(message string) Outcome {
		event := events.Started(newID(), chain, from.running.ID, step.GetRef(), step.GetAction(), request.Actor, m.now())
		m.Events.Settle(events.Finish(event, duskv1alpha1.EventStatus_EVENT_STATUS_DENIED, message, nil, m.now()))
		return Outcome{Event: event.GetId(), Chain: chain, Action: step.GetAction(), Ref: step.GetRef(), Done: true, Message: message}
	}

	if !slices.Contains(from.action.Then, step.GetAction()) {
		return refused(fmt.Sprintf("%s did not declare %s as something that may follow it, so it was not run",
			from.action.Name, step.GetAction()))
	}

	ref, owner := m.aboutOf(from, step, request)
	next := Request{
		Ref:     ref,
		Plugin:  owner,
		Action:  step.GetAction(),
		Params:  step.GetParams().AsMap(),
		Proof:   request.Proof,
		Actor:   request.Actor,
		Confirm: true,
	}

	target, err := m.resolve(ctx, next)
	if err != nil {
		// A composition that silently skips a step nobody serves is the failure
		// mode this whole mechanism exists to avoid.
		return refused(err.Error())
	}
	if !target.action.Enabled {
		return refused(notEnabled(target.action).Error())
	}

	outcome, err := m.run(ctx, *target, next, chain, depth+1)
	if err != nil {
		return refused(err.Error())
	}
	return *outcome
}

// Status polls an asynchronous invocation.
func (m *Manager) Status(ctx context.Context, id, handle string) (*Outcome, error) {
	m.mu.Lock()
	running, ok := m.running[id]
	m.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("plugin: %s is not running, so %s cannot be polled", id, handle)
	}
	if err := running.up(); err != nil {
		return nil, fmt.Errorf("%w, so %s cannot be polled and its outcome is not recoverable", err, handle)
	}

	answer, err := running.client.Status(ctx, &duskv1alpha1.StatusRequest{Handle: handle})
	if err != nil {
		return nil, fmt.Errorf("plugin: %s could not report on %s: %w", id, handle, err)
	}

	outcome := &Outcome{
		Plugin:  id,
		Handle:  handle,
		Done:    answer.GetDone(),
		OK:      answer.GetOk(),
		Message: answer.GetMessage(),
	}
	if detail := answer.GetDetail(); detail != nil {
		outcome.Detail = detail.AsMap()
	}
	return outcome, nil
}

func (m *Manager) reobserve(target chosen) {
	due, ok := m.Rota.(interface{ Due(name string) })
	if !ok {
		return
	}
	due.Due(target.scope())
}

// newID is crypto/rand.Text because it cannot fail, which keeps the no-panic
// rule without an error return on something that only names a thing.
func newID() string { return strings.ToLower(rand.Text()) }
