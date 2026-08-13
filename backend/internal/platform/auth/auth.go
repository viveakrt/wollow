// Package auth implements single-user session auth: one admin password
// (set via env) protects the whole API, sessions are JWTs in an HttpOnly cookie.
package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const CookieName = "wollow_session"
const sessionTTL = 7 * 24 * time.Hour

var ErrInvalidPassword = errors.New("invalid password")

type Authenticator struct {
	adminPassword string
	jwtSecret     []byte
}

func New(adminPassword, jwtSecret string) *Authenticator {
	return &Authenticator{adminPassword: adminPassword, jwtSecret: []byte(jwtSecret)}
}

func (a *Authenticator) CheckPassword(password string) error {
	if subtle.ConstantTimeCompare([]byte(password), []byte(a.adminPassword)) != 1 {
		return ErrInvalidPassword
	}
	return nil
}

func (a *Authenticator) IssueToken() (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   "admin",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(sessionTTL)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.jwtSecret)
}

func (a *Authenticator) VerifyToken(tokenStr string) error {
	_, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(t *jwt.Token) (interface{}, error) {
		return a.jwtSecret, nil
	})
	return err
}

// Middleware protects handlers behind a valid session cookie.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil || a.VerifyToken(cookie.Value) != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func SetSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}
