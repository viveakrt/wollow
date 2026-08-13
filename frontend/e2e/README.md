# End-to-end smoke test

Checks the things that only break once both products share a shell: logging in
once, switching between Mail and Money without a page reload, deep links
surviving a hard refresh, and the theme toggle actually repainting.

It drives a real browser, so it needs the stack running first.

```
# terminal 1 — backend
cd backend
export WOLLOW_MASTER_KEY=$(openssl rand -hex 32)
export WOLLOW_JWT_SECRET=$(openssl rand -hex 32)
export WOLLOW_ADMIN_PASSWORD=devpassword
export WOLLOW_DATA_DIR=./data
go run ./cmd/server

# terminal 2 — frontend on the port the test expects
cd frontend
npm run dev -- --port 5199 --strictPort

# terminal 3
cd frontend
npx playwright install chromium   # first time only
npm run test:e2e
```

The test assumes `WOLLOW_ADMIN_PASSWORD=devpassword`. It does not need a real
mailbox or any imported statements — every assertion is about the shell.
