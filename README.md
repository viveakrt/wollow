# Wollow

Open-source, self-hosted email **and** personal finance, under one roof.
Connect your own mailboxes, plug in your own AI API key, and get both an
AI-organized inbox and a finance tracker that reads your bank and card mail —
without handing either your mailbox or your transaction history to a
third-party SaaS.

Two products, one platform:

- **Mail** — connect mailboxes over IMAP, read/delete/flag, AI classification
  into smart views (Needs Action, Bills, Finance, Security, …), on-demand
  summaries. Messages render as their sender wrote them: HTML in a sandboxed
  frame, inline images resolved, attachments downloadable, remote images held
  back until you ask for them.
- **Money** — accounts, cards, wallets, deposits and holdings; transactions,
  categories, bills, and cross-account transfer matching. Fed by statement
  imports and by the bank/card alert mail already sitting in your inbox.

They share a login, a database, an AI provider config, and — the part that
matters — a single mail sync. Money doesn't open its own IMAP connection; it
reads the same message index Mail builds.

## Status

Early but real. Mail syncs, classifies, and renders full messages with their
attachments. Money imports HDFC account, PPF and fixed-deposit exports and
Zerodha Console P&L exports, and reads bank/card/wallet alert mail into
transactions, statement mail into bill reminders, balance-update mail into the
balances themselves and broker order mail into holdings — through parsers you
define from the UI on one real email each. Google/Microsoft OAuth, mobile, and
desktop targets are on the roadmap.

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
  money/        parsers · emailparse · parserules · pdfparse · ledger · ingest · moneyapi

frontend/src/
  platform/     apiClient · auth · theme · queryClient · AppShell
  products/mail/    Inbox · MessageDetail · Senders · ConnectAccount · MailSidebar
  products/money/   Dashboard · Accounts · Transactions · Investments · Bills · Parsers · Import · Transfers
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

### Reading a message

The index holds headers and a snippet; the body is fetched live from IMAP when
you open a message, parsed out of its MIME tree, and split into three things:

- **text and HTML bodies.** Non-UTF-8 charsets are decoded rather than rejected,
  and a malformed MIME tree degrades to whatever was readable instead of
  failing the open. A message always shows *something*.
- **inline images.** An HTML body's `cid:` references are rewritten to
  `/api/mail/…/parts/<content-id>`, so the sender's own images render.
- **attachments**, listed with size and type and fetched by part number on
  demand — so opening a mail with a 20 MB statement PDF on it stays cheap.

HTML is rendered in a `sandbox`ed iframe with no `allow-scripts`, stripped of
executable markup, and given its own `Content-Security-Policy`. Remote images
are withheld until you ask for them, because loading one tells the sender you
opened the mail. Attachment types that would execute as a document (HTML, SVG)
are always served as downloads with `nosniff`, never inline — the endpoint is
reachable as a top-level URL, where the iframe's sandbox does not apply.

### Deleting, archiving, and Gmail

Deleting **moves messages to the server's trash**, discovered through RFC 6154
SPECIAL-USE (`\Trash`), falling back to a conventional name the server actually
lists. It never invents a folder: mail routed somewhere the user's other clients
don't look is worse than not deleting at all.

This matters most on Gmail, where the obvious implementation is silently wrong.
Gmail's default IMAP setting maps `\Deleted` + `EXPUNGE` onto *archive* — it
removes the INBOX label and leaves the message in All Mail, so mail you deleted
is still in your account. Only a move to `[Gmail]/Trash` actually deletes it.
Expunging is kept for the two cases a move can't cover: a server with no trash,
and messages already in the trash, where deleting means destroying them.

Archiving follows the same discovery. Gmail has no `\Archive` mailbox, so it
resolves to `\All` — moving into All Mail, which is precisely what archiving
means there — rather than creating a stray "Archive" label beside it.

Sender-level bulk actions run as detached jobs with polled progress. They cover
every message from a sender, which in a real mailbox is thousands; holding an
HTTP request open for that long dies to a proxy timeout, and IMAP addresses
*sets* of UIDs anyway, so the work is a handful of batched commands rather than
two per message.

### One sync, many consumers

Mail's sync pass indexes message headers and a short snippet into `messages`,
reconciles server-side deletions, and runs every five minutes. Message bodies
are never stored — they stay live-fetched from IMAP on open.

