// Command server runs Wollow: one binary serving both the Mail and Money
// products from a single SQLite file, behind a single session.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"wollow/backend/internal/mail/mailapi"
	"wollow/backend/internal/money/moneyapi"
	"wollow/backend/internal/platform/auth"
	"wollow/backend/internal/platform/config"
	"wollow/backend/internal/platform/crypto"
	"wollow/backend/internal/platform/db"
	"wollow/backend/internal/platform/platformapi"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("failed to create data dir: %v", err)
	}

	database, err := db.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	box, err := crypto.New(cfg.MasterKeyHex)
	if err != nil {
		log.Fatalf("failed to init crypto: %v", err)
	}

	authenticator := auth.New(cfg.AdminPassword, cfg.JWTSecret)

	platformSrv := platformapi.NewServer(database, box, authenticator, cfg.CookieSecure)
	mailSrv := mailapi.NewServer(database, box)
	moneySrv := moneyapi.NewServer(database, box)

	// Keep the local message index warm in the background so the inbox is
	// current without the user having to hit "Sync now".
	go runPeriodicSync(context.Background(), mailSrv)

	log.Printf("wollow listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, router(platformSrv, mailSrv, moneySrv, authenticator)); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// router mounts login on a public mux and everything else — both products and
// the shared settings — behind the session middleware. A route that is not
// explicitly registered as public cannot be reached without a valid session.
func router(
	platformSrv *platformapi.Server,
	mailSrv *mailapi.Server,
	moneySrv *moneyapi.Server,
	authenticator *auth.Authenticator,
) http.Handler {
	protected := http.NewServeMux()
	platformSrv.RegisterProtected(protected)
	mailSrv.Register(protected)
	moneySrv.Register(protected)

	mux := http.NewServeMux()
	platformSrv.RegisterPublic(mux)
	mux.Handle("/api/", authenticator.Middleware(protected))

	return withCORS(mux)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// syncInterval is how often the background sync refreshes every account.
const syncInterval = 5 * time.Minute

func runPeriodicSync(ctx context.Context, server *mailapi.Server) {
	// Give the server a moment to come up before the first pass.
	time.Sleep(5 * time.Second)
	server.SyncAllAccounts(ctx)

	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			server.SyncAllAccounts(ctx)
		}
	}
}
