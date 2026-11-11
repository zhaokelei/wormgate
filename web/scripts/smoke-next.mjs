/**
 * End-to-end smoke test for the grey-release React console at /next/.
 *
 * Like scripts/shoot.mjs this drives the system Edge/Chrome through
 * playwright-core, so nothing is downloaded and it works offline. Unlike that
 * script it asserts behaviour instead of only capturing pixels: it logs in, walks
 * every route, and round-trips a throwaway account through create → edit → delete
 * so the React form payloads are validated against the real API.
 *
 * Usage:
 *   node scripts/smoke-next.mjs                       # local demo on :17200
 *   TX_BASE=https://host TX_PASS=... node scripts/smoke-next.mjs
 *
 * Exit code is non-zero when a page logs an error or a request fails, which makes
 * it usable as a pre-switchover gate.
 */
import { chromium } from 'playwright-core'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const here = path.dirname(fileURLToPath(import.meta.url))
const outDir = process.env.TX_OUT || path.resolve(here, '../../.run/next-smoke')

const BASE = process.env.TX_BASE || 'http://127.0.0.1:17200'
const USER = process.env.TX_USER || 'admin'
const PASS = process.env.TX_PASS || ''
const CHANNELS = ['msedge', 'chrome']
const TEMP_USER = `smoke${Date.now().toString().slice(-6)}`

const ROUTES = [
  ['dashboard', '/next/'],
  ['tunnels', '/next/tunnels'],
  ['clients', '/next/clients'],
  ['nodes', '/next/nodes'],
  ['alerts', '/next/alerts'],
  ['audit', '/next/audit'],
  ['users', '/next/users'],
  ['security', '/next/security']
]

if (!PASS) {
  console.error('TX_PASS is required (the console needs a real login)')
  process.exit(2)
}

async function launch() {
  for (const channel of CHANNELS) {
    try {
      return await chromium.launch({ channel, headless: true })
    } catch {
      /* next channel */
    }
  }
  throw new Error('neither msedge nor chrome is installed')
}

const problems = []
const note = (msg) => {
  problems.push(msg)
  console.log('  !', msg)
}

const browser = await launch()
await mkdir(outDir, { recursive: true })

const ctx = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  colorScheme: 'dark',
  ignoreHTTPSErrors: true
})
const page = await ctx.newPage()

page.on('console', (m) => {
  const text = m.text()
  // The pre-login /api/me probe is expected to 401; everything else is a bug.
  if (m.type() === 'error' && !text.includes('401')) note(`console: ${text.slice(0, 180)}`)
})
page.on('pageerror', (e) => note(`pageerror: ${String(e).slice(0, 180)}`))
page.on('response', (r) => {
  const url = r.url()
  if (r.status() >= 400 && !url.includes('/api/me') && !url.includes('favicon')) {
    note(`http ${r.status()} ${r.request().method()} ${url}`)
  }
})

// ---- login ---------------------------------------------------------------
await page.goto(`${BASE}/next/login`, { waitUntil: 'domcontentloaded' })
await page.waitForTimeout(600)
await page.screenshot({ path: `${outDir}/01-login.png` })

await page.fill('input#username', USER)
await page.fill('input[type="password"]', PASS)
await page.click('button[type="submit"]')
await page.waitForURL((u) => !u.pathname.endsWith('/login'), { timeout: 15000 })
await page.waitForTimeout(1000)

// Antd renders skeletons while a query is in flight. Capturing at a fixed
// delay produced screenshots of the loading state (empty cards, "--" values),
// which reads like a broken page. Wait for the skeletons to clear instead.
async function settle(extra = 520) {
  const deadline = Date.now() + 10000
  while (Date.now() < deadline) {
    const busy = await page.evaluate(
      () => !!document.querySelector('.ant-skeleton, .ant-spin-spinning')
    )
    if (!busy) break
    await page.waitForTimeout(120)
  }
  await page.waitForTimeout(extra)
}

// ---- every route ---------------------------------------------------------
let i = 2
for (const [name, route] of ROUTES) {
  await page.goto(`${BASE}${route}`, { waitUntil: 'domcontentloaded' })
  // The dashboard chart needs two 2 s samples before it draws a curve; every
  // page needs the stagger to finish or the screenshot catches a faded half.
  await settle(name === 'dashboard' ? 4500 : 520)
  await page.screenshot({ path: `${outDir}/${String(i).padStart(2, '0')}-${name}.png`, fullPage: true })
  i += 1
  console.log('  ok', route)
}

// ---- horizontal overflow across breakpoints ------------------------------
for (const width of [1920, 1440, 1280, 1024]) {
  await page.setViewportSize({ width, height: 900 })
  for (const [, route] of ROUTES) {
    await page.goto(`${BASE}${route}`, { waitUntil: 'domcontentloaded' })
    await page.waitForTimeout(500)
    const overflow = await page.evaluate(() => {
      const de = document.documentElement
      return de.scrollWidth - de.clientWidth
    })
    if (overflow > 0) note(`${route} overflows horizontally by ${overflow}px at ${width}px`)
  }
}
await page.setViewportSize({ width: 1440, height: 900 })

// ---- users CRUD round trip ----------------------------------------------
await page.goto(`${BASE}/next/users`, { waitUntil: 'domcontentloaded' })
await page.waitForTimeout(600)

await page.getByRole('button', { name: /新建账号/ }).click()
await page.waitForTimeout(400)
await page.fill('input#username', TEMP_USER)
await page.fill('input#display_name', '冒烟测试账号')
await page.fill('input#password', 'SmokePass12345!')
await page.getByRole('button', { name: /^创\s*建$/ }).click()
await page.waitForTimeout(1500)
if (!(await page.getByText(TEMP_USER).count())) {
  note(`创建 ${TEMP_USER} 后表格里没有出现`)
} else {
  console.log('  ok create')

  const row = page.locator('tr', { hasText: TEMP_USER })
  await row.locator('button:has(.anticon-edit)').click()
  await page.waitForTimeout(400)
  await page.fill('input#display_name', '冒烟已改名')
  await page.getByRole('button', { name: /^保\s*存$/ }).click()
  await page.waitForTimeout(1300)
  if (!(await page.getByText('冒烟已改名').count())) note('编辑后显示名没有更新')
  else console.log('  ok edit')

  const row2 = page.locator('tr', { hasText: TEMP_USER })
  await row2.locator('button:has(.anticon-delete)').click()
  await page.waitForTimeout(400)
  await page.getByRole('button', { name: /^删\s*除$/ }).click()
  await page.waitForTimeout(1300)
  if (await page.getByText(TEMP_USER).count()) note('删除后表格里仍有该账号')
  else console.log('  ok delete')
}

// ---- light theme ---------------------------------------------------------
await page.goto(`${BASE}/next/`, { waitUntil: 'domcontentloaded' })
await settle(4500)
const themeBtn = page.locator('button:has(.anticon-bulb)').first()
if (await themeBtn.count()) {
  await themeBtn.click()
  await page.waitForTimeout(800)
  await page.screenshot({ path: `${outDir}/12-dashboard-light.png`, fullPage: true })
}

await browser.close()

console.log(problems.length ? `\n${problems.length} problem(s)` : `\nall green — screenshots in ${outDir}`)
process.exit(problems.length ? 1 : 0)
