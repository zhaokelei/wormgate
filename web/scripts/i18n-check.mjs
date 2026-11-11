/**
 * Language-switch regression check for the React console at /next/.
 *
 * Asserts three things the console promised but did not deliver:
 *   1. a language toggle exists on the sign-in screen and in the header,
 *   2. switching to English leaves no Chinese copy behind (empty strings or
 *      half-translated labels are what a DOM-layer translator gets wrong),
 *   3. the header shows the account name once — the role chip must not repeat it.
 *
 * Usage:
 *   node scripts/i18n-check.mjs                              # local fixture
 *   TX_BASE=https://host TX_PASS=... node scripts/i18n-check.mjs
 */
import { chromium } from 'playwright-core'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const here = path.dirname(fileURLToPath(import.meta.url))
const outDir = process.env.TX_OUT || path.resolve(here, '../../.run/i18n-check')
const BASE = process.env.TX_BASE || 'http://127.0.0.1:17300'
const USER = process.env.TX_USER || 'admin'
const PASS = process.env.TX_PASS || ''
const CHANNELS = ['msedge', 'chrome']

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

const problems = []
const note = (msg) => {
  problems.push(msg)
  console.log('  !', msg)
}
const ok = (msg) => console.log('  ok', msg)

async function launch() {
  for (const channel of CHANNELS) {
    try {
      return await chromium.launch({ channel, headless: true })
    } catch {
      /* try the next installed browser */
    }
  }
  throw new Error('neither msedge nor chrome is installed')
}

/**
 * English glued at a sentence boundary: `Need the legacy UI?Open the Vue
 * console`. Chinese needs no space before a link, so the source copy runs the
 * clauses together and only the English rendering looks broken.
 */
async function gluedSentences(page) {
  return page.evaluate(() => {
    const text = document.body.innerText
    return (text.match(/[?!][A-Za-z][a-z]{2,}/g) || []).slice(0, 5)
  })
}

/** Text nodes that still contain Chinese after a switch to English. */
async function leftoverChinese(page) {
  return page.evaluate(() => {
    const CJK = /[\u4e00-\u9fff]/
    const SKIP = new Set(['SCRIPT', 'STYLE', 'TEXTAREA'])
    const hits = []
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    let node
    while ((node = walker.nextNode())) {
      const el = node.parentElement
      if (!el || SKIP.has(el.tagName) || el.closest('[data-i18n-skip]')) continue
      const text = (node.nodeValue || '').trim()
      if (!text || !CJK.test(text)) continue
      hits.push(`${el.tagName.toLowerCase()}.${String(el.className).split(' ')[0]}: ${text.slice(0, 40)}`)
    }
    return hits
  })
}

/**
 * Every visible string in the header, split by whether it is marked
 * `data-i18n-skip`. That marker is how the language button keeps its literal
 * "中" in English mode, so the leftover-Chinese check must skip it too.
 */
async function headerText(page) {
  return page.evaluate(() => {
    const bar = document.querySelector('.ant-layout-header')
    if (!bar) return { text: '', skipped: '' }
    const walker = document.createTreeWalker(bar, NodeFilter.SHOW_TEXT)
    const parts = []
    const skipped = []
    let node
    while ((node = walker.nextNode())) {
      const value = (node.nodeValue || '').trim()
      if (!value) continue
      if (node.parentElement && node.parentElement.closest('[data-i18n-skip]')) skipped.push(value)
      else parts.push(value)
    }
    return { text: parts.join(' '), skipped: skipped.join(' ') }
  })
}

if (!PASS) {
  console.error('TX_PASS is required (the console needs a real login)')
  process.exit(2)
}

await mkdir(outDir, { recursive: true })
const browser = await launch()
const ctx = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  colorScheme: 'dark',
  ignoreHTTPSErrors: true
})
const page = await ctx.newPage()
page.on('pageerror', (e) => note(`pageerror: ${String(e).slice(0, 160)}`))

// ---- sign-in screen: the toggle must exist before login ------------------
await page.goto(`${BASE}/next/login`, { waitUntil: 'domcontentloaded' })
await page.waitForTimeout(700)
await page.screenshot({ path: `${outDir}/01-login-zh.png` })

