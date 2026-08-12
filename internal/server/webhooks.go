package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/FetchHQ/dusk/internal/controller"
)

// maxWebhookBody caps what is read before signature checking, so an unverified
// caller cannot make Dusk buffer an arbitrary amount of memory.
const maxWebhookBody = 25 << 20

// deliveryMemory is how long a delivery id is remembered for replay rejection.
// GitHub retries genuine failures within minutes, so an hour is generous.
const deliveryMemory = time.Hour

// seenDeliveries rejects repeated delivery ids. In memory on purpose: a
// restart forgets, and re-processing is safe because reconcile is idempotent.
type seenDeliveries struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newSeenDeliveries() *seenDeliveries {
	return &seenDeliveries{seen: make(map[string]time.Time)}
}

// observe records id and reports whether it had already been seen.
func (s *seenDeliveries) observe(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, at := range s.seen {
		if now.Sub(at) > deliveryMemory {
			delete(s.seen, k)
		}
	}
	if _, dup := s.seen[id]; dup {
		return true
	}
	s.seen[id] = now
	return false
}

// verifySignature checks GitHub's X-Hub-Signature-256 header.
func verifySignature(header string, body, secret []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) || len(secret) == 0 {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	creds, err := s.credentials.Load()
	if err != nil {
		// Deliveries before onboarding are not an error worth retrying.
		http.Error(w, "not onboarded", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "could not read body", http.StatusBadRequest)
		return
	}

	// The forwarder in front of Dusk deliberately does not verify signatures,
	// so this is the only thing standing between the internet and the catalog.
	if !verifySignature(r.Header.Get("X-Hub-Signature-256"), body, []byte(creds.WebhookSecret.Reveal())) {
		s.log.Warn("webhook rejected: bad signature",
			"event", r.Header.Get("X-GitHub-Event"),
			"delivery", r.Header.Get("X-GitHub-Delivery"),
			"remote", r.RemoteAddr)
		http.Error(w, "signature does not match", http.StatusUnauthorized)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	delivery := r.Header.Get("X-GitHub-Delivery")

	if delivery == "" {
		http.Error(w, "missing delivery id", http.StatusBadRequest)
		return
	}
	if s.deliveries.observe(delivery, s.now()) {
		s.log.Info("webhook ignored: already delivered", "event", event, "delivery", delivery)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("duplicate\n"))
		return
	}

	if event == "ping" {
		s.log.Info("webhook ping", "delivery", delivery)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong\n"))
		return
	}

	s.log.Info("webhook accepted", "event", event, "delivery", delivery, "bytes", len(body))
	s.dispatch(r.Context(), event, delivery, body)

	// Accepted rather than done: GitHub gets a prompt answer and the reconcile
	// runs behind it, with the poll floor as the safety net if it fails.
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("accepted\n"))
}

// delivery is the slice of each payload the reconcile path needs. GitHub sends
// a great deal more, and none of it is trusted beyond these fields.
type deliveryPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
	Installation struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	} `json:"installation"`

	Action      string `json:"action"`
	PullRequest struct {
		Number int `json:"number"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
	} `json:"pull_request"`

	Created bool `json:"created"`
	Deleted bool `json:"deleted"`
	Forced  bool `json:"forced"`
	Commits []struct {
		Added    []string `json:"added"`
		Removed  []string `json:"removed"`
		Modified []string `json:"modified"`
	} `json:"commits"`
}

// pushCommitLimit is how many commits GitHub includes in a push payload. Past
// it the list is truncated with no flag saying so, so the count is the flag.
const pushCommitLimit = 20

// touched returns the files this push changed, or nil when the payload cannot
// be trusted to list them all. Nil means "look anyway"; an empty slice means
// "nothing changed", and only the second lets a caller skip the read.
func (p deliveryPayload) touched() []string {
	if p.Created || p.Deleted || p.Forced || len(p.Commits) == 0 || len(p.Commits) >= pushCommitLimit {
		return nil
	}

	seen := map[string]bool{}
	var files []string
	for _, commit := range p.Commits {
		for _, group := range [][]string{commit.Added, commit.Removed, commit.Modified} {
			for _, file := range group {
				if !seen[file] {
					seen[file] = true
					files = append(files, file)
				}
			}
		}
	}
	return files
}

// dispatch turns an accepted delivery into work, off the request goroutine so
// GitHub is not kept waiting on a reconcile.
func (s *Server) dispatch(ctx context.Context, event, delivery string, body []byte) {
	if s.controller == nil {
		return
	}

	var payload deliveryPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		s.log.Error("webhook payload could not be read", "event", event, "delivery", delivery, "error", err)
		return
	}

	switch event {
	case "push":
		s.reconcileRepository(ctx, delivery, payload)
	case "pull_request":
		s.previewPullRequest(ctx, delivery, payload)
	case "installation", "installation_repositories":
		go s.sweep(ctx, delivery)
	default:
		s.log.Info("webhook ignored: nothing to do for this event", "event", event, "delivery", delivery)
	}
}

func (s *Server) reconcileRepository(ctx context.Context, delivery string, payload deliveryPayload) {
	owner := payload.Repository.Owner.Login
	name := payload.Repository.Name
	if owner == "" || name == "" || payload.Ref == "" || payload.Installation.ID == 0 {
		s.log.Error("push delivery is missing what a reconcile needs", "delivery", delivery)
		return
	}

	// The account is taken from the installation rather than the repository, so
	// the allowlist is checked against who Dusk trusts rather than who pushed.
	account := payload.Installation.Account.Login
	if account == "" {
		account = owner
	}

	work := controller.Push{
		InstallationID: payload.Installation.ID,
		Account:        account,
		Owner:          owner,
		Name:           name,
		GitRef:         payload.Ref,
		Files:          payload.touched(),
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reconcileTimeout)
		defer cancel()
		if err := s.controller.SyncPush(ctx, work); err != nil {
			s.log.Error("reconcile from delivery failed", "delivery", delivery, "error", err)
		}
	}()
}

// previewsFor are the pull request actions worth acting on. Everything else,
// a label or an assignee changing, cannot alter what the catalog would say.
var previewsFor = map[string]bool{
	"opened": true, "reopened": true, "synchronize": true,
	"closed": true,
}

func (s *Server) previewPullRequest(ctx context.Context, delivery string, payload deliveryPayload) {
	if !previewsFor[payload.Action] {
		return
	}

	owner := payload.Repository.Owner.Login
	name := payload.Repository.Name
	if owner == "" || name == "" || payload.PullRequest.Number == 0 || payload.Installation.ID == 0 {
		s.log.Error("pull request delivery is missing what a preview needs", "delivery", delivery)
		return
	}

	account := payload.Installation.Account.Login
	if account == "" {
		account = owner
	}

	preview := controller.Preview{
		InstallationID: payload.Installation.ID,
		Account:        account,
		Owner:          owner,
		Name:           name,
		Number:         payload.PullRequest.Number,
		Head:           payload.PullRequest.Head.SHA,
		Closed:         payload.Action == "closed",
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reconcileTimeout)
		defer cancel()
		if err := s.controller.SyncPreview(ctx, preview); err != nil {
			s.log.Error("preview from delivery failed", "delivery", delivery, "error", err)
		}
	}()
}

func (s *Server) sweep(ctx context.Context, delivery string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sweepTimeout)
	defer cancel()
	if err := s.controller.Sync(ctx); err != nil {
		s.log.Error("sweep from delivery failed", "delivery", delivery, "error", err)
	}
}

const (
	reconcileTimeout = 2 * time.Minute
	sweepTimeout     = 15 * time.Minute
)
