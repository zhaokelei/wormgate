/**
 * Production check that does NOT need credentials: both sign-in screens must
 * expose the language toggle and render fully in English after the switch.
 */
import { chromium } from 'playwright-core'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'

const BASE = process.env.TX_BASE || 'http://43.228.85.180:7100'
const out = '.run/prod-login'
await mkdir(out, { recursive: true })

const browser = await chromium.launch({ channel: 'msedge', headless: true })
const page = await (await browser.newContext({ viewport: { width: 1440, height: 900 } })).newPage()

const problems = []
const ok = (m) => console.log('  ok', m)
const bad = (m) => { problems.push(m); console.log('  !', m) }

async function chineseLeft() {
  return page.evaluate(() => {
    const CJK = /[\u4e00-\u9fff]/
    const SKIP = new Set(['SCRIPT', 'STYLE', 'TEXTAREA'])
    const hits = []
    const walk = (n) => {
      if (n.nodeType === 3) {
        const t = (n.nodeValue || '').trim()
        if (t && CJK.test(t) && !SKIP.has(n.parentElement?.tagName ?? '')) hits.push(t)
        return
      }
      if (n.nodeType === 1) {
        if (n.hasAttribute?.('data-i18n-skip')) return
        for (const a of ['placeholder', 'title', 'aria-label']) {
          const v = n.getAttribute?.(a)
          if (v && CJK.test(v)) hits.push(`${a}=${v}`)
        }
      }
      n.childNodes.forEach(walk)
    }
    walk(document.body)
    return [...new Set(hits)]
  })
}

/** English glued at a sentence boundary, e.g. "Need the legacy UI?Open it". */
async function glued() {
  return page.evaluate(() =>
    (document.body.innerText.match(/[?!][A-Za-z][a-z]{2,}/g) || []).slice(0, 5))
}

// ---- React console -------------------------------------------------------
await page.goto(`${BASE}/next/login`, { waitUntil: 'domcontentloaded' })
await page.waitForTimeout(700)
await page.screenshot({ path: `${out}/next-login-zh.png` })

const zhBody = await page.evaluate(() => document.body.innerText)
if (!/登录|用户名/.test(zhBody)) bad(`/next/login is not Chinese by default: ${zhBody.slice(0, 80)}`)
else ok('/next/login defaults to Chinese')

let toggle = page.getByRole('button', { name: /switch to english/i })
if ((await toggle.count()) === 0) bad('/next/login has no language toggle')
else {
  await toggle.first().click()
  await page.waitForTimeout(700)
  const left = await chineseLeft()
  await page.screenshot({ path: `${out}/next-login-en.png` })
  if (left.length) bad(`/next/login still Chinese: ${left.slice(0, 6).join(' | ')}`)
  else ok('/next/login fully English after switch')
  const g = await glued()
  if (g.length) bad(`/next/login has glued words: ${g.join(' | ')}`)
  else ok('/next/login spacing ok')

  // round trip: labels must come back, not blank out
  const back = page.getByRole('button', { name: /switch to chinese/i })
  if ((await back.count()) === 0) bad('cannot switch back to Chinese on /next/login')
  else {
    await back.first().click()
    await page.waitForTimeout(700)
    const ph = await page.evaluate(() =>
      [...document.querySelectorAll('input')].map((i) => i.getAttribute('placeholder') || '').join('|'))
    if (!/用户名/.test(ph)) bad(`round-trip lost placeholders: ${ph}`)
    else ok('round-trip zh → en → zh restores placeholders')
  }
}

// ---- Vue admin console ---------------------------------------------------
await page.goto(`${BASE}/admin/`, { waitUntil: 'domcontentloaded' })
await page.waitForTimeout(900)
await page.screenshot({ path: `${out}/admin-login-zh.png` })
const vzh = await page.evaluate(() => document.body.innerText)
if (!/登录/.test(vzh)) bad(`/admin/ login not rendered: ${vzh.slice(0, 80)}`)
else ok('/admin/ login renders')

const vToggle = page.locator('.wg-lang, [data-i18n-toggle]').filter({ hasText: /EN|中/ })
if ((await vToggle.count()) === 0) bad('/admin/ login has no language toggle')
else {
  await vToggle.first().click()
  await page.waitForTimeout(700)
  const left = await chineseLeft()
  await page.screenshot({ path: `${out}/admin-login-en.png` })
  if (left.length) bad(`/admin/ login still Chinese: ${left.slice(0, 6).join(' | ')}`)
  else ok('/admin/ login fully English after switch')
}

await browser.close()
console.log(problems.length ? `\nFAIL ${problems.length}` : '\nPASS')
process.exit(problems.length ? 1 : 0)
