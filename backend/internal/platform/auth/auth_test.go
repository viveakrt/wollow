package auth

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifyTokenAcceptsItsOwnTokens(t *testing.T) {
	a := New("hunter2", "a-secret-long-enough-for-real-use-32")
	token, err := a.IssueToken()
	if err != nil {
		t.Fatalf("issuing token: %v", err)
	}
	if err := a.VerifyToken(token); err != nil {
		t.Fatalf("token issued here did not verify: %v", err)
	}
}

func TestVerifyTokenRejectsOtherSecrets(t *testing.T) {
	issuer := New("hunter2", "a-secret-long-enough-for-real-use-32")
	verifier := New("hunter2", "a-different-secret-of-adequate-len32")

	token, err := issuer.IssueToken()
	if err != nil {
		t.Fatalf("issuing token: %v", err)
	}
	if err := verifier.VerifyToken(token); err == nil {
		t.Fatal("a token signed with another secret verified")
	}
}

// A token must not get to choose how it is verified. Without pinned methods
// the parser honours the token's own alg header, which is the classic JWT
// confusion bug.
func TestVerifyTokenRejectsUnsignedTokens(t *testing.T) {
	a := New("hunter2", "a-secret-long-enough-for-real-use-32")

	claims := jwt.RegisteredClaims{
		Subject:   "admin",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("building alg=none token: %v", err)
	}

	if err := a.VerifyToken(unsigned); err == nil {
		t.Fatal("an alg=none token was accepted")
	}
}

func TestVerifyTokenRejectsExpiredTokens(t *testing.T) {
	a := New("hunter2", "a-secret-long-enough-for-real-use-32")

	claims := jwt.RegisteredClaims{
		Subject:   "admin",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
	}
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
		SignedString([]byte("a-secret-long-enough-for-real-use-32"))
	if err != nil {
		t.Fatalf("building expired token: %v", err)
	}

	if err := a.VerifyToken(expired); err == nil {
		t.Fatal("an expired token was accepted")
	}
}

func TestThrottleAllowsABurstThenStops(t *testing.T) {
	now := time.Now()
	throttle := NewThrottle()
	throttle.now = func() time.Time { return now }

	for i := 0; i < loginBurst; i++ {
		if !throttle.Allow("1.2.3.4") {
			t.Fatalf("attempt %d was throttled inside the burst allowance", i+1)
		}
	}
	if throttle.Allow("1.2.3.4") {
		t.Fatal("attempts past the burst allowance were not throttled")
	}

	// A different address is unaffected.
	if !throttle.Allow("5.6.7.8") {
		t.Fatal("one address's attempts throttled another's")
	}

	// Waiting earns attempts back.
	now = now.Add(loginRefill)
	if !throttle.Allow("1.2.3.4") {
		t.Fatal("no attempt was refilled after waiting a full refill period")
	}
}

func TestThrottleResetClearsAnAddress(t *testing.T) {
	throttle := NewThrottle()
	for i := 0; i < loginBurst+3; i++ {
		throttle.Allow("1.2.3.4")
	}
	throttle.Reset("1.2.3.4")
	if !throttle.Allow("1.2.3.4") {
		t.Fatal("a successful login did not clear the address's earlier failures")
	}
}

// X-Forwarded-For is attacker-controlled; keying on it would let one client
// mint a fresh identity per request and never be throttled at all.
func TestClientKeyIgnoresForwardedHeaders(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "203.0.113.9:54321"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")

	if got := ClientKey(r); got != "203.0.113.9" {
		t.Errorf("ClientKey = %q, want %q", got, "203.0.113.9")
	}
}
