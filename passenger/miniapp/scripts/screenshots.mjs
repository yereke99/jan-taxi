#!/usr/bin/env node
// Development tool: screenshots of the real Mini Apps (presentation build) for
// docs/app-screenshots/. Uses headless Google Chrome over the DevTools
// Protocol (Node's built-in WebSocket); no npm dependencies.
//
//   pnpm --filter @jantaxi/passenger-miniapp build:demo
//   pnpm --filter @jantaxi/driver-miniapp build:demo
//   node passenger/miniapp/scripts/screenshots.mjs              all screens + overview
//   node passenger/miniapp/scripts/screenshots.mjs 01 05        only some screens
//   node passenger/miniapp/scripts/screenshots.mjs --maps       map walk-through check
//
// The script serves both dist-demo folders with `vite preview` (ports 4173 and
// 4174), opens every screen at 393×852 @3x (iPhone 15/16 Pro), waits for
// window.__jtReady() (fonts, map tiles, camera at rest) and draws the device
// status bar and the Telegram header buttons on top, as on a real phone.
// CHROME=/path/to/chrome overrides the browser.

import { spawn } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '../../..')
const OUT = join(repo, 'docs/app-screenshots')
const CHROME = process.env.CHROME ?? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
const DEBUG_PORT = 9333
const W = 393
const H = 852
const SCALE = 3

const P = 'http://localhost:4173/passenger/'
const D = 'http://localhost:4174/driver/'

/** The eight screens: file name, Kazakh caption for the overview, URL. */
const SHOTS = [
  { id: '01', name: '01-passenger-home', caption: 'Басты бет', url: `${P}?demo=home` },
  { id: '02', name: '02-passenger-route', caption: 'Маршрут', url: `${P}?demo=route` },
  { id: '03', name: '03-passenger-confirm', caption: 'Тапсырысты растау', url: `${P}?demo=confirm` },
  { id: '04', name: '04-passenger-searching', caption: 'Жүргізуші іздеу', url: `${P}?demo=searching&advance=0` },
  { id: '05', name: '05-passenger-driver-found', caption: 'Жүргізуші табылды', url: `${P}?demo=found` },
  { id: '06', name: '06-driver-orders', caption: 'Жүргізуші: тапсырыстар', url: `${D}?demo=orders` },
  { id: '07', name: '07-driver-order-detail', caption: 'Жүргізуші: тапсырыс', url: `${D}?demo=order` },
  { id: '08', name: '08-driver-wallet-topup', caption: 'Жүргізуші: баланс толтыру', url: `${D}?demo=topup` },
]

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// --- Processes ---------------------------------------------------------------

const children = []

function start(cmd, args, opts = {}) {
  const child = spawn(cmd, args, { stdio: ['ignore', 'pipe', 'pipe'], ...opts })
  children.push(child)
  return child
}

function stopAll() {
  for (const c of children) if (!c.killed) c.kill('SIGTERM')
}
process.on('exit', stopAll)
process.on('SIGINT', () => process.exit(130))

async function waitHttp(url, timeoutMs = 30_000) {
  const until = Date.now() + timeoutMs
  while (Date.now() < until) {
    try {
      const res = await fetch(url)
      if (res.ok) return res
    } catch {
      // not up yet
    }
    await sleep(250)
  }
  throw new Error(`timed out waiting for ${url}`)
}

function preview(app, port) {
  const root = join(repo, app, 'miniapp')
  start(join(root, 'node_modules/.bin/vite'), ['preview', '--outDir', 'dist-demo', '--port', String(port), '--strictPort'], { cwd: root })
}

// --- DevTools Protocol -----------------------------------------------------------

class Cdp {
  constructor(ws) {
    this.ws = ws
    this.id = 0
    this.pending = new Map()
    this.listeners = new Set()
    ws.addEventListener('message', (ev) => {
      const msg = JSON.parse(String(ev.data))
      if (msg.id && this.pending.has(msg.id)) {
        const { resolve: ok, reject } = this.pending.get(msg.id)
        this.pending.delete(msg.id)
        if (msg.error) reject(new Error(`${msg.error.message} (${msg.error.code})`))
        else ok(msg.result)
      } else {
        for (const l of this.listeners) l(msg)
      }
    })
  }