const loginToggle = page.getByRole('button', { name: /switch to english/i })
if ((await loginToggle.count()) === 0) note('sign-in screen has no language toggle')
else {
  await loginToggle.click()
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${outDir}/02-login-en.png` })
  const left = await leftoverChinese(page)
  left.length ? note(`login (en) still Chinese: ${left.join(' | ')}`) : ok('login (en) fully translated')
  if ((await page.evaluate(() => document.documentElement.lang)) !== 'en') {
    note('login (en) did not set <html lang="en">')
  }
  // Back to Chinese: the form must return to the authored copy, not to blanks.
  await page.getByRole('button', { name: /switch to chinese/i }).click()
  await page.waitForTimeout(500)
  await page.screenshot({ path: `${outDir}/02b-login-back-zh.png` })
  const back = await page.evaluate(() => ({
    text: document.body.innerText,
    placeholders: [...document.querySelectorAll('input')].map((i) => i.placeholder)
  }))
  // antd inserts a space between two CJK glyphs unless autoInsertSpace is off,
  // so compare with whitespace stripped.
  const restored =
    back.placeholders.includes('用户名') &&
    back.placeholders.includes('密码') &&
    back.text.replace(/\s+/g, '').includes('登录')
  if (!restored) {
    note(`switching back to Chinese did not restore the form: ${JSON.stringify(back).slice(0, 260)}`)
  } else ok('round-trip zh → en → zh restores labels')
}

// ---- login ---------------------------------------------------------------
await page.fill('input[autocomplete="username"]', USER)
await page.fill('input[autocomplete="current-password"]', PASS)
await page.click('button[type="submit"]')
await page.waitForURL((u) => !u.pathname.endsWith('/login'), { timeout: 15000 })
await page.waitForTimeout(1200)

// ---- header: language button + a single account name ---------------------
const zhHeader = await headerText(page)
await page.screenshot({ path: `${outDir}/03-header-zh.png`, clip: { x: 900, y: 0, width: 540, height: 60 } })

const adminHits = (zhHeader.text.match(/admin/gi) || []).length
adminHits > 1
  ? note(`header repeats the account name: "${zhHeader.text}"`)
  : ok(`header names the account once: "${zhHeader.text}"`)
if (!/管理员|用户|只读/.test(zhHeader.text)) note('header does not show a role label')
if (zhHeader.skipped !== 'EN') note(`language button (zh) shows "${zhHeader.skipped}", expected EN`)

const headerToggle = page.locator('.ant-layout-header').getByRole('button', { name: /switch to english/i })
if ((await headerToggle.count()) === 0) note('header has no language toggle')
else {
  await headerToggle.click()
  await page.waitForTimeout(600)
  const enHeader = await headerText(page)
  await page.screenshot({ path: `${outDir}/04-header-en.png`, clip: { x: 900, y: 0, width: 540, height: 60 } })
  const headerHasZh = /[\u4e00-\u9fff]/.test(enHeader.text)
  if (headerHasZh) note(`header (en) still Chinese: "${enHeader.text}"`)
  else ok(`header (en): "${enHeader.text}" (button: ${enHeader.skipped})`)
  if ((await page.evaluate(() => localStorage.getItem('wormgate.lang'))) !== 'en') {
    note('language choice is not persisted to localStorage')
  }
}

// ---- every route in English ---------------------------------------------
let i = 5
for (const [name, route] of ROUTES) {
  await page.goto(`${BASE}${route}`, { waitUntil: 'domcontentloaded' })
  const deadline = Date.now() + 9000
  while (Date.now() < deadline) {
    const busy = await page.evaluate(() => !!document.querySelector('.ant-skeleton, .ant-spin-spinning'))
    if (!busy) break
    await page.waitForTimeout(120)
  }
  await page.waitForTimeout(name === 'dashboard' ? 4200 : 700)
  await page.screenshot({ path: `${outDir}/${String(i).padStart(2, '0')}-${name}-en.png`, fullPage: true })
  i += 1
  const left = await leftoverChinese(page)
  left.length ? note(`${name} (en) still Chinese: ${left.slice(0, 6).join(' | ')}`) : ok(`${name} (en) clean`)
  const glued = await gluedSentences(page)
  glued.length ? note(`${name} (en) glued words: ${glued.join(' | ')}`) : ok(`${name} (en) spacing ok`)
}

// ---- switching back must not leave English behind ------------------------
await page.goto(`${BASE}/next/`, { waitUntil: 'domcontentloaded' })
await page.waitForTimeout(1500)
await page.locator('.ant-layout-header').getByRole('button', { name: /switch to chinese/i }).click()
await page.waitForTimeout(700)
await page.screenshot({ path: `${outDir}/99-dashboard-back-to-zh.png`, fullPage: true })
if ((await page.evaluate(() => localStorage.getItem('wormgate.lang'))) !== 'zh') {
  note('switching back to Chinese was not persisted')
} else ok('back to Chinese, persisted')

await browser.close()
console.log(problems.length ? `\n${problems.length} problem(s)` : '\nall checks passed')
process.exit(problems.length ? 1 : 0)
