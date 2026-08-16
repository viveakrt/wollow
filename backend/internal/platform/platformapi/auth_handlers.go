package platformapi

import (
	"encoding/json"
	"net/http"
	"wollow/backend/internal/platform/httpx"

	"wollow/backend/internal/platform/auth"
)

type loginRequest struct {
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// One password guards the whole instance, so guesses are rationed before
	// the password is even read.
	key := auth.ClientKey(r)
	if !s.throttle.Allow(key) {
		w.Header().Set("Retry-After", "20")
		httpx.WriteError(w, http.StatusTooManyRequests, "too many login attempts — try again shortly")
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.Auth.CheckPassword(req.Password); err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid password")
		return
	}
	// A correct password clears whatever the typos before it cost.
	s.throttle.Reset(key)

	token, err := s.Auth.IssueToken()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to issue session")
		return
	}

	auth.SetSessionCookie(w, token, s.CookieSecure)
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	auth.ClearSessionCookie(w, s.CookieSecure)
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleHealth answers without touching auth so a container healthcheck or an
// uptime monitor can use it, but still proves the database is reachable —
// a process that is up with a broken database is not healthy.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.PingContext(r.Context()); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
