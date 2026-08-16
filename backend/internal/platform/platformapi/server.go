// Package platformapi serves the endpoints that belong to neither product:
// session auth and the shared AI provider configuration. Both Mail and Money
// sit behind the session this package issues.
package platformapi

import (
	"database/sql"
	"net/http"

	"wollow/backend/internal/platform/auth"
	"wollow/backend/internal/platform/crypto"
)

type Server struct {
	DB           *sql.DB
	Box          *crypto.Box
	Auth         *auth.Authenticator
	CookieSecure bool

	// throttle rations login attempts per source address.
	throttle *auth.Throttle
}

func NewServer(database *sql.DB, box *crypto.Box, authenticator *auth.Authenticator, cookieSecure bool) *Server {
	return &Server{
		DB:           database,
		Box:          box,
		Auth:         authenticator,
		CookieSecure: cookieSecure,
		throttle:     auth.NewThrottle(),
	}
}

// RegisterPublic mounts the routes that must be reachable without a session.
func (s *Server) RegisterPublic(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	// Health has to answer before anyone has logged in, for container
	// healthchecks and uptime monitors. It reveals nothing but liveness.
	mux.HandleFunc("GET /api/health", s.handleHealth)
}

// RegisterProtected mounts the routes that require a valid session.
func (s *Server) RegisterProtected(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
}
