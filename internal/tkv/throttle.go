package tkv

import (
	"sync"
	"time"
)

const (
	backoffBase = 250 * time.Millisecond
	backoffCap  = 5 * time.Second
	// inFlightRetry is the Retry-After hint while another attempt is hashing.
	inFlightRetry = time.Second
)

// loginThrottle is one global failed-login counter. It keys on nothing the
// client sends, so its state is constant-size, it says nothing about which
// usernames exist, and rotating source addresses gains nothing.
type loginThrottle struct {
	now func() time.Time

	mu       sync.Mutex
	failures int
	inFlight bool
	until    time.Time
}

func newLoginThrottle(now func() time.Time) *loginThrottle {
	return &loginThrottle{now: now}
}

// backoff is the wait after the n-th consecutive failure: min(250ms*2^(n-1), 5s).
func backoff(n int) time.Duration {
	if n <= 0 {
		return 0
	}
	if n > 6 {
		return backoffCap
	}
	return min(backoffBase<<(n-1), backoffCap)
}

// admit reserves the one verification slot. An admitted attempt counts as a
// failure until finish says otherwise, so a concurrent attempt is refused
// without hashing and at most one PBKDF2 derivation runs at a time. wait is
// the hint for a refused attempt.
func (t *loginThrottle) admit() (ok bool, wait time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inFlight {
		return false, inFlightRetry
	}
	if now := t.now(); now.Before(t.until) {
		return false, t.until.Sub(now)
	}
	t.inFlight = true
	t.failures++
	return true, 0
}

// finish releases the slot. A success resets the counter; a failure starts
// the backoff from now.
func (t *loginThrottle) finish(success bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inFlight = false
	if success {
		t.failures = 0
		t.until = time.Time{}
		return
	}
	t.until = t.now().Add(backoff(t.failures))
}