  static async connect(url) {
    const ws = new WebSocket(url)
    await new Promise((ok, fail) => {
      ws.addEventListener('open', ok, { once: true })
      ws.addEventListener('error', fail, { once: true })
    })
    return new Cdp(ws)
  }

  send(method, params = {}, sessionId) {
    const id = ++this.id
    this.ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }))
    return new Promise((ok, reject) => this.pending.set(id, { resolve: ok, reject }))
  }
}

async function openPage(cdp, { width = W, height = H, scale = SCALE, mobile = true } = {}) {
  const { targetId } = await cdp.send('Target.createTarget', { url: 'about:blank' })
  const { sessionId } = await cdp.send('Target.attachToTarget', { targetId, flatten: true })
  const s = (m, p) => cdp.send(m, p, sessionId)
  await s('Page.enable')
  await s('Runtime.enable')
  await s('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: scale, mobile })
  if (mobile) {
    await s('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 5 })
    await s('Emulation.setUserAgentOverride', {
      userAgent:
        'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 Telegram-iOS',
    })
  }
  const logs = []
  cdp.listeners.add((msg) => {
    if (msg.sessionId !== sessionId) return
    if (msg.method === 'Runtime.consoleAPICalled' && ['error', 'warning'].includes(msg.params.type)) {
      logs.push(`${msg.params.type}: ${msg.params.args.map((a) => a.value ?? a.description ?? '').join(' ')}`)
    }
    if (msg.method === 'Runtime.exceptionThrown') logs.push(`exception: ${msg.params.exceptionDetails.exception?.description ?? msg.params.exceptionDetails.text}`)
  })
  return {
    s,
    logs,
    async eval(expression) {
      const r = await s('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })
      if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? r.exceptionDetails.text)
      return r.result.value
    },
    async goto(url) {
      await s('Page.navigate', { url })
    },
    close: () => cdp.send('Target.closeTarget', { targetId }),
  }
}

async function waitReady(page, timeoutMs = 45_000) {
  const until = Date.now() + timeoutMs
  let stable = 0
  while (Date.now() < until) {
    const ok = await page.eval('typeof window.__jtReady === "function" && window.__jtReady()').catch(() => false)
    stable = ok ? stable + 1 : 0
    if (stable >= 3) return true
    await sleep(300)
  }
  return false
}

/** What window.__jtReady() is waiting for. */
const DIAGNOSE = `(() => {
  const m = window.__jtMap
  return {
    fonts: document.fonts.status,
    app: !!document.querySelector('#app > *'),
    busy: [...document.querySelectorAll('.jt-skeleton, [aria-busy="true"]')].map((e) => e.className).slice(0, 4),
    map: m ? { loaded: m.loaded(), tiles: m.areTilesLoaded(), moving: m.isMoving(), zoom: m.getZoom(), center: m.getCenter() } : null,
    path: location.pathname + location.search,
  }
})()`

// --- Device chrome (status bar and Telegram header buttons) -----------------------------

const CHROME_OVERLAY = `(() => {
  if (document.getElementById('jt-device-chrome')) return
  const el = document.createElement('div')
  el.id = 'jt-device-chrome'
  el.setAttribute('aria-hidden', 'true')
  el.innerHTML = \`
<style>
#jt-device-chrome { position: fixed; inset: 0; z-index: 2147483647; pointer-events: none;
  font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif; color: #000; }
#jt-device-chrome .sb-time { position: absolute; left: 54px; top: 17px; width: 54px; text-align: center;
  font: 600 17px/22px -apple-system, "SF Pro Text", sans-serif; letter-spacing: -0.4px; }
#jt-device-chrome .sb-right { position: absolute; right: 30px; top: 22px; display: flex; gap: 6px; align-items: center; }
#jt-device-chrome .tg { position: absolute; top: 66px; height: 31px; border-radius: 999px; display: flex; align-items: center;
  justify-content: center; gap: 24px; background: rgba(255,255,255,.78); -webkit-backdrop-filter: blur(20px) saturate(180%);
  backdrop-filter: blur(20px) saturate(180%); box-shadow: 0 4px 16px rgba(0,0,0,.10), inset 0 0.5px 0 rgba(255,255,255,.9); }
#jt-device-chrome .tg-left { left: 16px; width: 33px; }
#jt-device-chrome .tg-back { width: auto; gap: 6px; padding: 0 12px 0 10px; font: 500 15px/20px -apple-system, "SF Pro Text", sans-serif; }
#jt-device-chrome .tg-right { right: 16px; width: 68px; }
#jt-device-chrome .home { position: absolute; left: 50%; bottom: 8px; width: 139px; height: 5px; margin-left: -69.5px;
  border-radius: 3px; background: #000; }
</style>
<div class="sb-time">9:41</div>
<div class="sb-right">
  <svg width="18" height="12" viewBox="0 0 18 12"><rect x="0" y="8" width="3" height="4" rx="1"/><rect x="5" y="5.5" width="3" height="6.5" rx="1"/><rect x="10" y="3" width="3" height="9" rx="1"/><rect x="15" y="0" width="3" height="12" rx="1"/></svg>
  <svg width="16" height="12" viewBox="0 0 16 12"><path d="M8 2.6c2.3 0 4.4.9 6 2.4l1.1-1.1A10 10 0 0 0 8 1 10 10 0 0 0 .9 3.9L2 5c1.6-1.5 3.7-2.4 6-2.4Zm0 3.2c1.4 0 2.7.5 3.7 1.4l1.1-1.1A7 7 0 0 0 8 4.2a7 7 0 0 0-4.8 1.9l1.1 1.1c1-.9 2.3-1.4 3.7-1.4Zm0 3.2c.6 0 1.1.2 1.5.6L8 11.1 6.5 9.6c.4-.4.9-.6 1.5-.6Z"/></svg>
  <svg width="27" height="13" viewBox="0 0 27 13"><rect x="0.5" y="0.5" width="22" height="12" rx="3.5" fill="none" stroke="#000" stroke-opacity=".4"/><rect x="2" y="2" width="19" height="9" rx="2"/><path d="M24 4.5v4c.8-.3 1.3-1.1 1.3-2s-.5-1.7-1.3-2Z" fill-opacity=".45"/></svg>
</div>
\${document.documentElement.hasAttribute('data-demo-tg-back')
  ? '<div class="tg tg-left tg-back"><svg width="8" height="13" viewBox="0 0 8 13"><path d="M6.5 1.5l-5 5 5 5" fill="none" stroke="#000" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg><span>' + (document.documentElement.lang === 'ru' ? 'Назад' : document.documentElement.lang === 'en' ? 'Back' : 'Артқа') + '</span></div>'
  : '<div class="tg tg-left"><svg width="12" height="12" viewBox="0 0 12 12"><path d="M1.5 1.5l9 9M10.5 1.5l-9 9" stroke="#000" stroke-width="1.6" stroke-linecap="round"/></svg></div>'}
<div class="tg tg-right"><svg width="13" height="8" viewBox="0 0 13 8"><path d="M1.5 1.5l5 5 5-5" fill="none" stroke="#000" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/></svg><svg width="14" height="4" viewBox="0 0 14 4"><circle cx="2" cy="2" r="1.6"/><circle cx="7" cy="2" r="1.6"/><circle cx="12" cy="2" r="1.6"/></svg></div>
<div class="home"></div>\`
  document.body.appendChild(el)
})()`

// --- Screens ----------------------------------------------------------------------

async function shoot(cdp, shot) {
  const page = await openPage(cdp)
  await page.goto(shot.url)
  const ready = await waitReady(page)
  if (!ready) page.logs.push(`not ready: ${JSON.stringify(await page.eval(DIAGNOSE).catch((e) => e.message))}`)
  await page.eval(CHROME_OVERLAY)
  await sleep(250)
  const { data } = await page.s('Page.captureScreenshot', { format: 'png' })
  writeFileSync(join(OUT, `${shot.name}.png`), Buffer.from(data, 'base64'))
  console.log(`${ready ? 'ok  ' : 'WAIT'} ${shot.name}.png${page.logs.length ? `\n     ${page.logs.slice(0, 6).join('\n     ')}` : ''}`)
  await page.close()
}

async function overview(cdp) {
  const cards = SHOTS.map(
    (s) => `<figure><div class="phone"><img src="file://${join(OUT, `${s.name}.png`)}"></div><figcaption><b>${s.id}</b> ${s.caption}</figcaption></figure>`,
  ).join('')
  const html = `<!doctype html><meta charset="utf-8"><style>
  body { margin: 0; background: #F2F2F7; font-family: -apple-system, BlinkMacSystemFont, "SF Pro Display", sans-serif; color: #000; }
  header { padding: 72px 96px 24px; } h1 { margin: 0; font-size: 64px; font-weight: 700; letter-spacing: -1px; }
  p { margin: 12px 0 0; font-size: 28px; color: rgba(60,60,67,.6); }
  main { display: grid; grid-template-columns: repeat(4, 1fr); gap: 56px 48px; padding: 32px 96px 96px; }
  figure { margin: 0; display: flex; flex-direction: column; align-items: center; gap: 24px; }
  .phone { width: 100%; aspect-ratio: 393 / 852; border-radius: 64px; padding: 12px; background: #111; box-shadow: 0 24px 60px rgba(0,0,0,.18); box-sizing: border-box; }
  .phone img { width: 100%; height: 100%; border-radius: 52px; display: block; object-fit: cover; }
  figcaption { font-size: 28px; font-weight: 600; } figcaption b { color: rgba(60,60,67,.6); font-weight: 600; margin-right: 6px; }
  </style><header><h1>Jan Taxi — Telegram Mini App</h1><p>Жолаушы және жүргізуші қосымшалары · Ембі</p></header><main>${cards}</main>`
  const file = join(mkdtempSync(join(tmpdir(), 'jt-overview-')), 'overview.html')
  writeFileSync(file, html)
  const page = await openPage(cdp, { width: 3200, height: 3200, scale: 1, mobile: false })
  await page.goto(`file://${file}`)
  await sleep(1500)
  const height = await page.eval('Math.ceil(document.documentElement.scrollHeight)')
  await page.s('Emulation.setDeviceMetricsOverride', { width: 3200, height, deviceScaleFactor: 1, mobile: false })
  await sleep(300)
  const { data } = await page.s('Page.captureScreenshot', { format: 'png' })
  writeFileSync(join(OUT, '00-overview.png'), Buffer.from(data, 'base64'))
  console.log('ok   00-overview.png')
  await page.close()
}

/**
 * Walks every map screen in one tab, the way a user does (including going
 * back), and reports per step whether tiles and the expected layers are on
 * the map. Captures go to $TMPDIR/jt-map-walk.
 */
async function mapWalk(cdp) {
  const dir = mkdtempSync(join(tmpdir(), 'jt-map-walk-'))
  const probe = `(() => {
    const m = window.__jtMap
    const style = m?.getStyle()
    if (!m || !style) return { map: false, path: location.pathname }
    const layers = style.layers.map((l) => l.id)
    const c = m.getCanvas()
    return {
      map: true,
      canvas: c.width + 'x' + c.height,
      tiles: m.areTilesLoaded(),
      route: m.getSource('jt-route') ? m.querySourceFeatures('jt-route').length : 0,
      approach: m.getSource('jt-approach') ? m.querySourceFeatures('jt-approach').length : 0,
      layers: layers.filter((id) => id.startsWith('jt-')).length,
      stops: document.querySelectorAll('.jt-stop-marker').length,
      car: document.querySelectorAll('.jt-car-marker').length,
      user: document.querySelectorAll('.jt-user-location').length,
      pin: !!document.querySelector('.jt-center-pin'),
      path: location.pathname,
    }
  })()`
  const steps = [
    { app: 'passenger', label: 'home', url: `${P}?demo=home` },
    { label: 'route editor', run: `document.querySelector('.home-where')?.click()` },
    { label: 'back to home', run: 'history.back()' },
    { label: 'route (A–D)', url: `${P}?demo=route` },
    { label: 'pick on map (B)', run: `window.__jtRouter?.push({ name: 'book-pick', params: { index: '1' } })` },
    { label: 'back to route', run: 'history.back()' },
    { label: 'confirm', run: `window.__jtRouter?.push({ name: 'confirm' })` },
    { label: 'back to route', run: 'history.back()' },
    { label: 'searching', url: `${P}?demo=searching&advance=6` },
    { label: 'driver found (timer)', wait: 9000 },
    { label: 'home tab', run: `window.__jtRouter?.push({ name: 'home' })` },
    { label: 'trip tab', run: 'history.back()' },
    { app: 'driver', label: 'driver order detail', url: `${D}?demo=order` },
    { label: 'driver orders', run: `window.__jtRouter?.push({ name: 'orders' })` },
    { label: 'driver order detail again', run: 'history.back()' },
  ]
  const page = await openPage(cdp)
  const report = []
  let n = 0
  for (const step of steps) {
    if (step.url) await page.goto(step.url)
    if (step.run) await page.eval(step.run).catch((e) => report.push(`  ! ${step.label}: ${e.message}`))
    if (step.wait) await sleep(step.wait)
    await sleep(600)
    const ready = await waitReady(page, 30_000)
    const r = await page.eval(probe)
    const { data } = await page.s('Page.captureScreenshot', { format: 'png' })
    const file = join(dir, `${String(++n).padStart(2, '0')}-${step.label.replace(/[^a-z0-9]+/gi, '-')}.png`)
    writeFileSync(file, Buffer.from(data, 'base64'))
    report.push(`${ready ? 'ok  ' : 'WAIT'} ${step.label.padEnd(28)} ${JSON.stringify(r)}`)
  }
  await page.close()
  console.log(report.join('\n'))
  if (page.logs.length) console.log(page.logs.slice(0, 12).join('\n'))
  console.log(`captures: ${dir}`)
}

// --- Main -----------------------------------------------------------------------------

async function main() {
  const args = process.argv.slice(2)
  const only = args.filter((a) => /^\d\d$/.test(a))
  mkdirSync(OUT, { recursive: true })

  preview('passenger', 4173)
  preview('driver', 4174)
  await waitHttp(P)
  await waitHttp(D)

  const profile = mkdtempSync(join(tmpdir(), 'jt-chrome-'))
  start(CHROME, [
    '--headless=new',
    `--remote-debugging-port=${DEBUG_PORT}`,
    '--use-angle=swiftshader',
    '--enable-unsafe-swiftshader',
    '--hide-scrollbars',
    '--no-first-run',
    '--no-default-browser-check',
    '--allow-file-access-from-files',
    `--user-data-dir=${profile}`,
    `--window-size=${W},${H}`,
  ])
  const version = await (await waitHttp(`http://127.0.0.1:${DEBUG_PORT}/json/version`)).json()
  const cdp = await Cdp.connect(version.webSocketDebuggerUrl)

  try {
    if (args.includes('--maps')) {
      await mapWalk(cdp)
      return
    }
    for (const shot of SHOTS) if (only.length === 0 || only.includes(shot.id)) await shoot(cdp, shot)
    if (only.length === 0 || args.includes('--overview')) await overview(cdp)
  } finally {
    await cdp.send('Browser.close').catch(() => undefined)
    await sleep(300)
    try {
      rmSync(profile, { recursive: true, force: true })
    } catch {
      // Chrome may still be writing its profile; the OS cleans the temp dir.
    }
  }
}

main()
  .then(() => process.exit(0))
  .catch((err) => {
    console.error(err)
    process.exit(1)
  })
