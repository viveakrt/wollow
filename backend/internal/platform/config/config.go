// Package config loads runtime configuration from the environment. Every
// variable is WOLLOW_-prefixed; the MOOLIQ_* variables the finance side used
// before the merge (MOOLIQ_DB_PATH, MOOLIQ_ADDR) are retired — there is one
// database and one listener now.
package config

import (
	"fmt"
	"os"
	"strings"
)

// minSecretLength is the shortest JWT signing secret accepted. A short secret
// is brute-forceable offline from a single captured session cookie, which
// yields a permanent forged login.
const minSecretLength = 32

type Config struct {
	Addr          string
	DataDir       string
	MasterKeyHex  string
	AdminPassword string
	JWTSecret     string
	CookieSecure  bool
	// StaticDir is a built frontend to serve from the same listener as the API.
	// Set it and one process on one port is the whole app — no second dev
	// server, no reverse proxy, and the session cookie is same-origin by
	// construction. Empty means API only (the Docker image serves the UI
	// through nginx instead).
	StaticDir string
	// AllowedOrigins are the browser origins permitted to make credentialed
	// cross-origin API calls. Empty — the default, and correct for every
	// deployment described in the README — means same-origin only.
	AllowedOrigins []string
}

func Load() (*Config, error) {
	cfg := &Config{
		Addr:           getEnv("WOLLOW_ADDR", ":8080"),
		DataDir:        getEnv("WOLLOW_DATA_DIR", "/data"),
		MasterKeyHex:   os.Getenv("WOLLOW_MASTER_KEY"),
		AdminPassword:  os.Getenv("WOLLOW_ADMIN_PASSWORD"),
		JWTSecret:      os.Getenv("WOLLOW_JWT_SECRET"),
		CookieSecure:   os.Getenv("WOLLOW_COOKIE_SECURE") == "true",
		StaticDir:      os.Getenv("WOLLOW_STATIC_DIR"),
		AllowedOrigins: splitList(os.Getenv("WOLLOW_ALLOWED_ORIGINS")),
	}

	if cfg.MasterKeyHex == "" {
		return nil, fmt.Errorf("WOLLOW_MASTER_KEY is required (generate with: openssl rand -hex 32)")
	}
	if cfg.AdminPassword == "" {
		return nil, fmt.Errorf("WOLLOW_ADMIN_PASSWORD is required")
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("WOLLOW_JWT_SECRET is required (generate with: openssl rand -hex 32)")
	}
	if len(cfg.JWTSecret) < minSecretLength {
		return nil, fmt.Errorf(
			"WOLLOW_JWT_SECRET is too short (%d chars, need at least %d) — generate with: openssl rand -hex 32",
			len(cfg.JWTSecret), minSecretLength)
	}

	for _, origin := range cfg.AllowedOrigins {
		if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
			return nil, fmt.Errorf(
				"WOLLOW_ALLOWED_ORIGINS entry %q must be a full origin, e.g. https://wollow.example.com", origin)
		}
		if strings.HasSuffix(origin, "/") {
			return nil, fmt.Errorf(
				"WOLLOW_ALLOWED_ORIGINS entry %q must not end with a slash — an Origin header never does", origin)
		}
	}

	return cfg, nil
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
