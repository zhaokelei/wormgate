import { chromium } from 'playwright-core'
const [out, url, scheme = 'light', width = '1280'] = process.argv.slice(2)
const b = await chromium.launch({ channel: 'msedge', headless: true })
const ctx = await b.newContext({ viewport: { width: +width, height: 900 }, deviceScaleFactor: 2, colorScheme: scheme })
const p = await ctx.newPage()
const errs = []
p.on('pageerror', (e) => errs.push('pageerror: ' + e.message))
p.on('console', (m) => { if (m.type() === 'error') errs.push('console: ' + m.text().slice(0, 160)) })
await p.goto(url, { waitUntil: 'load' })
await p.waitForTimeout(2600)
const state = await p.evaluate(() => ({
  pill: document.getElementById('pill-text')?.textContent,
  state: document.getElementById('pill')?.getAttribute('data-state'),
  rtt: document.getElementById('rtt')?.textContent,
  origin: document.getElementById('origin-time')?.textContent,
  server: document.getElementById('f-server')?.textContent,
  hits: document.getElementById('hits')?.textContent,
  tunnel: document.getElementById('f-tunnel')?.textContent,
  hop2: document.getElementById('c2')?.textContent,
  current: document.querySelector('#switch a.on b')?.textContent
}))
await p.screenshot({ path: out, fullPage: true })
console.log(JSON.stringify({ url, ...state, errs }, null, 1))
await b.close()
