// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Presentation build runtime (VITE_DEMO=1, `pnpm dev:demo` / `pnpm build:demo`).
// main.ts imports the demo folder only behind `import.meta.env.VITE_DEMO === '1'`,
// so production bundles never contain this file. It lets the real app run in a
// plain browser, without Telegram and without the API:
//
//   * a mocked Telegram client (mockTelegramEnv): iOS platform, white theme,
//     fullscreen with iPhone safe areas (top 59 / bottom 34) on phone-sized
//     viewports, LocationManager answering with the simulated device position;
//   * a simulated device near Embi that moves a little every second, served
//     through both navigator.geolocation and the Telegram LocationManager;
//   * an in-memory API: the HTTP client's transport (setHttpTransport) and the
//     realtime socket factory are replaced by the handlers the app registers;
//   * window.__jtReady() for the screenshot script (fonts, map tiles, no
//     skeletons, camera at rest).

import { API_BASE, setHttpTransport } from '../services/http'
import { installTelegramMock } from '../telegram/mock'
import type { SocketLike } from '../services/realtime'
import type { LatLon } from '../utils/geo'
import osrm from './fixtures/embi-osrm.json'

// --- Fixture data shared by both apps -----------------------------------------

export interface DemoPlace extends LatLon {
  title: string
  subtitle: string | null
}

/** The four route stops of the presentation (A–D). */
export const STOPS: readonly [DemoPlace, DemoPlace, DemoPlace, DemoPlace] = [
  { lat: 48.82901, lon: 58.138517, title: 'Абай көшесі, 12', subtitle: 'Ембі' },
  { lat: 48.824852, lon: 58.143715, title: 'Ембі базары', subtitle: 'Ембі' },
  { lat: 48.820722, lon: 58.150964, title: '№2 мектеп', subtitle: 'Ембі' },
  { lat: 48.831881, lon: 58.158461, title: 'Теміржол вокзалы', subtitle: 'Ембі' },
]

/** Other named places (recent destinations, other drivers' orders). */
export const PLACES: readonly [DemoPlace, DemoPlace, DemoPlace] = [
  { lat: 48.8301, lon: 58.1418, title: 'Аудандық аурухана', subtitle: 'Тәуелсіздік көшесі, 5' },
  { lat: 48.8236, lon: 58.1352, title: 'Мұғалжар көшесі, 8', subtitle: 'Ембі' },
  { lat: 48.8318, lon: 58.1452, title: 'Абай көшесі, 30', subtitle: 'Ембі' },
]

/** Simulated device position (the passenger stands at stop A). */
export const DEVICE_HOME: LatLon = { lat: 48.829, lon: 58.1385 }

export interface DemoLine {
  type: 'LineString'
  coordinates: Array<[number, number]>
}

/** Road route A→B→C→D (OSRM). Distance 6,8 km; the API's own duration model says 14 min. */
export const ROUTE = {
  geometry: osrm.route.geometry as DemoLine,
  distance_m: osrm.route.distance_m,
  duration_s: 840,
  legs: osrm.route.legs.map((l) => ({
    distance_m: l.distance_m,
    duration_s: Math.round((840 * l.distance_m) / osrm.route.distance_m),
  })),
}

/** Driver's approach to A (OSRM). */
export const APPROACH = { geometry: osrm.approach.geometry as DemoLine, distance_m: osrm.approach.distance_m, duration_s: 180 }

export function bboxOf(coords: ReadonlyArray<[number, number]>): [number, number, number, number] {
  let w = Infinity
  let s = Infinity
  let e = -Infinity
  let n = -Infinity
  for (const [x, y] of coords) {
    w = Math.min(w, x)
    s = Math.min(s, y)
    e = Math.max(e, x)
    n = Math.max(n, y)
  }
  return [w, s, e, n]
}

/** Point `fraction` (0..1) of the way along a line, by vertex count. */
export function pointAlong(line: DemoLine, fraction: number): LatLon {
  const c = line.coordinates
  const i = Math.max(0, Math.min(c.length - 1, Math.round(fraction * (c.length - 1))))
  const [lon, lat] = c[i] ?? [0, 0]
  return { lon, lat }
}

function distanceM(a: LatLon, b: LatLon): number {
  const k = Math.PI / 180
  const x = (b.lon - a.lon) * k * Math.cos(((a.lat + b.lat) / 2) * k)
  const y = (b.lat - a.lat) * k
  return Math.sqrt(x * x + y * y) * 6_371_000
}

