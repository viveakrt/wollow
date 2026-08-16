# End-to-end tests

Both drive a real browser, so they need the stack running first.

```
# terminal 1 — backend
cd backend
export WOLLOW_MASTER_KEY=$(openssl rand -hex 32)
export WOLLOW_JWT_SECRET=$(openssl rand -hex 32)
export WOLLOW_ADMIN_PASSWORD=devpassword
export WOLLOW_DATA_DIR=./data
go run ./cmd/server

# terminal 2 — frontend on the port the tests expect
cd frontend
npm run dev -- --port 5199 --strictPort

# terminal 3
cd frontend
npx playwright install chromium   # first time only
npm run test:e2e                  # shell
npm run test:e2e:links            # cross-product links (needs seeded data, below)
```

Both assume `WOLLOW_ADMIN_PASSWORD=devpassword`.

## `smoke.mjs` — the shell

Logging in once, switching between Mail and Money without a page reload, deep
links surviving a hard refresh, and the theme toggle actually repainting. Runs
against an empty database.

## `links.mjs` — the cross-product links

The round trip: an inbox message shows what Money made of it, clicking through
lands on that transaction, and the banner there links back to the message.

Needs a database with mail and money data in it, which `seeddemo` produces from
the sample emails in `statements/` — no real mailbox required:

```
cd backend
go run ./cmd/seeddemo -data ./data -samples ../statements
```

Run that **before** starting the server (it seeds 9 indexed messages, 3
transactions and 3 bills). Message bodies are deliberately not stored by the
index, so opening a message still needs a live IMAP connection and will show a
fetch error against seeded data — every link assertion is about addressing, not
about body rendering.
