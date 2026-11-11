/**
 * Screenshot harness for the two embedded consoles.
 *
 * Drives the locally running Wormgate demo through the system Edge/Chrome rather
 * than downloading a browser: playwright-core + channel detection keeps the
 * toolchain small and works offline.
 *
 * Usage:
 *   node scripts/shoot.mjs            # capture every view
 *   node scripts/shoot.mjs dark       # dark theme only
 *
 * Output: ../docs/screenshots/*.png
 */
import { chromium } from 'playwright-core'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const here = path.dirname(fileURLToPath(import.meta.url))
const outDir = path.resolve(here, '../../docs/screenshots')

// Endpoints of the local demo (see the README "快速开始" section).
const CLIENT_URL = process.env.TX_CLIENT_URL || 'http://127.0.0.1:7200'
const ADMIN_URL = process.env.TX_ADMIN_URL || 'http://127.0.0.1:7100/admin'
const CLIENT_TOKEN = process.env.TX_CLIENT_TOKEN || ''
const ADMIN_USER = process.env.TX_ADMIN_USER || 'admin'
const ADMIN_PASS = process.env.TX_ADMIN_PASS || ''

// Edge/Chrome channels are tried in order; the first installed one wins.
const CHANNELS = ['msedge', 'chrome']

const only = process.argv[2] // optional: 'dark' | 'light'

async function launch() {
  for (const channel of CHANNELS) {
    try {
      return await chromium.launch({ channel, headless: true })
    } catch {
      // try the next channel
    }
  }
  throw new Error('no Edge/Chrome channel available for playwright-core')
}

/** Wait until animations settle and the realtime socket has pushed data. */
async function settle(page, ms = 1600) {
  await page.waitForLoadState('networkidle').catch(() => {})
  await page.waitForTimeout(ms)
}

async function newContext(browser, theme) {
  const ctx = await browser.newContext({
    viewport: { width: 1600, height: 1000 },
    deviceScaleFactor: 2,
    colorScheme: theme
  })
  // Seed the persisted theme before any script runs (same key as theme.js).
  await ctx.addInitScript((t) => {
    try {
      localStorage.setItem('tunnelx.theme', t)
    } catch {
      /* storage disabled */
    }
  }, theme)
  return ctx
}

async function shot(page, name) {
  const file = path.join(outDir, `${name}.png`)
  await page.screenshot({ path: file, fullPage: false })
  console.log('  saved', path.relative(process.cwd(), file))
}

async function captureClient(browser, theme) {
  const ctx = await newContext(browser, theme)
  const page = await ctx.newPage()

  // --- login -------------------------------------------------------------
  await page.goto(`${CLIENT_URL}/login`, { waitUntil: 'domcontentloaded' })
  await settle(page, 600)
  await shot(page, `client-${theme}-01-login`)

  await page.fill('input[type="password"], input.mono', CLIENT_TOKEN)
  await Promise.all([page.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 15000 }), page.click('button[type="submit"]')])
  await settle(page, 2600)
  await shot(page, `client-${theme}-02-dashboard`)

  // --- tunnels -----------------------------------------------------------
  await page.goto(`${CLIENT_URL}/tunnels`)
  await settle(page, 1500)
  await shot(page, `client-${theme}-03-tunnels`)

  // Create dialog (visual check of the form, then dismiss).
  await page.getByRole('button', { name: /新建隧道/ }).first().click()
  await settle(page, 700)
  await shot(page, `client-${theme}-04-tunnel-form`)
  await page.keyboard.press('Escape')
  await settle(page, 400)

  // --- connections -------------------------------------------------------
  await page.goto(`${CLIENT_URL}/connections`)
  await settle(page, 1500)
  await shot(page, `client-${theme}-05-connections`)

  // --- config ------------------------------------------------------------
  await page.goto(`${CLIENT_URL}/config`)
  await settle(page, 1500)
  await shot(page, `client-${theme}-06-config`)

  // --- logs --------------------------------------------------------------
  await page.goto(`${CLIENT_URL}/logs`)
  await settle(page, 2200)
  await shot(page, `client-${theme}-07-logs`)

  await ctx.close()
}

async function captureAdmin(browser, theme) {
  const ctx = await newContext(browser, theme)
  const page = await ctx.newPage()

  await page.goto(`${ADMIN_URL}/login`, { waitUntil: 'domcontentloaded' })
  await settle(page, 600)
  await shot(page, `admin-${theme}-01-login`)

  await page.fill('input[autocomplete="username"]', ADMIN_USER)
  await page.fill('input[autocomplete="current-password"]', ADMIN_PASS)
  await Promise.all([page.waitForURL((u) => !u.pathname.endsWith('/login'), { timeout: 15000 }), page.click('button[type="submit"]')])
  await settle(page, 2800)
  await shot(page, `admin-${theme}-02-dashboard`)

  for (const [route, name] of [
    ['/tunnels', '03-tunnels'],
    ['/clients', '04-clients'],
    ['/nodes', '05-nodes'],
    ['/users', '06-users'],
    ['/audit', '07-audit'],
    ['/alerts', '08-alerts']
  ]) {
    await page.goto(`${ADMIN_URL}${route}`)
    await settle(page, 1500)
    await shot(page, `admin-${theme}-${name}`)
  }

  // User create dialog (shows the quota editor and one-time token flow).
  await page.goto(`${ADMIN_URL}/users`)
  await settle(page, 1200)
  await page.getByRole('button', { name: /新建用户/ }).first().click()
  await settle(page, 700)
  await shot(page, `admin-${theme}-09-user-form`)
  await page.keyboard.press('Escape')

  await ctx.close()
}

const themes = only ? [only] : ['dark', 'light']

await mkdir(outDir, { recursive: true })
const browser = await launch()
try {
  for (const theme of themes) {
    console.log(`client console (${theme})`)
    await captureClient(browser, theme)
    console.log(`admin console (${theme})`)
    await captureAdmin(browser, theme)
  }
} finally {
  await browser.close()
}
console.log('done')