Money does not connect to IMAP. After each sync pass, `money/ingest` queries the
index that pass just refreshed for finance mail it hasn't looked at yet, and
pulls raw bodies for only those messages — over the **same connection**, inside
the same per-mailbox lock. One mailbox costs one IMAP session, no matter how many
products are reading it. `internal/mail/imap.go` is the only file in the
repository that imports an IMAP client.

A message qualifies as finance mail if it is from a known issuer domain, from
a sender one of your parsers names, **or** the AI classifier flagged it
`is_transactional`. Those last two arms are the point of reading the index:
they surface senders the registry has never heard of, which land as
`parsed_as = 'unrecognized'` rather than being silently skipped — and every
such message carries a "define a parser from this email" link.

### Parsers you define, not parsers we ship

There are no hard-coded issuer parsers. Every bank, card, wallet and broker
template is taught from one real email, on the **Parsers** page:

1. pick a message from a connected mailbox;
2. in its text (subject on the first line, then the body — normalized exactly
   as ingest will see it), select the amount, the account's last digits, the
   payee, the date, and so on, and say which is which;
3. say what the email is — a transaction alert, a card statement, a balance
   update or an investment order — and, for a transaction, whether money went
   out or in;
4. see what the draft reads out of the sender's twenty most recent messages,
   then save.

The server turns each marked value into an anchor — the shortest run of words
before (and, where needed, after) it that identifies exactly that spot in the
sample — with digits, whitespace, masks and addresses in the anchor generalized
so the next message's date or VPA doesn't break it. Anchors are always literal
text: user input never reaches the regex engine unquoted. `money/parserules`
owns normalization, derivation and application, so the editor and ingest can't
drift apart. A rule that matches a sender but can't read a particular message
falls through to the next rule, which is how one sender's several templates
coexist. Zerodha's password-protected PDF statements (contract notes, holding
statements) are the one shape rules can't yet express; holdings from them come
in through the Console P&L import for now.

### Knowing what you actually hold

Getting the *accounts* right matters more than getting any single transaction
right:

- **Mail never creates an account.** A parsed message names an account by its
  last four digits *together with* the sending institution (four digits alone
  collide, and a mis-attached transaction corrupts two balances at once). If no
  such account exists, the message is held as `pending_account` — listed on the
  Accounts and Parsers pages under "mail waiting for an account", with an
  *Add account* button prefilled from what the mail said. Adding it releases the
  held mail for the next sync. A rule can also be bound to one account
  outright, for senders that never state a number (a wallet).
- **Account types are never rewritten from mail.** The type you chose stands.
- **Archiving retires an account** — off the Accounts page and the dashboard,
  no longer matched by mail — while keeping its history, unlike delete.
- **The balance and credit limit the bank stated.** A balance-update rule
  records the figure in `balance_snapshots` rather than as a transaction, and
  the running balance is anchored on the most recent snapshot with only later
  transactions applied. "Set outstanding" on a card does the same by hand.

Deposits and holdings — fixed deposits, PPF, mutual funds, stocks — live in
`investments` rather than `finance_accounts`, because the facts that matter are
different ones (maturity date, rate, units) and a deposit has no transaction
stream to derive a balance from. They still count as assets.

Net worth is therefore **assets minus liabilities**, holdings included, not a
sum over every account balance: a card's outstanding spend is debt, and adding
it to savings understated what you owe and overstated what you have.

Every result is written back as a `message_links` row joining the index row to
whatever Money made of it. That one table carries both products' cross-links:

- an inbox message shows a chip for the transaction or bill it produced, and
  clicking it opens that record in Money
- a transaction shows a **Source email** link back to the message it came from
- the dashboard's upcoming bills each link back to the statement email

Messages that finance ingest examined but no rule could read still get a link
row, labelled `unrecognized`, and still render a marker in the inbox — an
untaught issuer stays visible rather than silently vanishing. Each link also
records which rule read the message, and a rule can be re-applied to the mail
that arrived before it existed ("apply to old mail").

### What a holding is worth

