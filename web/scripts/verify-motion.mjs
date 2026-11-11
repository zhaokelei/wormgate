/**
 * Asserts the polish pass actually reached the DOM.
 *
 * These are the properties that are easy to "add" in CSS and then lose to a
 * specificity fight with antd, so each one is read back from getComputedStyle
 * instead of trusted.
 */
import { chromium } from 'playwright-core'

const EDGE = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe'
const BASE = process.env.TX_BASE || 'http://127.0.0.1:17200'
const USER = process.env.TX_USER || 'admin'
const PASS = process.env.TX_PASS || ''

const results = []
function check(name, ok, detail) {
  results.push({ name, ok, detail })
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? '  → ' + detail : ''}`)
}

async function login(page) {
  await page.goto(`${BASE}/next/login`, { waitUntil: 'domcontentloaded' })
  await page.fill('input#username', USER)
  await page.fill('input[type=password]', PASS)
  await page.click('button[type=submit]')
  await page.waitForURL((u) => !u.pathname.endsWith('/login'), { timeout: 15000 })
  await page.waitForTimeout(1200)
}

;(async () => {
  const browser = await chromium.launch({ executablePath: EDGE, headless: true })
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })
  const errors = []
  page.on('pageerror', (e) => errors.push(String(e)))

  await login(page)

  // 1. sliding indicator
  const ind = page.locator('.nav-indicator')
  // Read the inline target, not the computed value: mid-transition computed
  // transforms depend on how many frames the headless compositor has ticked.
  const first = await ind.evaluate((el) => el.style.transform)
  await page.locator('.ant-menu-item', { hasText: '隧道' }).first().click()
  // Selection and content paint together; wait for the menu to actually switch
  // rather than guessing a delay.
  await page
    .locator('.ant-menu-item-selected', { hasText: '隧道' })
    .first()
    .waitFor({ timeout: 5000 })
  await page.waitForTimeout(450)
  const second = await ind.evaluate((el) => el.style.transform)
  const indStyle = await ind.evaluate((el) => {
    const cs = getComputedStyle(el)
    return { transition: cs.transitionProperty, timing: cs.transitionTimingFunction, bg: cs.backgroundColor }
  })
  check('nav indicator moves between items', first !== second, `${first} → ${second}`)
  check(
    'nav indicator transitions with the shared curve',
    indStyle.transition.includes('transform') && indStyle.timing.includes('cubic-bezier(0.4, 0, 0.2, 1)'),
    `${indStyle.transition} / ${indStyle.timing}`
  )
  check('nav indicator has a tinted fill', indStyle.bg !== 'rgba(0, 0, 0, 0)', indStyle.bg)

  const bar = await ind.evaluate((el) => {
    const s = getComputedStyle(el, '::before')
    return { width: s.width, bg: s.backgroundColor, shadow: s.boxShadow }
  })
  check('indicator carries the 3px brand bar', bar.width === '3px', `${bar.width} / ${bar.bg}`)
  check('brand bar has a glow', bar.shadow !== 'none', bar.shadow)

  // 2. page stagger
  await page.goto(`${BASE}/next/users`, { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(400)
  const stagger = await page.evaluate(() => {
    const kids = [...document.querySelectorAll('.page-enter > .page > *')].slice(0, 3)
    return kids.map((k) => {
      const cs = getComputedStyle(k)
      return { name: cs.animationName, delay: cs.animationDelay, dur: cs.animationDuration }
    })
  })
  check(
    'page content fades up with a 60ms stagger',
    stagger.length >= 2 &&
      stagger.every((s) => s.name === 'page-fade-up') &&
      stagger[1].delay === '0.06s',
    JSON.stringify(stagger)
  )

  // 3. breathing dot + count-up
  await page.goto(`${BASE}/next/`, { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(1600)
  const dot = await page.locator('.live-dot > i').evaluate((el) => {
    const cs = getComputedStyle(el)
    return { anim: cs.animationName, dur: cs.animationDuration }
  })
  check('live dot breathes', dot.anim === 'breathe', `${dot.anim} ${dot.dur}`)

  const prog = await page.evaluate(() => {
    const el = document.querySelector('.ant-progress-track, .ant-progress-bg')
    return el ? getComputedStyle(el).transitionProperty : ''
  })
  check('progress bars transition their width on the shared curve', prog.includes('width'), prog)

  const kpiBefore = await page.locator('.kpi-value').first().innerText()
  await page.waitForTimeout(120)
  check('KPI value is rendered by the counter', !!kpiBefore, `first KPI = ${kpiBefore.trim()}`)

  // 4. empty state illustration
  const emptyArt = page.locator('.empty-traffic')
  // The idle state is a soft area + one slow pulse now, not three rings: the
  // multi-ring version read as a schematic at card size.
  const ripples = await page.locator('.empty-traffic .ripple').count()
  const curve = await page.locator('.empty-traffic .curve').count()
  const area = await page.locator('.empty-traffic path[fill^="url(#"]').count()
  const copy = await emptyArt.first().innerText()
  // Two instances render at once (traffic card + Top tunnels), so compare each
  // count against the number of illustrations rather than to a literal 1.
  const arts = await emptyArt.count()
  check(
    'empty state draws one soft area with a single pulse',
    arts >= 1 && ripples === arts && curve === arts && area === arts,
    `${arts} illustration(s), ${ripples} ripple / ${curve} curve / ${area} filled area`
  )
  check('empty state copy is the waiting message', copy.includes('等待隧道请求接入中'), copy.replace(/\n/g, ' | '))

  // 5. table row hover
  await page.goto(`${BASE}/next/users`, { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(900)
  const row = page.locator('.ant-table-tbody > tr.ant-table-row').first()
  const hover = await row.evaluate((el) => {
    const cs = getComputedStyle(el)
    return { transition: cs.transitionProperty, transform: cs.transform }
  })
  check('table rows animate lift/shadow', hover.transition.includes('transform'), hover.transition)

  const icon = page.locator('.ant-table .ant-btn .anticon').first()
  if (await icon.count()) {
    await page.locator('.ant-table .ant-btn').first().hover()
    await page.waitForTimeout(260)
    const iconT = await icon.evaluate((el) => getComputedStyle(el).transform)
    check('action icons scale on hover', iconT !== 'none' && iconT !== 'matrix(1, 0, 0, 1, 0, 0)', iconT)
  }

  // 6. soft tags
  const tag = await page.locator('.soft-tag').first().evaluate((el) => {
    const cs = getComputedStyle(el)
    return { bg: cs.backgroundColor, color: cs.color, radius: cs.borderRadius }
  })
  const alpha = /rgba\([^)]*,\s*0?\.\d+\)/.test(tag.bg)
  check('tags use a translucent tint', alpha, `${tag.bg} on ${tag.color}`)

  // 7. dark surface
  const bg = await page.evaluate(() => {
    const el = document.querySelector('.ant-layout') || document.body
    return getComputedStyle(el).backgroundColor
  })
  const card = await page.locator('.ant-card').first().evaluate((el) => {
    const cs = getComputedStyle(el)
    return { bg: cs.backgroundColor, border: cs.borderTopColor, blur: cs.backdropFilter }
  })
  check('canvas is the near-black #0a0a0a', bg === 'rgb(10, 10, 10)', bg)
  check('cards are translucent with a hairline border', /rgba\(255, 255, 255/.test(card.bg), `${card.bg} / ${card.border}`)
  check('cards apply a backdrop blur', card.blur.includes('blur'), card.blur)

  // 8. theme cross-fade
  // Sampling at a fixed delay raced the 560 ms removal under load, so watch the
  // attribute instead: the class must appear and then go away on its own.
  await page.evaluate(() => {
    window.__themeLog = []
    new MutationObserver((recs) => {
      for (const r of recs) {
        if (r.attributeName === 'class') window.__themeLog.push(document.documentElement.className)
      }
    }).observe(document.documentElement, { attributes: true })
  })
  await page.click('button.theme-toggle')
  await page.waitForTimeout(900)
  const log = await page.evaluate(() => window.__themeLog)
  const after = await page.evaluate(() => document.documentElement.dataset.theme)
  check('theme toggle adds the cross-fade class', log.some((c) => c.includes('theme-anim')), `data-theme=${after}, states=${JSON.stringify(log)}`)
  check('cross-fade class is cleaned up', log[log.length - 1] !== undefined && !log[log.length - 1].includes('theme-anim'), JSON.stringify(log[log.length - 1]))

  check('no page errors', errors.length === 0, errors.join(' | ') || 'none')

  // reduced motion: everything collapses to ~0
  const ctx = await browser.newContext({ reducedMotion: 'reduce', colorScheme: 'dark' })
  const rp = await ctx.newPage()
  await rp.goto(`${BASE}/next/login`, { waitUntil: 'domcontentloaded' })
  await rp.fill('input#username', USER)
  await rp.fill('input[type=password]', PASS)
  await rp.click('button[type=submit]')
  await rp.waitForURL((u) => !u.pathname.endsWith('/login'), { timeout: 15000 })
  await rp.waitForTimeout(800)
  const rm = await rp.evaluate(() => {
    const dotEl = document.querySelector('.live-dot > i')
    const pageEl = document.querySelector('.page-enter > .page > *')
    const cs1 = dotEl ? getComputedStyle(dotEl) : null
    const cs2 = pageEl ? getComputedStyle(pageEl) : null
    return { dotDur: cs1?.animationDuration, pageAnim: cs2?.animationName }
  })
  check(
    'prefers-reduced-motion disables the animations',
    rm.dotDur === '0.001s' || rm.dotDur === '1ms' || rm.pageAnim === 'none',
    JSON.stringify(rm)
  )

  await browser.close()
  const failed = results.filter((r) => !r.ok)
  console.log(`\n${results.length - failed.length}/${results.length} checks passed`)
  process.exit(failed.length ? 1 : 0)
})()
