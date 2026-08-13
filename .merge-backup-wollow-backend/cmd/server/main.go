package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"wollow/backend/internal/api"
	"wollow/backend/internal/auth"
	"wollow/backend/internal/config"
	"wollow/backend/internal/crypto"
	"wollow/backend/internal/db"
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

	cookieSecure := os.Getenv("WOLLOW_COOKIE_SECURE") == "true"
	server := api.NewServer(database, box, authenticator, cookieSecure)

	// Keep the local message index warm in the background so the inbox is
	// current without the user having to hit "Sync now".
	go runPeriodicSync(context.Background(), server)

	log.Printf("wollow backend listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, server.Router()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// syncInterval is how often the background sync refreshes every account.
const syncInterval = 5 * time.Minute

func runPeriodicSync(ctx context.Context, server *api.Server) {
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
