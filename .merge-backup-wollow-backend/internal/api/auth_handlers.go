package api

import (
	"encoding/json"
	"net/http"

	"wollow/backend/internal/auth"
)

type loginRequest struct {
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.Auth.CheckPassword(req.Password); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	token, err := s.Auth.IssueToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue session")
		return
	}

	auth.SetSessionCookie(w, token, s.CookieSecure)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	auth.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