/** Reverse geocoding over the known places: the nearest one within 250 m. */
export function reverse(p: LatLon): { title: string; subtitle: string | null } {
  let best: DemoPlace | null = null
  let bestD = 250
  for (const place of [...STOPS, ...PLACES]) {
    const d = distanceM(p, place)
    if (d < bestD) {
      best = place
      bestD = d
    }
  }
  return best ? { title: best.title, subtitle: best.subtitle } : { title: 'Ембі', subtitle: `${p.lat.toFixed(5)}, ${p.lon.toFixed(5)}` }
}

/** Text search over the known places (case-insensitive substring). */
export function search(q: string): DemoPlace[] {
  const needle = q.trim().toLocaleLowerCase('kk')
  return [...STOPS, ...PLACES].filter((p) => `${p.title} ${p.subtitle ?? ''}`.toLocaleLowerCase('kk').includes(needle))
}

export function isoAgo(seconds: number): string {
  return new Date(Date.now() - seconds * 1000).toISOString()
}

export function isoIn(seconds: number): string {
  return new Date(Date.now() + seconds * 1000).toISOString()
}

/** The URL query the demo page was opened with (screen and state switches). */
export const params = new URLSearchParams(typeof location === 'undefined' ? '' : location.search)

// --- Simulated device ----------------------------------------------------------

let deviceHome: LatLon = DEVICE_HOME

/** Current simulated position: a slow walk of a few metres around the device's home point. */
export function devicePosition(now = Date.now()): LatLon & { accuracy_m: number } {
  const t = now / 1000
  return {
    lat: deviceHome.lat + 0.00004 * Math.sin(t / 6),
    lon: deviceHome.lon + 0.00006 * Math.cos(t / 6),
    accuracy_m: 12,
  }
}

function position(): GeolocationPosition {
  const p = devicePosition()
  const coords = {
    latitude: p.lat,
    longitude: p.lon,
    accuracy: p.accuracy_m,
    altitude: null,
    altitudeAccuracy: null,
    heading: null,
    speed: null,
  }
  return { coords: { ...coords, toJSON: () => coords }, timestamp: Date.now(), toJSON: () => ({ coords, timestamp: Date.now() }) } as GeolocationPosition
}

function installGeolocation(): void {
  const watchers = new Map<number, ReturnType<typeof setInterval>>()
  let nextId = 1
  const geo: Geolocation = {
    getCurrentPosition(ok) {
      setTimeout(() => ok(position()), 150)
    },
    watchPosition(ok) {
      const id = nextId++
      setTimeout(() => ok(position()), 50)
      watchers.set(
        id,
        setInterval(() => ok(position()), 1000),
      )
      return id
    },
    clearWatch(id) {
      clearInterval(watchers.get(id))
      watchers.delete(id)
    },
  }
  Object.defineProperty(navigator, 'geolocation', { value: geo, configurable: true })
}

// --- Telegram client -----------------------------------------------------------

export interface DemoUser {
  id: number
  first_name: string
  last_name: string
  username: string
  language_code: string
}

/** iPhone insets (Dynamic Island + home indicator) on phone-sized viewports, or with ?device=iphone. */
function isPhone(): boolean {
  const device = params.get('device')
  return device === 'iphone' || (device !== 'desktop' && window.matchMedia('(max-width: 500px)').matches)
}

// --- In-memory API ---------------------------------------------------------------

export interface DemoRequest {
  method: string
  path: string
  query: URLSearchParams
  body: unknown
  params: Record<string, string>
}

export class DemoResponse {
  constructor(
    readonly status: number,
    readonly body: unknown,
  ) {}
}

/** Error envelope as the API sends it (§10). */
export function apiError(status: number, code: string, message = code): DemoResponse {
  return new DemoResponse(status, { error: { code, message } })
}

export type DemoHandler = (req: DemoRequest) => unknown

export class DemoApi {
  private readonly routes: Array<{ method: string; re: RegExp; keys: string[]; handler: DemoHandler }> = []

  on(method: string, pattern: string, handler: DemoHandler): this {
    const keys: string[] = []
    const re = new RegExp(
      `^${pattern.replace(/:([a-z_]+)/g, (_m, k: string) => {
        keys.push(k)
        return '([^/]+)'
      })}$`,
    )
    this.routes.push({ method, re, keys, handler })
    return this
  }

