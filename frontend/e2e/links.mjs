// Cross-product link round trip: inbox message -> transaction -> back to the
// message. Needs a seeded database — see e2e/README.md.
import { chromium } from 'playwright'

const BASE = process.env.WOLLOW_E2E_BASE || 'http://localhost:5199'
const PASSWORD = process.env.WOLLOW_ADMIN_PASSWORD || 'devpassword'
const SHOT = process.argv[2] || '.'

const results = []
function check(name, pass, detail = '') {
  results.push({ name, pass })
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${name}${detail ? '  — ' + detail : ''}`)
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1500, height: 950 } })
const errors = []
page.on('pageerror', (e) => errors.push(String(e)))

await page.goto(BASE, { waitUntil: 'networkidle' })
await page.fill('input[type="password"]', PASSWORD)
await page.click('button[type="submit"]')
await page.waitForURL('**/mail', { timeout: 15000 })
await page.waitForSelector('nav[aria-label="Products"]', { timeout: 15000 })

// --- Mail side: the inbox shows what Money made of each message ---
const txnChips = page.locator('a[href^="/money/transactions?txn="]')
const billChips = page.locator('a[href="/money/bills"]')
await txnChips.first().waitFor({ timeout: 15000 })

const txnChipCount = await txnChips.count()
const billChipCount = await billChips.count()
check('inbox shows transaction chips', txnChipCount === 3, `${txnChipCount} chips`)
check('inbox shows bill chips', billChipCount === 3, `${billChipCount} chips`)
await page.screenshot({ path: `${SHOT}/links-inbox.png` })

// --- Mail -> Money ---
const chipHref = await txnChips.first().getAttribute('href')
const txnId = Number(new URL(chipHref, BASE).searchParams.get('txn'))
check('chip carries a transaction id', Number.isInteger(txnId) && txnId > 0, String(txnId))

await txnChips.first().click()
await page.waitForURL('**/money/transactions?txn=*', { timeout: 15000 })
check('chip navigates into Money', page.url().includes(`txn=${txnId}`), page.url())

const banner = page.locator('a:has-text("Back to email")')
await banner.waitFor({ timeout: 15000 })
check('linked-transaction banner renders', await banner.isVisible())

const highlighted = page.locator('tr.ring-1')
check('the linked row is highlighted', (await highlighted.count()) === 1, `${await highlighted.count()} rows`)
await page.screenshot({ path: `${SHOT}/links-transaction.png` })

// --- Money -> Mail: the return leg ---
const backHref = await banner.getAttribute('href')
check('back link addresses a mailbox + uid', /^\/mail\/messages\/\d+\/\d+$/.test(backHref), backHref)

await banner.click()
await page.waitForURL('**/mail/messages/**', { timeout: 15000 })
check('back link returns to the message in Mail', page.url().includes('/mail/messages/'), page.url())

// The round trip is closed if the URL we came back to is the message whose chip
// we clicked. Body rendering needs live IMAP, which a seeded DB has no way to
// provide, so the assertion is on addressing, not on the body.
check('round trip closed', page.url().endsWith(backHref), `${page.url()} vs ${backHref}`)

// --- Dashboard surfaces bills discovered in mail ---
await page.goto(`${BASE}/money`, { waitUntil: 'networkidle' })
await page.waitForSelector('nav[aria-label="Products"]', { timeout: 15000 })
const billsCard = page.locator('text=Upcoming Bills')
await billsCard.waitFor({ timeout: 15000 })
check('dashboard shows the Upcoming Bills card', await billsCard.isVisible())

const billSourceLinks = page.locator('a[href^="/mail/messages/"]')
const billSourceCount = await billSourceLinks.count()
check('dashboard bills link back to their source email', billSourceCount >= 1, `${billSourceCount} links`)
await page.screenshot({ path: `${SHOT}/links-dashboard.png` })

check('no page errors', errors.length === 0, errors.slice(0, 2).join(' | '))

await browser.close()
const failed = results.filter((r) => !r.pass)
console.log(`\n${results.length - failed.length}/${results.length} passed`)
process.exit(failed.length ? 1 : 0)
