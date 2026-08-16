// Command server runs Wollow: one binary serving both the Mail and Money
// products from a single SQLite file, behind a single session.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"wollow/backend/internal/mail"
	"wollow/backend/internal/mail/mailapi"
	"wollow/backend/internal/money/ingest"
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

	// This is the one place the two products are joined, and it is deliberately
	// here rather than in either product: Money borrows a mailbox connection
	// through an interface it defined, and Mail publishes a hook it knows
	// nothing about the other side of. Neither package imports the other.
	moneySrv := moneyapi.NewServer(database, box,
		func(ctx context.Context, accountID int64, fn func(ingest.RawFetcher) error) error {
			return mailSrv.WithProvider(ctx, accountID, func(p mail.Provider) error {
				return fn(p)
			})
		})

	// After every sync pass, read finance mail out of the index that pass just
	// refreshed — on the same connection, so one mailbox means one IMAP session.
	mailSrv.AfterSync = func(ctx context.Context, accountID int64, provider mail.Provider) error {
		result, err := ingest.RunWithPasswords(ctx, database, provider, accountID, "INBOX", moneySrv.PDFPasswordLookup())
		if err != nil {
			return err
		}
		if result.Transactions > 0 || result.Bills > 0 || result.Unrecognized > 0 {
			log.Printf("ingest: account %d scanned=%d transactions=%d bills=%d unrecognized=%d",
				accountID, result.Scanned, result.Transactions, result.Bills, result.Unrecognized)
		}
		return nil
	}

	// A cancellable context so the background sync stops when the process is
	// asked to shut down, rather than being killed mid-IMAP-transaction.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Keep the local message index warm in the background so the inbox is
	// current without the user having to hit "Sync now".
	go runPeriodicSync(ctx, mailSrv)

	server := &http.Server{
		Addr:    cfg.Addr,
		Handler: router(platformSrv, mailSrv, moneySrv, authenticator, cfg),
		// A client that opens a connection and sends nothing must not hold a
		// slot forever. WriteTimeout is generous because a statement import or
		// a large attachment download legitimately takes a while; there is no
		// overall read timeout for the same reason, only a header one.
		ReadHeaderTimeout: 15 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	if cfg.StaticDir != "" {
		log.Printf("wollow listening on %s — UI and API on one origin", cfg.Addr)
	} else {
		log.Printf("wollow listening on %s — API only", cfg.Addr)
	}

	serverErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		log.Fatalf("server error: %v", err)
	case <-ctx.Done():
		log.Print("shutting down — finishing in-flight requests")
	}

	// Give in-flight work a bounded chance to finish. SQLite writes are the
	// thing worth waiting for: a sync interrupted mid-batch is fine to redo,
	// but a half-written import is not.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown timed out: %v", err)
	}
}

// spaHandler serves a built single-page app: real files as themselves, and
// every other path as index.html so client-side routes like /money/transactions
// survive a refresh or a bookmark.
func spaHandler(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(clean); err == nil && !info.IsDir() {
			// Hashed asset filenames change on every build, so they are safe to
			// cache hard; index.html must never be, or a deploy goes unnoticed.
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, index)
	})
}

// router mounts login on a public mux and everything else — both products and
// the shared settings — behind the session middleware. A route that is not
// explicitly registered as public cannot be reached without a valid session.
func router(
	platformSrv *platformapi.Server,
	mailSrv *mailapi.Server,
	moneySrv *moneyapi.Server,
	authenticator *auth.Authenticator,
	cfg *config.Config,
) http.Handler {
	protected := http.NewServeMux()
	platformSrv.RegisterProtected(protected)
	mailSrv.Register(protected)
	moneySrv.Register(protected)

	mux := http.NewServeMux()
	platformSrv.RegisterPublic(mux)
	mux.Handle("/api/", authenticator.Middleware(protected))

	// "/api/" is a more specific pattern than "/", so the API keeps winning
	// regardless of registration order.
	if cfg.StaticDir != "" {
		mux.Handle("/", spaHandler(cfg.StaticDir))
	}

	return withRequestLog(withSecurityHeaders(withCORS(mux, cfg.AllowedOrigins)))
}

// withCORS answers cross-origin preflights for the origins the operator listed,
// and nobody else.
//
// It used to reflect whatever Origin the request carried while also sending
// Access-Control-Allow-Credentials: true — which tells the browser that *any*
// website may make authenticated calls to this instance and read the replies.
// For an app holding a mailbox and a transaction history that is the whole
// store. The default is now an empty list: every deployment in the README puts
// the UI on the same origin as the API, where CORS never applies.
func withCORS(next http.Handler, allowedOrigins []string) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
			// The response varies by Origin, so a shared cache must not serve
			// one origin's response to another.
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			// A preflight from a disallowed origin gets no CORS headers, which
			// is what makes the browser block the real request.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withSecurityHeaders sets the headers that cost nothing and close off whole
// classes of attack. The CSP is deliberately not applied to /api/ responses:
// the attachment endpoint sets its own, stricter one.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Cross-Origin-Opener-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			// The app is entirely self-hosted, so everything but images is
			// pinned to this origin — including scripts, which is why the
			// theme bootstrap is an external file rather than inline.
			//
			// img-src is the exception, and deliberately so. The message viewer
			// renders mail in a srcdoc iframe, and a srcdoc document *inherits*
			// its embedder's CSP: whatever policy the frame sets for itself can
			// only narrow this one, never widen it. Pinning images to 'self'
			// here would therefore make "Show images" silently do nothing, no
			// matter what the frame asked for. Remote images stay blocked by
			// the frame's own stricter policy until the reader opts in; this
			// line only stops the app from vetoing that choice.
			header.Set("Content-Security-Policy",
				"default-src 'self'; img-src 'self' data: blob: https: http:; "+
					"style-src 'self' 'unsafe-inline'; font-src 'self' data:; "+
					"frame-src 'self'; connect-src 'self'; "+
					"object-src 'none'; base-uri 'none'; form-action 'self'")
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response status for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// withRequestLog logs API requests that failed or were slow. Logging every
// successful request would bury the interesting ones under sync polling, which
// runs every few seconds per open tab.
func withRequestLog(next http.Handler) http.Handler {
	const slowRequest = 3 * time.Second

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)

		elapsed := time.Since(start)
		if recorder.status >= 400 || elapsed > slowRequest {
			log.Printf("%s %s -> %d in %s", r.Method, r.URL.Path, recorder.status, elapsed.Round(time.Millisecond))
		}
	})
}

// syncInterval is how often the background sync refreshes every account.
const syncInterval = 5 * time.Minute

func runPeriodicSync(ctx context.Context, server *mailapi.Server) {
	// Give the server a moment to come up before the first pass — but abandon
	// it outright if a shutdown arrives during that window.
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}
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