  async handle(method: string, url: URL, body: unknown): Promise<Response> {
    const path = url.pathname.slice(url.pathname.indexOf(API_BASE) + API_BASE.length)
    for (const r of this.routes) {
      const m = r.method === method ? r.re.exec(path) : null
      if (!m) continue
      const params = Object.fromEntries(r.keys.map((k, i) => [k, decodeURIComponent(m[i + 1] ?? '')]))
      await new Promise((ok) => setTimeout(ok, 120))
      const out = await r.handler({ method, path, query: url.searchParams, body, params })
      const res = out instanceof DemoResponse ? out : new DemoResponse(out === undefined ? 204 : 200, out)
      return new Response(res.status === 204 ? null : JSON.stringify(res.body), {
        status: res.status,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    console.warn(`[demo] no fixture for ${method} ${path}`)
    return new Response(JSON.stringify({ error: { code: 'not_found', message: 'demo: no fixture' } }), { status: 404 })
  }
}

// --- Realtime ----------------------------------------------------------------------

const sockets = new Set<DemoSocket>()

class DemoSocket implements SocketLike {
  readyState = 0
  onopen: ((ev: Event) => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onclose: ((ev: CloseEvent) => void) | null = null
  onerror: ((ev: Event) => void) | null = null

  constructor() {
    sockets.add(this)
    setTimeout(() => {
      this.readyState = 1
      this.onopen?.(new Event('open'))
    }, 50)
  }

  push(frame: unknown): void {
    if (this.readyState === 1) this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(frame) }))
  }

  send(data: string): void {
    const f = JSON.parse(data) as { type?: string }
    if (f.type === 'auth') setTimeout(() => this.push({ type: 'ready', scopes: [] }), 30)
    else if (f.type === 'ping') setTimeout(() => this.push({ type: 'pong' }), 10)
  }

  close(code = 1000, reason = ''): void {
    sockets.delete(this)
    this.readyState = 3
    this.onclose?.(new CloseEvent('close', { code, reason }))
  }
}

let eventSeq = 0

/** Sends a realtime event (a hint; screens re-fetch REST state) to every open socket. */
export function broadcast(type: string, data: Record<string, unknown>): void {
  eventSeq += 1
  for (const s of sockets) s.push({ type, id: `demo-${eventSeq}`, ts: new Date().toISOString(), data })
}

// --- Readiness for screenshots -----------------------------------------------------

interface MapLike {
  loaded(): boolean
  areTilesLoaded(): boolean
  isMoving(): boolean
  on(type: string, cb: () => void): void
}

declare global {
  interface Window {
    /** Set by MapView in the presentation build. */
    __jtMap?: MapLike
    __jtReady?: () => boolean
  }
}

function installReadiness(): void {
  let fontsReady = false
  let lastMove = Date.now()
  let hooked: MapLike | null = null
  void document.fonts.ready.then(() => (fontsReady = true))
  window.__jtReady = () => {
    const m = window.__jtMap
    if (m && m !== hooked) {
      hooked = m
      m.on('move', () => (lastMove = Date.now()))
      lastMove = Date.now()
    }
    if (!fontsReady || !document.querySelector('#app > *')) return false
    if (document.querySelector('.jt-skeleton, [aria-busy="true"]')) return false
    if (m && document.querySelector('.maplibregl-canvas')) {
      if (!m.loaded() || !m.areTilesLoaded() || m.isMoving() || Date.now() - lastMove < 900) return false
    }
    return true
  }
}

// --- Entry -------------------------------------------------------------------------

export interface DemoSetup {
  user: DemoUser
  api: DemoApi
  /** Where the simulated device is (default: the passenger at stop A). */
  device?: LatLon
  startParam?: string | null
}

export interface DemoRuntime {
  createSocket: (url: string) => SocketLike
}

/** Installs the mocked Telegram client, device and API. Call before bootTelegram(). */
export function installDemoRuntime(setup: DemoSetup): DemoRuntime {
  if (setup.device) deviceHome = setup.device
  installTelegramMock({
    user: setup.user,
    startParam: setup.startParam ?? null,
    phone: isPhone(),
    location: () => devicePosition(),
  })
  installGeolocation()
  installReadiness()
  setHttpTransport(async (input, init) => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, location.href)
    if (!url.pathname.includes(API_BASE)) return fetch(input, init)
    let body: unknown = undefined
    if (typeof init?.body === 'string') body = JSON.parse(init.body)
    return setup.api.handle((init?.method ?? 'GET').toUpperCase(), url, body)
  })
  return { createSocket: () => new DemoSocket() }
}
