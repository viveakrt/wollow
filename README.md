# Wollow

Open-source, self-hosted email **and** personal finance, under one roof.
Connect your own mailboxes, plug in your own AI API key, and get both an
AI-organized inbox and a finance tracker that reads your bank and card mail —
without handing either your mailbox or your transaction history to a
third-party SaaS.

Two products, one platform:

- **Mail** — connect mailboxes over IMAP, read/delete/flag, AI classification
  into smart views (Needs Action, Bills, Finance, Security, …), on-demand
  summaries.
- **Money** — accounts, transactions, categories, bills, and cross-account
  transfer matching. Fed by bank statement imports and by the bank/card alert
  mail already sitting in your inbox.

They share a login, a database, an AI provider config, and — the part that
matters — a single mail sync. Money doesn't open its own IMAP connection; it
reads the same message index Mail builds.

## Status

Early but real. Mail syncs and classifies; Money imports HDFC statements and
parses HDFC/Axis alert mail into transactions plus ICICI/BOBCARD/HDFC Diners
statement mail into bill reminders. Google/Microsoft OAuth, mobile, and desktop
targets are on the roadmap.

## Architecture

- **`backend/`** — Go server, one binary, one port. Owns mailbox credentials,
  talks IMAP directly, proxies AI provider calls. Nothing sensitive reaches the
  browser or any third party except the mail server and the AI provider you
  configure.
- **`frontend/`** — React + TypeScript (Vite, TanStack Query, Tailwind).
- Storage is a single SQLite file. IMAP passwords, AI API keys, and issuer PDF
  passwords are all encrypted at rest with AES-GCM using a key you control.

```
backend/internal/
  platform/     config · auth · crypto · db · httpx · platformapi
  mail/         provider · imap · sync · ai · classifier · mailapi
  money/        parsers · emailparse · pdfparse · models · moneyapi

frontend/src/
  platform/     apiClient · auth · theme · queryClient · AppShell
  products/mail/    Inbox · MessageDetail · ConnectAccount · MailSidebar
  products/money/   Dashboard · Accounts · Transactions · Bills · Import · Transfers
```

`platform/` is shared; the two product trees never import each other. The shell
is a product rail pinned to the left — switching between Mail and Money is a
route change inside one React tree, so the session, query cache, and theme all
survive it.

### Theming

One token set in `frontend/src/index.css` drives both products, in three
states: light (the bare `@theme` block), explicit dark (`[data-theme="dark"]`),
and system (no stamp — `prefers-color-scheme` decides). Components style
through `var(--color-*)` and never hardcode a colour scale, so a new product
inherits both themes for free. The rail's toggle cycles system → light → dark.

### One sync, many consumers

Mail's sync pass indexes message headers and a short snippet into `messages`,
reconciles server-side deletions, and runs every five minutes. Message bodies
are never stored — they stay live-fetched from IMAP on open.

Money reads that index rather than connecting to IMAP itself, so it inherits
background sync, delete reconciliation, and multi-account support for free.
The AI classifier's `is_transactional` flag is what surfaces finance mail from
issuers the regex parsers have never seen.

### A note on table names

`mail_accounts` is a mailbox. `finance_accounts` is a bank or card account.
Both are `INTEGER PRIMARY KEY`, so a crossed foreign key would silently attach
transactions to a mailbox with no error raised. **Do not reintroduce a bare
`accounts` table.** See `backend/internal/platform/db/schema.sql`.

## Running it (Docker Compose)

1. Copy `.env.example` to `.env` and fill it in:
   ```
   cp .env.example .env
   openssl rand -hex 32   # WOLLOW_MASTER_KEY
   openssl rand -hex 32   # WOLLOW_JWT_SECRET
   ```
   Set `WOLLOW_ADMIN_PASSWORD` to whatever you want to log in with.
2. `docker compose up --build`
3. Open `http://localhost:8081` (or your `WOLLOW_PORT`), log in, connect a
   mailbox.

For Gmail/Outlook today, use an **app password** (not your normal login) as the
IMAP password — native OAuth is on the roadmap. Gmail app passwords:
[myaccount.google.com/apppasswords](https://myaccount.google.com/apppasswords)
(requires 2-Step Verification).

The frontend container's nginx reverse-proxies `/api` to the backend, so the
browser only ever talks to one origin and the session cookie works without CORS
complications.

## Local development

Backend:
```
cd backend
export WOLLOW_MASTER_KEY=$(openssl rand -hex 32)
export WOLLOW_JWT_SECRET=$(openssl rand -hex 32)
export WOLLOW_ADMIN_PASSWORD=devpassword
export WOLLOW_DATA_DIR=./data
go run ./cmd/server
```

Frontend:
```
cd frontend
npm install
npm run dev
```
One dev server serves both products. It proxies `/api` to
`http://localhost:8080`, so it talks to a local backend the same same-origin way
the production nginx proxy does.

Tests:
```
cd backend  && go test ./...      # parsers, IMAP, email → transaction pipeline
cd frontend && npm run lint
cd frontend && npm run test:e2e   # browser smoke test — see frontend/e2e/README.md
```

The Go suite includes an integration test that replays the real sample emails in
`statements/` through the full parse → persist path, so schema changes that break
transaction or bill extraction fail the build.

## API surface

| Prefix | Auth | Owns |
| --- | --- | --- |
| `/api/auth/*` | public | login, logout |
| `/api/settings` | session | AI provider, model, base URL (shared) |
| `/api/mail/*` | session | mailboxes, messages, sync, classify, insights |
| `/api/money/*` | session | finance accounts, transactions, categories, bills, import, transfers |

Every route that is not explicitly registered as public requires a valid
session cookie.

## Roadmap

- Money ingest reading the shared message index (removing its last IMAP call —
  today it still opens its own connection, using the same stored credentials)
- Cross-product links: message → transaction, transaction → source email
- Google & Microsoft OAuth (no app passwords needed)
- More bank/issuer parsers; itemized PDF statement extraction
- Native iOS, Android, and Windows desktop apps
- Rules & filters, AI-drafted replies
- Multi-user accounts

## Security notes

- Single-user mode: one admin password protects the whole instance, both
  products. Put it behind HTTPS (Caddy/Traefik) if you expose it beyond your
  local network, and set `WOLLOW_COOKIE_SECURE=true` once you do.
- `WOLLOW_MASTER_KEY` encrypts stored IMAP passwords, AI API keys, and issuer
  PDF passwords. Losing it means reconnecting accounts and re-entering keys;
  leaking it means those secrets are recoverable. Treat it like any other root
  secret.
- PDF statement attachments are stored as blobs so they can be re-parsed after
  you add an issuer password. This is a deliberate exception to the "no message
  bodies at rest" rule the mail index otherwise follows.
