import { chromium } from 'playwright'

// Drives the user-defined parser flow: the Parsers page, the archived toggle
// on Accounts, and the editor — marking values by selecting text, exactly as
// a person would, and checking the marks line up with the server's offsets.
//
// The editor is exercised on a temporary rule created through the API (its
// sample text lives in the database, so no mailbox is needed) and deleted
// afterwards. The live path — picking a real email and testing the draft
// against recent mail — runs only when a mailbox is connected and reachable,
// and is skipped (not failed) otherwise.
const BASE = process.env.E2E_BASE || 'http://localhost:5199'
const PASSWORD = process.env.E2E_PASSWORD || 'devpassword'
const SHOT = process.argv[2] || '.'
const results = []
function check(name, pass, detail = '') {
  results.push({ name, pass, detail })
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${name}${detail ? '  — ' + detail : ''}`)
}
function skip(name, why) {
  console.log(`SKIP  ${name}  — ${why}`)
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const consoleErrors = []
page.on('console', (m) => {
  if (m.type() === 'error') consoleErrors.push(m.text())
})
page.on('pageerror', (e) => consoleErrors.push(String(e)))

const api = (path, init) =>
  page.evaluate(
    async ([path, init]) => {
      const r = await fetch(path, init)
      let body = null
      try {
        body = await r.json()
      } catch {}
      return { status: r.status, body }
    },
    [path, init],
  )

await page.goto(BASE, { waitUntil: 'networkidle' })
await page.fill('input[type="password"]', PASSWORD)
await page.click('button[type="submit"]')
await page.waitForURL('**/mail', { timeout: 10000 })
check('login', page.url().endsWith('/mail'))

// --- API surface ---
const fields = await api('/api/money/parser-rules/fields')
check('fields catalogue served', fields.body?.transaction?.[0]?.name === 'amount')
check('pending accounts endpoint', (await api('/api/money/accounts/pending')).status === 200)
check('parser rules list endpoint', (await api('/api/money/parser-rules')).status === 200)

// --- Parsers page ---
await page.goto(`${BASE}/money/parsers`, { waitUntil: 'networkidle' })
await page.waitForSelector('h1:has-text("Parsers")', { timeout: 15000 })
check('Parsers page renders', true)
check('Parsers is in the Money nav', (await page.locator('aside a[href="/money/parsers"]').count()) === 1)
await page.screenshot({ path: `${SHOT}/parsers.png` })

// --- Accounts page: grouped, with the archived toggle ---
await page.goto(`${BASE}/money/accounts`, { waitUntil: 'networkidle' })
await page.waitForSelector('h1:has-text("Accounts")', { timeout: 15000 })
check('Accounts has a Show archived toggle', (await page.locator('label:has-text("Show archived")').count()) === 1)
await page.screenshot({ path: `${SHOT}/accounts.png` })

// Selects a run of text inside the editor's <pre> the way a person would —
// a Range over its text nodes — and fires mouseup so the mark menu opens.
// Offsets are code points, matching the editor and the server.
async function selectInPre(pattern) {
  return page.evaluate((source) => {
    const pre = document.querySelector('pre')
    if (!pre) return { ok: false, why: 'no pre' }
    const text = pre.textContent || ''
    const m = new RegExp(source).exec(text)
    if (!m) return { ok: false, why: 'pattern not in text' }
    const cpStart = Array.from(text.slice(0, m.index)).length
    const cpEnd = cpStart + Array.from(m[0]).length
    const utf16 = (s, cpIdx) => Array.from(s).slice(0, cpIdx).join('').length
    const walker = document.createTreeWalker(pre, NodeFilter.SHOW_TEXT)
    let cp = 0
    let startNode = null, startOff = 0, endNode = null, endOff = 0
    while (walker.nextNode()) {
      const node = walker.currentNode
      const len = Array.from(node.data).length
      if (!startNode && cpStart < cp + len) { startNode = node; startOff = utf16(node.data, cpStart - cp) }
      if (!endNode && cpEnd <= cp + len) { endNode = node; endOff = utf16(node.data, cpEnd - cp); break }
      cp += len
    }
    if (!startNode || !endNode) return { ok: false, why: 'offsets not found' }
    const range = document.createRange()
    range.setStart(startNode, startOff)
    range.setEnd(endNode, endOff)
    const sel = window.getSelection()
    sel.removeAllRanges()
    sel.addRange(range)
    pre.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }))
    return { ok: true, value: m[0] }
  }, pattern)
}

// --- Editor on a stored sample (no mailbox needed) ---
const sampleText =
  '❗ You have done a UPI txn. Check details!\n' +
  'Rs.1,234.00 is debited from your account ending XX4125 towards VPA swiggy@ybl (SWIGGY LIMITED) on 12-08-26.\n' +
  'UPI transaction reference no.: 123456789012.'
const amountAt = Array.from(sampleText.slice(0, sampleText.indexOf('1,234.00'))).length
const created = await api('/api/money/parser-rules', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({
    name: 'e2e temporary parser',
    kind: 'transaction',
    enabled: false,
    issuer: 'HDFC',
    senderDomain: 'e2e.invalid',
    accountType: 'bank',
    attributes: { direction: 'expense' },
    sample: { mailAccountId: 0, uid: 0, folder: 'INBOX', subject: '❗ You have done a UPI txn. Check details!', from: 'alerts@e2e.invalid', text: sampleText },
    spans: [
      { name: 'amount', start: amountAt, end: amountAt + '1,234.00'.length, sample: '1,234.00' },
      { name: 'account_last4', start: Array.from(sampleText.slice(0, sampleText.indexOf('XX4125'))).length, end: Array.from(sampleText.slice(0, sampleText.indexOf('XX4125'))).length + 6, sample: 'XX4125' },
    ],
  }),
})
check('rule created from spans via API', created.status === 201 && created.body?.fields?.length === 2, `status ${created.status} ${JSON.stringify(created.body?.error ?? '')}`)
const ruleId = created.body?.id

if (ruleId) {
  try {
    await page.goto(`${BASE}/money/parsers/${ruleId}`, { waitUntil: 'networkidle' })
    const loaded = await page.waitForSelector('text=Mark the values', { timeout: 15000 }).then(() => true).catch(() => false)
    check('editor opens a stored rule on the marking step', loaded)
    if (loaded) {
      // The server tightened "XX4125" to the digits; the editor shows that.
      const amountMark = page.locator('mark[data-field="amount"]')
      const last4Mark = page.locator('mark[data-field="account_last4"]')
      check('stored marks render at the right text', (await amountMark.textContent()) === '1,234.00' && (await last4Mark.textContent()) === '4125',
        `amount=${await amountMark.textContent().catch(() => '')} last4=${await last4Mark.textContent().catch(() => '')}`)

      // Mark two more values by selecting them: the payee (after an emoji and
      // a ₹-less line, so multi-byte offsets are in play) and the reference.
      const payee = await selectInPre('SWIGGY LIMITED')
      check('selecting text opens the mark menu', payee.ok && (await page.locator('button:has-text("Sender / receiver")').count()) > 0, payee.why ?? '')
      await page.locator('button:has-text("Sender / receiver")').first().click()
      const payeeMark = page.locator('mark[data-field="counterparty"]')
      check('payee highlighted exactly as selected', (await payeeMark.count()) === 1 && (await payeeMark.textContent()) === 'SWIGGY LIMITED')

      const ref = await selectInPre('123456789012')
      await page.locator('button:has-text("Reference number")').first().click()
      const refMark = page.locator('mark[data-field="reference"]')
      check('reference highlighted exactly as selected', ref.ok && (await refMark.count()) === 1 && (await refMark.textContent()) === '123456789012')

      // Overlapping marks are refused.
      await selectInPre('SWIGGY')
      await page.locator('button:has-text("Description")').first().click()
      check('overlapping selection is refused', (await page.locator('text=overlaps').count()) === 1)

      // Clearing a mark removes the highlight.
      await page.locator('span:has-text("Reference number") button[title="Clear"]').first().click()
      check('a mark can be cleared', (await refMark.count()) === 0)
      await page.screenshot({ path: `${SHOT}/parser-mark.png` })

      check('test button enabled with required values marked', await page.locator('button:has-text("Test on recent mail")').isEnabled())
    }
  } finally {
    const deleted = await api(`/api/money/parser-rules/${ruleId}`, { method: 'DELETE' })
    check('temporary rule deleted', deleted.status === 204, `status ${deleted.status}`)
  }
}

// --- Live path: pick a real email from a connected mailbox ---
await page.goto(`${BASE}/money/parsers/new`, { waitUntil: 'networkidle' })
await page.waitForSelector('text=Choose an email', { timeout: 15000 })
check('editor starts on the email picker', true)

const mailboxes = (await api('/api/money/email-accounts')).body
if (!Array.isArray(mailboxes) || mailboxes.length === 0) {
  skip('live email pick', 'no mailbox connected')
} else {
  await page.waitForSelector('div.max-h-\\[32rem\\] button', { timeout: 15000 }).catch(() => {})
  const rows = page.locator('button:has-text("No parser matched")')
  const candidate = (await rows.count()) > 0 ? rows.first() : page.locator('div.max-h-\\[32rem\\] button').first()
  if ((await candidate.count()) === 0) {
    skip('live email pick', 'mailbox has no messages in the index')
  } else {
    await candidate.click()
    const outcome = await Promise.race([
      page.waitForSelector('text=Mark the values', { timeout: 30000 }).then(() => 'loaded'),
      page.waitForSelector('text=could not fetch', { timeout: 30000 }).then(() => 'unreachable'),
    ]).catch(() => 'timeout')
    if (outcome === 'loaded') {
      check('live sample loads into the editor', true)
      const preText = await page.locator('pre').first().textContent()
      check('live sample text rendered', Boolean(preText && preText.length > 0), `${preText?.length ?? 0} chars`)
      const marked = await selectInPre('\\d[\\d,]*\\.\\d{2}|\\d{3,}')
      if (marked.ok) {
        await page.locator('button:has-text("Amount")').first().click()
        check('live amount highlighted', (await page.locator('mark[data-field="amount"]').textContent()) === marked.value)
        const nameInput = page.locator('input').first()
        if (!(await nameInput.inputValue())) await nameInput.fill('e2e draft')
        await page.locator('button:has-text("Test on recent mail")').click()
        const tested = await page.waitForSelector('text=Check what it reads', { timeout: 60000 }).then(() => true).catch(() => false)
        check('draft rule tested against recent mail', tested)
        if (tested) await page.screenshot({ path: `${SHOT}/parser-test.png` })
      }
    } else {
      // The mailbox exists but IMAP is not reachable from here (wrong master
      // key, no network). Not a UI failure.
      const msg = await page.locator('div[class*="negative"]:has-text("could not fetch")').first().textContent().catch(() => '')
      skip('live email pick', `mailbox unreachable: ${msg?.trim().slice(0, 160)}`)
      consoleErrors.splice(0, consoleErrors.length, ...consoleErrors.filter((e) => !/status of 502/.test(e)))
    }
  }
}

const realErrors = consoleErrors.filter((e) => !/status of 401/.test(e))
check('no unexpected console errors', realErrors.length === 0, realErrors.slice(0, 3).join(' | '))

await browser.close()
const failed = results.filter((r) => !r.pass)
console.log(`\n${results.length - failed.length}/${results.length} passed`)
process.exit(failed.length ? 1 : 0)
