package githubapp

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// LowBudget is the fraction of an hour's requests below which an installation
// is worth warning about. Two thirds spent is early enough to act on and late
// enough not to fire on every busy sweep.
const LowBudget = 0.33

// RateLimit is what GitHub reported about the caller's budget on the last
// response. A zero value means no response has carried the headers yet, which
// is why Known exists rather than callers testing Limit for zero.
type RateLimit struct {
	Limit     int
	Remaining int
	Used      int
	Resource  string
	Reset     time.Time
	Known     bool
}

// Low reports whether the remaining budget has fallen below LowBudget. An
// unknown budget is not low: it is unknown, and warning about it would train
// the operator to ignore the warning.
func (r RateLimit) Low() bool {
	return r.Known && r.Limit > 0 && float64(r.Remaining)/float64(r.Limit) < LowBudget
}

// Exhausted reports whether the budget is spent.
func (r RateLimit) Exhausted() bool { return r.Known && r.Remaining <= 0 }

// String renders the budget for a log line.
func (r RateLimit) String() string {
	if !r.Known {
		return "unknown"
	}
	return fmt.Sprintf("%d/%d left, resets %s", r.Remaining, r.Limit, r.Reset.UTC().Format(time.RFC3339))
}

// readRateLimit parses the budget headers GitHub attaches to every response.
func readRateLimit(header http.Header) RateLimit {
	limit, ok := atoi(header.Get("X-RateLimit-Limit"))
	if !ok {
		return RateLimit{}
	}

	remaining, _ := atoi(header.Get("X-RateLimit-Remaining"))
	used, _ := atoi(header.Get("X-RateLimit-Used"))

	rate := RateLimit{
		Limit:     limit,
		Remaining: remaining,
		Used:      used,
		Resource:  header.Get("X-RateLimit-Resource"),
		Known:     true,
	}
	if reset, ok := atoi(header.Get("X-RateLimit-Reset")); ok {
		rate.Reset = time.Unix(int64(reset), 0)
	}
	return rate
}

func atoi(raw string) (int, bool) {
	value, err := strconv.Atoi(raw)
	return value, err == nil
}

// ErrRateLimited reports that GitHub refused the request for budget reasons
// rather than permission ones. Both arrive as 403, and telling them apart is
// the difference between "wait" and "your App cannot see this repository".
var ErrRateLimited = errors.New("github rate limit exceeded")

// RateLimitError carries when the budget returns, so a caller can decide
// between waiting and giving up instead of retrying into a wall.
type RateLimitError struct {
	Rate RateLimit

	// RetryAfter is GitHub's own backoff for a secondary limit, which has no
	// reset time and is not the hourly budget running out.
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s: retry after %s", ErrRateLimited, e.RetryAfter)
	}
	return fmt.Sprintf("%s: %s", ErrRateLimited, e.Rate)
}

// Is makes errors.Is(err, ErrRateLimited) match any budget refusal.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// Wait reports how long until the request could succeed.
func (e *RateLimitError) Wait(now time.Time) time.Duration {
	if e.RetryAfter > 0 {
		return e.RetryAfter
	}
	if e.Rate.Reset.IsZero() {
		return 0
	}
	return max(e.Rate.Reset.Sub(now), 0)
}

// rateLimited returns a *RateLimitError if resp is a refusal for budget
// reasons. GitHub signals three different ways and only one of them is 429.
func rateLimited(resp *http.Response) *RateLimitError {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}

	if after := resp.Header.Get("Retry-After"); after != "" {
		if seconds, ok := atoi(after); ok {
			return &RateLimitError{Rate: readRateLimit(resp.Header), RetryAfter: time.Duration(seconds) * time.Second}
		}
	}

	rate := readRateLimit(resp.Header)
	if rate.Exhausted() {
		return &RateLimitError{Rate: rate}
	}
	return nil
}
