package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
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

	// Accepted rather than processed: reconcile is not built yet, and the poll
	// floor in ADR-0006 means nothing is lost while it is missing.
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("accepted\n"))
}
