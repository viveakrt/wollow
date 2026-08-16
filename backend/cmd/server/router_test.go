package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// The default posture is same-origin only. Reflecting an arbitrary Origin
// alongside Allow-Credentials would let any website on the internet make
// authenticated calls to this instance and read the replies — mailbox and
// transaction history included.
func TestCORSDoesNotReflectUnknownOrigins(t *testing.T) {
	handler := withCORS(okHandler(), nil)

	r := httptest.NewRequest("GET", "/api/mail/accounts", nil)
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want it unset", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want it unset", got)
	}
}

func TestCORSAllowsConfiguredOrigins(t *testing.T) {
	handler := withCORS(okHandler(), []string{"https://wollow.example.com"})

	r := httptest.NewRequest("GET", "/api/mail/accounts", nil)
	r.Header.Set("Origin", "https://wollow.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://wollow.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the configured origin", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want %q", got, "true")
	}
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q — a cache must not cross origins", got, "Origin")
	}
}

// A preflight from a disallowed origin still gets an answer; what it does not
// get is the headers that would let the browser proceed.
func TestCORSPreflightFromUnknownOriginCarriesNoGrant(t *testing.T) {
	handler := withCORS(okHandler(), []string{"https://wollow.example.com"})

	r := httptest.NewRequest("OPTIONS", "/api/mail/accounts", nil)
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want it unset", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := withSecurityHeaders(okHandler())

	t.Run("app pages carry a CSP", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/money/accounts", nil))

		policy := w.Header().Get("Content-Security-Policy")
		if policy == "" {
			t.Fatal("no Content-Security-Policy on an app page")
		}
		// A srcdoc iframe inherits its embedder's CSP, so the message viewer's
		// own policy can only narrow this one. Pinning images to 'self' here
		// makes "Show images" silently do nothing.
		if !strings.Contains(policy, "img-src") || !strings.Contains(policy, "https:") {
			t.Errorf("img-src does not permit remote images: %q", policy)
		}
		// Scripts stay pinned; the theme bootstrap is an external file for
		// exactly this reason.
		if strings.Contains(policy, "'unsafe-inline'") && !strings.Contains(policy, "style-src 'self' 'unsafe-inline'") {
			t.Errorf("unexpected 'unsafe-inline' outside style-src: %q", policy)
		}
		if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("X-Frame-Options = %q, want DENY", got)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
		}
	})

	// The attachment endpoint sets its own, far stricter policy; a blanket one
	// here would override it with a looser rule.
	t.Run("API responses set their own policy", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/mail/accounts", nil))

		if got := w.Header().Get("Content-Security-Policy"); got != "" {
			t.Errorf("Content-Security-Policy = %q on an API route, want it left to the handler", got)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
		}
	})
}
