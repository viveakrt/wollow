import { chromium } from 'playwright'

const BASE = 'http://localhost:5199'
const SHOT = process.argv[2] || '.'
const results = []
function check(name, pass, detail = '') {
  results.push({ name, pass, detail })
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${name}${detail ? '  — ' + detail : ''}`)
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const consoleErrors = []
page.on('console', (m) => {
  if (m.type() === 'error') consoleErrors.push(m.text())
})
page.on('pageerror', (e) => consoleErrors.push(String(e)))

// --- unauthenticated lands on login ---
await page.goto(BASE, { waitUntil: 'networkidle' })
check('unauthenticated redirects to /login', page.url().endsWith('/login'), page.url())

// --- log in once ---
await page.fill('input[type="password"]', 'devpassword')
await page.click('button[type="submit"]')
await page.waitForURL('**/mail', { timeout: 10000 })
check('login lands in Mail', page.url().endsWith('/mail'), page.url())

// Marker on window: survives client-side nav, dies on a full page load.
await page.evaluate(() => {
  window.__spaMarker = 'alive'
})

// --- rail switch to Money ---
await page.click('nav[aria-label="Products"] a[aria-label="Money"]')
await page.waitForURL('**/money', { timeout: 10000 })
await page.waitForSelector('text=Dashboard', { timeout: 10000 })
check('rail switches to Money', page.url().endsWith('/money'))
const markerAfterMoney = await page.evaluate(() => window.__spaMarker)
check('Mail -> Money is client-side (no reload)', markerAfterMoney === 'alive', `marker=${markerAfterMoney}`)
check('Money sidebar rendered', (await page.locator('aside').count()) > 0)
await page.screenshot({ path: `${SHOT}/money-dark-or-light.png` })

// --- money sub-navigation ---
await page.click('aside a[href="/money/transactions"]')
await page.waitForURL('**/money/transactions', { timeout: 10000 })
check('Money sub-nav works', page.url().endsWith('/money/transactions'))

// --- back to Mail ---
await page.click('nav[aria-label="Products"] a[aria-label="Mail"]')
await page.waitForURL('**/mail', { timeout: 10000 })
const markerAfterMail = await page.evaluate(() => window.__spaMarker)
check('Money -> Mail is client-side (no reload)', markerAfterMail === 'alive', `marker=${markerAfterMail}`)

// --- session survived both switches (no bounce to login) ---
check('session persists across product switches', !page.url().includes('/login'), page.url())

// --- theme toggle: system -> light -> dark ---
const themeBtn = 'nav[aria-label="Products"] button[aria-label*="theme"]'
const readTheme = () => page.evaluate(() => ({
  stamp: document.documentElement.getAttribute('data-theme'),
  bg: getComputedStyle(document.body).backgroundColor,
}))
const t0 = await readTheme()
await page.click(themeBtn)
const t1 = await readTheme()
await page.click(themeBtn)
const t2 = await readTheme()
check('theme toggle cycles system -> light -> dark',
  t0.stamp === null && t1.stamp === 'light' && t2.stamp === 'dark',
  `${t0.stamp} -> ${t1.stamp} -> ${t2.stamp}`)
check('dark theme actually repaints body', t1.bg !== t2.bg, `${t1.bg} vs ${t2.bg}`)
await page.screenshot({ path: `${SHOT}/mail-dark.png` })

// --- hard reload of a deep product link keeps the session (HttpOnly cookie,
//     so the client has to probe the server on boot) ---
await page.click(themeBtn) // -> system
await page.click(themeBtn) // -> light
await page.goto(`${BASE}/money/transactions`, { waitUntil: 'networkidle' })
await page.waitForSelector('nav[aria-label="Products"]', { timeout: 15000 })
check('deep link survives a hard reload', page.url().endsWith('/money/transactions'), page.url())

await page.goto(`${BASE}/money`, { waitUntil: 'networkidle' })
await page.waitForSelector('nav[aria-label="Products"]', { timeout: 15000 })
await page.screenshot({ path: `${SHOT}/money-light.png` })
const lightBg = (await readTheme()).bg
check('light theme ground is light', lightBg === 'rgb(244, 245, 248)', lightBg)

// --- settings is reachable from the rail ---
await page.click('nav[aria-label="Products"] a[aria-label="Settings"]')
await page.waitForURL('**/settings', { timeout: 10000 })
check('shared Settings reachable from rail', page.url().endsWith('/settings'))

// The boot probe deliberately hits /api/settings unauthenticated on the login
// screen; the browser logs that 401 as a resource error. Anything else is real.
const realErrors = consoleErrors.filter((e) => !/status of 401/.test(e))
check('no unexpected console errors', realErrors.length === 0, realErrors.slice(0, 3).join(' | '))
check('auth probe 401s only before login', consoleErrors.length === consoleErrors.filter((e) => /status of 401/.test(e)).length)

await browser.close()

const failed = results.filter((r) => !r.pass)
console.log(`\n${results.length - failed.length}/${results.length} passed`)
process.exit(failed.length ? 1 : 0)
