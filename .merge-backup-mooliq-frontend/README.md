# Mooliq

Personal finance tracker. Go backend (SQLite) + React frontend. Currently supports importing HDFC Bank savings/current account statements (`.xls` export from NetBanking) and tracking accounts, transactions, and a monthly dashboard.

## Run it

**Backend** (http://localhost:8080):

```
cd backend
go run ./cmd/server
```

Creates `backend/mooliq.db` (SQLite) on first run. Override with `MOOLIQ_DB_PATH` / `MOOLIQ_ADDR` env vars.

**Frontend** (http://localhost:5173):

```
cd frontend
npm install   # first time only
npm run dev
```

The Vite dev server proxies `/api/*` to `localhost:8080`, so just open http://localhost:5173.

## What's here

- `backend/internal/parser/hdfc.go` — parses HDFC's BIFF `.xls` statement export (real Excel binary, not HTML). Reverse-engineered from the sample files in `statements/HDFC/`. Verified: opening balance + deposits − withdrawals reconciles exactly against the statement's closing balance on all 5 sample files.
- `backend/internal/emailparser/` — connects to Gmail over IMAP (App Password auth) and parses bank/card alert emails into transactions and bill reminders. Covers HDFC (UPI debit, NEFT credit, balance-update snapshots) and Axis (card spend alerts) as full transactions; ICICI/BOBCARD/HDFC Diners statement emails as bill reminders (due date + amount when present in the email body; PDF attachments aren't parsed yet). Only scans emails from an allowlisted set of bank/card sender domains.
- `backend/internal/api/` — REST API: accounts, transactions, categories, dashboard summary, statement import (preview + commit with dedupe), email account connect/sync, bills.
- `frontend/src/pages/` — Dashboard, Accounts, Transactions, Import Statement (upload → review → commit flow), Bills, Settings (Gmail connect).

## Gmail setup

Settings → Connect Gmail needs a Google **App Password** (not your normal password): [myaccount.google.com/apppasswords](https://myaccount.google.com/apppasswords), requires 2-Step Verification enabled. The password is stored in the local SQLite DB only — this app has no server component and never leaves your machine.

## Not built yet

Investments, Budgets, Reports, and Net Worth screens from the design reference (`screenshots/`) are stubbed as "coming soon" — the current build focused on the core loop: import a statement or sync email, see it on the dashboard, browse transactions. Other banks aren't supported yet for statement import; the parser is HDFC-specific but the schema/API is bank-agnostic, so adding another parser (e.g. ICICI, SBI) is additive. For email, only HDFC and Axis alerts become transactions — other issuers' alert formats would need their own parser added to `internal/emailparser/`. PDF statement attachments (BOBCARD, HDFC Diners, full ICICI itemization) aren't opened/parsed.
