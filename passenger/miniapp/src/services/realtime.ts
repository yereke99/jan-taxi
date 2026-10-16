// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// WebSocket client for GET /api/v1/ws (ARCHITECTURE §9).
//
// Frame protocol (admin/backend/internal/realtime/ws.go):
//   client → {"type":"auth","token":"<app token>"}   first frame, within 5 s of open
//   server → {"type":"ready","scopes":[...]}         auth accepted; events follow
//   client → {"type":"ping"}  server → {"type":"pong"}   application heartbeat
//   server → {"type":"resync"}                       events may have been lost: re-fetch
//   server → {"type":"order.accepted","id":..,"ts":..,"data":{..}}  events
// The server also sends protocol-level pings (answered by the browser).
// Close codes: 1008 with reason auth_timeout | unauthorized | token_expired |
// user_blocked | slow_consumer | rate_limited; 1001 on server shutdown;
// 1011 internal. unauthorized / token_expired → sign in again with initData,
// then reconnect; user_blocked stops the client; everything else reconnects
// with backoff.
//
// Events are hints: subscribers re-fetch authoritative REST state. Whenever
// events may have been missed (reconnect, server resync, app returning to the
// foreground) the onResync callbacks fire so screens can refresh.

import { readonly, ref, type Ref } from 'vue'
import { API_BASE } from './http'

export interface RealtimeEvent<T = Record<string, unknown>> {
  type: string
  id?: string
  ts?: string
  scopes?: string[]
  data: T
}

export type RealtimeStatus = 'idle' | 'connecting' | 'authenticating' | 'open' | 'reconnecting' | 'stopped'

export type EventHandler = (event: RealtimeEvent) => void

/** Minimal WebSocket surface used here (lets tests inject a fake). */
export interface SocketLike {
  readonly readyState: number
  onopen: ((ev: Event) => void) | null
  onmessage: ((ev: MessageEvent) => void) | null
  onclose: ((ev: CloseEvent) => void) | null
  onerror: ((ev: Event) => void) | null
  send(data: string): void
  close(code?: number, reason?: string): void
}

export interface RealtimeOptions {
  url?: string | (() => string)
  getToken: () => string | null | Promise<string | null>
  /** Obtains a new token after the server rejected the current one. */
  refreshToken?: () => Promise<boolean>
  createSocket?: (url: string) => SocketLike
  random?: () => number
  backoffInitialMs?: number
  backoffMaxMs?: number
  /** Interval between client pings. */
  heartbeatMs?: number
  /** Reconnect when nothing (not even a pong) arrived for this long. */
  idleTimeoutMs?: number
  /** Give up on a connection whose auth was not acknowledged in time. */
  readyTimeoutMs?: number
  /** Listen to document visibility and online events (default true). */
  watchLifecycle?: boolean
}

const OPEN = 1
/** Close code (policy violation) the server uses for auth and abuse problems. */
export const CLOSE_POLICY_VIOLATION = 1008
/** 1008 reasons after which the token must be renewed before reconnecting. */
export const TOKEN_REFRESH_REASONS: readonly string[] = ['unauthorized', 'token_expired']
/** 1008 reason after which the client stops: the account is blocked. */
export const CLOSE_REASON_BLOCKED = 'user_blocked'
const CLOSE_CLIENT_TIMEOUT = 4000

/**
 * Reconnect delay for the n-th consecutive failure (0-based): exponential
 * from initialMs to maxMs with "equal jitter" — half fixed, half random — so
 * many clients spread out without any of them retrying instantly.
 */
export function backoffDelay(attempt: number, random: () => number = Math.random, initialMs = 1000, maxMs = 30_000): number {
  const base = Math.min(maxMs, initialMs * 2 ** Math.max(0, attempt))
  return Math.round(base / 2 + random() * (base / 2))
}

