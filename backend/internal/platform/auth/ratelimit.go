package auth

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Login throttling.
//
// One password protects the whole instance, both products and every stored
// credential, so an unthrottled login endpoint is the instance's weakest point:
// a few thousand requests a second against a human-chosen password is not a
// long wait. This makes each source address pay for its guesses.

const (
	// loginBurst is how many attempts an address may make before throttling
	// starts. Generous enough that a real person mistyping their password
	// never notices.
	loginBurst = 5
	// loginRefill is how long it takes to earn one attempt back.
	loginRefill = 20 * time.Second
	// loginWindow is how long an idle address is remembered.
	loginWindow = 30 * time.Minute
)

type attemptRecord struct {
	tokens   float64
	lastSeen time.Time
}

// Throttle is a per-address token bucket over login attempts.
type Throttle struct {
	mu      sync.Mutex
	records map[string]*attemptRecord
	// now is injectable so the tests don't have to sleep.
	now func() time.Time
}

func NewThrottle() *Throttle {
	return &Throttle{records: map[string]*attemptRecord{}, now: time.Now}
}

// Allow reports whether an attempt from key may proceed, consuming one token
// when it may.
func (t *Throttle) Allow(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.evictLocked(now)

	record, ok := t.records[key]
	if !ok {
		t.records[key] = &attemptRecord{tokens: loginBurst - 1, lastSeen: now}
		return true
	}

	record.tokens = min(loginBurst, record.tokens+now.Sub(record.lastSeen).Seconds()/loginRefill.Seconds())
	record.lastSeen = now
	if record.tokens < 1 {
		return false
	}
	record.tokens--
	return true
}

// Reset forgets an address's attempts, called after a successful login so a
// correct password immediately clears whatever the typos cost.
func (t *Throttle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.records, key)
}

// evictLocked drops addresses idle past the window, so the map can't grow
// without bound under a distributed attack.
func (t *Throttle) evictLocked(now time.Time) {
	for key, record := range t.records {
		if now.Sub(record.lastSeen) > loginWindow {
			delete(t.records, key)
		}
	}
}

// ClientKey identifies the caller for throttling purposes.
//
// It uses the socket's own address, never X-Forwarded-For: a header the client
// controls would let an attacker mint a fresh identity per request and defeat
// the throttle entirely. Behind a reverse proxy every attempt therefore shares
// the proxy's address, which for a single-user instance is the right trade.
func ClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