Prices and exchange rates are fetched every 30 minutes, and on demand from the
Investments page: stocks from Yahoo Finance, Indian mutual fund NAVs from AMFI's
own daily file, and `<currency>INR=X` for the rupee rate. Only symbols and ISINs
leave the machine — never quantities or amounts. A holding's symbol is found
from its ISIN or its name and can be corrected by hand; `none` switches fetching
off for it, and a quote whose currency doesn't match the holding is refused
rather than applied, because that error would misstate the position by the
exchange rate.

A rate you type in still outranks the market one, and a rate derived from your
own remittance is used when the market is unreachable.

Profit is reported two ways: **unrealised**, today's value against what the
remaining units cost, and **realised**, locked in by sells under a moving
average cost — buying again after a sale never changes the profit that sale
already made. For a foreign holding both are also stated in rupees, with each
buy and sell converted at *its own* trade date's rate (fetched from daily
history and stored per order), so the rupee's own movement is part of the
answer rather than lost in a single conversion at today's rate.

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

Want to see it with data but without connecting a mailbox? Seed from the sample
emails in `statements/`:
```
cd backend
go run ./cmd/seeddemo -data ./data -samples ../statements
```

Tests:
```
cd backend  && go test ./...             # rule derivation/application, IMAP, email → ledger pipeline
cd frontend && npm run lint
cd frontend && npm run test:e2e          # browser smoke test
cd frontend && npm run test:e2e:links    # cross-product round trip
cd frontend && npm run test:e2e:parsers  # define-a-parser flow: marking values by selecting text
```
See `frontend/e2e/README.md` for what the browser tests need.

The rule and ingest suites are table-driven on inline sample emails (a rule
derived from one alert must read its sibling), so they need no fixture files.
The statement-import suite runs against the real `.xls` exports in
`statements/`. A schema change that breaks transaction, bill, balance or
holding extraction fails the build.

## API surface

| Prefix | Auth | Owns |
| --- | --- | --- |
| `/api/auth/*` | public | login, logout |
| `/api/health` | public | liveness + database reachability, for healthchecks |
| `/api/settings` | session | AI provider, model, base URL (shared) |
| `/api/mail/*` | session | mailboxes, messages, message parts, sync, classify, insights |
| `/api/money/*` | session | finance accounts, transactions, categories, bills, investments, import, transfers |

Every route that is not explicitly registered as public requires a valid
session cookie.

## Roadmap

- Google & Microsoft OAuth (no app passwords needed)
- More bank/issuer parsers; itemized PDF statement extraction
- Native iOS, Android, and Windows desktop apps
- Rules & filters, AI-drafted replies
- Multi-user accounts

## Security notes

- Single-user mode: one admin password protects the whole instance, both
  products. Put it behind HTTPS (Caddy/Traefik) if you expose it beyond your
  local network, and set `WOLLOW_COOKIE_SECURE=true` once you do. Login attempts
  are rate-limited per source address, since that one password is the only thing
  between the internet and your mailbox.
- `WOLLOW_MASTER_KEY` encrypts stored IMAP passwords, AI API keys, and issuer
  PDF passwords. Losing it means reconnecting accounts and re-entering keys;
  leaking it means those secrets are recoverable. Treat it like any other root
  secret.
- `WOLLOW_JWT_SECRET` signs session cookies and must be at least 32 characters;
  a short one is brute-forceable offline from a single captured cookie, which
  yields a permanent forged login. The server refuses to start without one.
- **CORS is closed by default.** `WOLLOW_ALLOWED_ORIGINS` is empty unless you
  set it, and every deployment described above puts the UI and API on one
  origin, where CORS never applies. Listing an origin there grants that origin
  authenticated access to your mail and transactions — only do it if you are
  genuinely serving the UI from somewhere else.
- Message HTML is never trusted: it is stripped of executable markup, rendered
  in a sandboxed iframe with scripting off, and constrained by its own CSP.
  Remote images are blocked until you ask for them, so opening a mail does not
  confirm your address to the sender.
- PDF statement attachments are stored as blobs so they can be re-parsed after
  you add an issuer password. This is a deliberate exception to the "no message
  bodies at rest" rule the mail index otherwise follows. So is the one sample
  email each parser rule was defined on: its normalized text is kept in
  `email_parser_rules.sample_text` so the rule can be reopened for editing
  after the message is gone from the server. Deleting the rule deletes it.