export function defaultSocketUrl(): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}${API_BASE}/ws`
}

export class RealtimeClient {
  private readonly opts: RealtimeOptions
  private socket: SocketLike | null = null
  private statusValue: RealtimeStatus = 'idle'
  private attempt = 0
  private connectedBefore = false
  private stopped = true
  private needsTokenRefresh = false
  private scopesValue: string[] = []
  private lastInboundAt = 0
  private reconnectTimer: ReturnType<typeof setTimeout> | undefined
  private readyTimer: ReturnType<typeof setTimeout> | undefined
  private heartbeatTimer: ReturnType<typeof setInterval> | undefined
  private readonly handlers = new Map<string, Set<EventHandler>>()
  private readonly resyncCallbacks = new Set<() => void>()
  private readonly statusCallbacks = new Set<(s: RealtimeStatus) => void>()
  private readonly detachLifecycle: Array<() => void> = []

  constructor(opts: RealtimeOptions) {
    this.opts = opts
  }

  get status(): RealtimeStatus {
    return this.statusValue
  }

  /** Delivery scopes the server granted in its last "ready" frame. */
  get scopes(): readonly string[] {
    return this.scopesValue
  }

  /** Connects and keeps the connection alive until stop(). */
  start(): void {
    if (!this.stopped) return
    this.stopped = false
    if (this.opts.watchLifecycle !== false) this.attachLifecycle()
    this.connect()
  }

  stop(): void {
    this.stopped = true
    this.clearTimers()
    for (const off of this.detachLifecycle.splice(0)) off()
    const s = this.socket
    this.socket = null
    if (s) {
      s.onopen = s.onmessage = s.onerror = s.onclose = null
      s.close(1000, 'client stop')
    }
    this.setStatus('stopped')
  }

  /** Subscribes to one event type, or '*' for all events. Returns an unsubscribe. */
  on<T = Record<string, unknown>>(type: string, handler: (event: RealtimeEvent<T>) => void): () => void {
    const set = this.handlers.get(type) ?? new Set<EventHandler>()
    set.add(handler as EventHandler)
    this.handlers.set(type, set)
    return () => set.delete(handler as EventHandler)
  }

  /** Called whenever events may have been missed; re-fetch REST state there. */
  onResync(cb: () => void): () => void {
    this.resyncCallbacks.add(cb)
    return () => this.resyncCallbacks.delete(cb)
  }

  onStatus(cb: (status: RealtimeStatus) => void): () => void {
    this.statusCallbacks.add(cb)
    return () => this.statusCallbacks.delete(cb)
  }

  /**
   * The app returned to the foreground (Telegram activation, visibility).
   * Re-syncs when connected, otherwise reconnects immediately (and re-syncs
   * once ready).
   */
  notifyForeground(): void {
    if (this.stopped) return
    if (this.statusValue === 'open') this.fireResync()
    else this.reconnectNow()
  }

  /** Skips the pending backoff and connects now. */
  reconnectNow(): void {
    if (this.stopped) return
    if (this.socket && (this.statusValue === 'connecting' || this.statusValue === 'authenticating' || this.statusValue === 'open')) {
      return
    }
    clearTimeout(this.reconnectTimer)
    this.reconnectTimer = undefined
    this.attempt = 0
    this.connect()
  }

  // --- internals -----------------------------------------------------------

  private setStatus(s: RealtimeStatus): void {
    if (s === this.statusValue) return
    this.statusValue = s
    for (const cb of this.statusCallbacks) cb(s)
  }

  private url(): string {
    const u = this.opts.url
    return typeof u === 'function' ? u() : (u ?? defaultSocketUrl())
  }

  private createSocket(url: string): SocketLike {
    return this.opts.createSocket ? this.opts.createSocket(url) : (new WebSocket(url) as unknown as SocketLike)
  }

  private connect(): void {
    if (this.stopped) return
    this.setStatus('connecting')
    let socket: SocketLike
    try {
      socket = this.createSocket(this.url())
    } catch {
      this.scheduleReconnect()
      return
    }
    this.socket = socket
    socket.onopen = () => void this.handleOpen(socket)
    socket.onmessage = (ev) => this.handleMessage(socket, ev)
    socket.onerror = () => {
      // A close event always follows; reconnect logic lives there.
    }
    socket.onclose = (ev) => void this.handleClose(socket, ev)
  }

  private async handleOpen(socket: SocketLike): Promise<void> {
    if (socket !== this.socket) return
    this.setStatus('authenticating')
    this.lastInboundAt = Date.now()
    let token = await this.opts.getToken()
    if (!token && this.opts.refreshToken && (await this.opts.refreshToken())) token = await this.opts.getToken()
    if (socket !== this.socket) return
    if (!token) {
      this.needsTokenRefresh = true
      socket.close(CLOSE_CLIENT_TIMEOUT, 'no token')
      return
    }
    this.sendFrame(socket, { type: 'auth', token })
    this.readyTimer = setTimeout(() => {
      if (socket === this.socket && this.statusValue !== 'open') socket.close(CLOSE_CLIENT_TIMEOUT, 'auth timeout')
    }, this.opts.readyTimeoutMs ?? 10_000)
  }

  private handleMessage(socket: SocketLike, ev: MessageEvent): void {
    if (socket !== this.socket) return
    this.lastInboundAt = Date.now()
    let frame: unknown
    try {
      frame = JSON.parse(String(ev.data))
    } catch {
      return
    }
    if (!frame || typeof frame !== 'object') return
    const f = frame as Record<string, unknown>
    const type = typeof f.type === 'string' ? f.type : ''
    switch (type) {
      case 'ready':
        this.scopesValue = strArray(f.scopes) ?? []
        this.handleReady(socket)
        return
      case 'ping':
        this.sendFrame(socket, { type: 'pong' })
        return
      case 'pong':
        return
      case 'resync':
        this.fireResync()
        return
      default:
        if (type) this.dispatch({ type, id: str(f.id), ts: str(f.ts), scopes: strArray(f.scopes), data: record(f.data) })
    }
  }

  private handleReady(socket: SocketLike): void {
    clearTimeout(this.readyTimer)
    this.readyTimer = undefined
    this.attempt = 0
    this.setStatus('open')
    this.startHeartbeat(socket)
    if (this.connectedBefore) this.fireResync()
    this.connectedBefore = true
  }

  private async handleClose(socket: SocketLike, ev: CloseEvent): Promise<void> {
    if (socket !== this.socket) return
    this.socket = null
    this.clearTimers()
    if (this.stopped) return
    if (ev.code === CLOSE_POLICY_VIOLATION && ev.reason === CLOSE_REASON_BLOCKED) {
      this.stop()
      return
    }
    this.setStatus('reconnecting')
    if ((ev.code === CLOSE_POLICY_VIOLATION && TOKEN_REFRESH_REASONS.includes(ev.reason)) || this.needsTokenRefresh) {
      this.needsTokenRefresh = false
      if (this.opts.refreshToken) {
        try {
          await this.opts.refreshToken()
        } catch {
          // Next attempt will try again after the backoff.
        }
      }
    }
    this.scheduleReconnect()
  }

  private scheduleReconnect(): void {
    if (this.stopped) return
    this.setStatus('reconnecting')
    clearTimeout(this.reconnectTimer)
    const delay = backoffDelay(this.attempt, this.opts.random, this.opts.backoffInitialMs, this.opts.backoffMaxMs)
    this.attempt++
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = undefined
      this.connect()
    }, delay)
  }

  private startHeartbeat(socket: SocketLike): void {
    clearInterval(this.heartbeatTimer)
    const every = this.opts.heartbeatMs ?? 25_000
    const idle = this.opts.idleTimeoutMs ?? 70_000
    this.heartbeatTimer = setInterval(() => {
      if (socket !== this.socket) return
      if (Date.now() - this.lastInboundAt > idle) {
        socket.close(CLOSE_CLIENT_TIMEOUT, 'idle timeout')
        return
      }
      this.sendFrame(socket, { type: 'ping' })
    }, every)
  }

  private sendFrame(socket: SocketLike, frame: Record<string, unknown>): void {
    if (socket.readyState !== OPEN) return
    try {
      socket.send(JSON.stringify(frame))
    } catch {
      // The close handler takes over.
    }
  }

  private dispatch(event: RealtimeEvent): void {
    for (const key of [event.type, '*']) {
      const set = this.handlers.get(key)
      if (!set) continue
      for (const h of [...set]) {
        try {
          h(event)
        } catch (err) {
          console.error('[realtime] handler failed for', event.type, err)
        }
      }
    }
  }

  private fireResync(): void {
    for (const cb of [...this.resyncCallbacks]) {
      try {
        cb()
      } catch (err) {
        console.error('[realtime] resync callback failed', err)
      }
    }
  }

  private clearTimers(): void {
    clearTimeout(this.readyTimer)
    clearInterval(this.heartbeatTimer)
    clearTimeout(this.reconnectTimer)
    this.readyTimer = this.heartbeatTimer = this.reconnectTimer = undefined
  }

  private attachLifecycle(): void {
    if (typeof document === 'undefined') return
    const onVisibility = (): void => {
      if (document.visibilityState === 'visible') this.notifyForeground()
    }
    const onOnline = (): void => this.reconnectNow()
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('online', onOnline)
    this.detachLifecycle.push(
      () => document.removeEventListener('visibilitychange', onVisibility),
      () => window.removeEventListener('online', onOnline),
    )
  }
}

function str(v: unknown): string | undefined {
  return typeof v === 'string' ? v : undefined
}

function strArray(v: unknown): string[] | undefined {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : undefined
}

function record(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {}
}

export function createRealtimeClient(opts: RealtimeOptions): RealtimeClient {
  return new RealtimeClient(opts)
}

let appClient: RealtimeClient | null = null
const appStatus = ref<RealtimeStatus>('idle')

/**
 * Configures the app-wide client once (main.ts) with the auth service's token
 * functions. Later calls return the same client.
 */
export function initRealtime(opts: RealtimeOptions): RealtimeClient {
  if (!appClient) {
    appClient = createRealtimeClient(opts)
    appClient.onStatus((s) => (appStatus.value = s))
  }
  return appClient
}

/** The app-wide client; initRealtime() must have run. */
export function useRealtime(): RealtimeClient {
  if (!appClient) throw new Error('realtime client not initialised; call initRealtime() in main.ts')
  return appClient
}

/** Reactive connection status for banners. */
export function useRealtimeStatus(): Readonly<Ref<RealtimeStatus>> {
  return readonly(appStatus)
}
