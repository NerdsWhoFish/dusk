package githubapp_test

import (
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FetchHQ/dusk/pkg/githubapp"
)

// Exhaustion and "your App cannot see this" are both 403. Telling them apart is
// the difference between waiting and re-onboarding an installation.
func TestExhaustionIsDistinguishableFromDenial(t *testing.T) {
	reset := time.Now().Add(40 * time.Minute).Truncate(time.Second)

	tests := []struct {
		name    string
		status  int
		header  http.Header
		limited bool
		wait    time.Duration
	}{
		{
			name:   "the hourly budget is spent",
			status: http.StatusForbidden,
			header: http.Header{
				"X-Ratelimit-Limit":     {"5000"},
				"X-Ratelimit-Remaining": {"0"},
				"X-Ratelimit-Reset":     {strconv.FormatInt(reset.Unix(), 10)},
			},
			limited: true,
			wait:    40 * time.Minute,
		},
		{
			name:    "a secondary limit names its own backoff",
			status:  http.StatusForbidden,
			header:  http.Header{"Retry-After": {"60"}},
			limited: true,
			wait:    time.Minute,
		},
		{
			name:    "too many requests",
			status:  http.StatusTooManyRequests,
			header:  http.Header{"Retry-After": {"1"}},
			limited: true,
			wait:    time.Second,
		},
		{
			name:   "a denial with budget to spare is not a rate limit",
			status: http.StatusForbidden,
			header: http.Header{
				"X-Ratelimit-Limit":     {"5000"},
				"X-Ratelimit-Remaining": {"4993"},
			},
			limited: false,
		},
		{
			name:    "not found is not a rate limit",
			status:  http.StatusNotFound,
			header:  http.Header{},
			limited: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := readThrough(t, tt.status, tt.header)

			if got := errors.Is(err, githubapp.ErrRateLimited); got != tt.limited {
				t.Fatalf("errors.Is(err, ErrRateLimited) = %v, want %v (err = %v)", got, tt.limited, err)
			}
			if !tt.limited {
				return
			}

			var limited *githubapp.RateLimitError
			if !errors.As(err, &limited) {
				t.Fatalf("errors.As did not yield a *RateLimitError from %v", err)
			}
			if got := limited.Wait(time.Now()).Round(time.Minute); got != tt.wait.Round(time.Minute) {
				t.Errorf("Wait = %s, want %s", got, tt.wait)
			}
		})
	}
}

// The budget is on every response, so it should never need a request of its own
// to find out. Spending a request to ask how many are left would be absurd.
func TestTheBudgetIsObservedWithoutAskingForIt(t *testing.T) {
	header := http.Header{
		"X-Ratelimit-Limit":     {"5000"},
		"X-Ratelimit-Remaining": {"4321"},
		"X-Ratelimit-Used":      {"679"},
		"X-Ratelimit-Resource":  {"core"},
	}
	client, repo := clientServing(t, http.StatusOK, header, `{"sha":"a866a20"}`)

	if known := client.RateLimit().Known; known {
		t.Error("a budget was reported before any request was made")
	}
	if _, err := repo.Resolve(t.Context(), "refs/heads/main"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	rate := client.RateLimit()
	if !rate.Known {
		t.Fatal("no budget was recorded from a successful response")
	}
	if rate.Remaining != 4321 || rate.Limit != 5000 || rate.Used != 679 {
		t.Errorf("rate = %+v, want 4321/5000 used 679", rate)
	}
	if rate.Low() {
		t.Error("4321 of 5000 was reported as low")
	}
}

func TestLowBudget(t *testing.T) {
	tests := []struct {
		name string
		rate githubapp.RateLimit
		low  bool
	}{
		{"plenty", githubapp.RateLimit{Limit: 5000, Remaining: 4000, Known: true}, false},
		{"under a third", githubapp.RateLimit{Limit: 5000, Remaining: 1000, Known: true}, true},
		{"spent", githubapp.RateLimit{Limit: 5000, Remaining: 0, Known: true}, true},
		// An unknown budget must not warn, or the warning gets ignored.
		{"never seen", githubapp.RateLimit{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rate.Low(); got != tt.low {
				t.Errorf("Low() = %v, want %v", got, tt.low)
			}
		})
	}
}

func readThrough(t *testing.T, status int, header http.Header) error {
	t.Helper()
	_, repo := clientServing(t, status, header, `{"message":"nope"}`)
	_, err := repo.Resolve(t.Context(), "refs/heads/main")
	if err == nil && status != http.StatusOK {
		t.Fatal("a failing status produced no error")
	}
	return err
}

func clientServing(t *testing.T, status int, header http.Header, body string) (*githubapp.Client, *githubapp.Repository) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Minting a token is a request like any other and must not carry the
		// headers under test, or the budget would be read from the wrong one.
		if strings.Contains(req.URL.Path, "/access_tokens") {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"ghs_secret","expires_at":"2099-01-01T00:00:00Z"}`))
			return
		}

		maps.Copy(w.Header(), header)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	client := &githubapp.Client{BaseURL: server.URL}
	install := &githubapp.Install{
		Client: client,
		Tokens: &githubapp.Tokens{Client: client, App: pkcs1App(t), Now: time.Now},
		ID:     10,
	}
	return client, install.Repository("example", "homelab")
}
