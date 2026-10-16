// WebSocket client for GET /api/v1/ws as used by the admin panel
// (admin/backend/internal/realtime/ws.go). Admins are authenticated by the
// session cookie at upgrade time, so no auth frame is sent.
//
//   server → {"type":"ready","scopes":[...]}         connection accepted
//   server → {"type":"order.accepted","id","ts","data"}  events (hints only)
//   server → {"type":"resync"}                        events may have been lost
//   client → {"type":"ping"}  server → {"type":"pong"}  application heartbeat
//   close 1008 unauthorized | token_expired | slow_consumer | rate_limited | auth_timeout
//   close 1001 on server shutdown
//
// Screens re-fetch REST state on events and on every resync (reconnect,
// tab returning to the foreground).

import { API_BASE } from './http'

export interface RealtimeEvent {
  type: string
  id?: string
  ts?: string
  data: Record<string, unknown>
}

export type RealtimeStatus = 'idle' | 'connecting' | 'open' | 'reconnecting' | 'stopped'

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
  url?: string
  createSocket?: (url: string) => SocketLike
  /** Called after a policy close that may mean the session ended; resolve false to stop reconnecting. */
  checkSession?: () => Promise<boolean>
  random?: () => number
  heartbeatMs?: number
  idleTimeoutMs?: number
  watchLifecycle?: boolean
}

const OPEN = 1
const CLOSE_POLICY = 1008
const SESSION_REASONS = new Set(['unauthorized', 'token_expired', 'user_blocked'])

/** Reconnect delay for the n-th consecutive failure: 1s → 30s, half fixed and half random. */
export function backoffDelay(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(30_000, 1000 * 2 ** Math.max(0, attempt))
  return Math.round(base / 2 + random() * (base / 2))
}

export function defaultSocketUrl(): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}${API_BASE}/ws`
}

type Handler = (ev: RealtimeEvent) => void

export class AdminRealtime {
  private readonly opts: RealtimeOptions
  private socket: SocketLike | null = null
  private stopped = true
  private attempt = 0
  private connectedBefore = false
  private lastInbound = 0
  private reconnectTimer: ReturnType<typeof setTimeout> | undefined
  private heartbeatTimer: ReturnType<typeof setInterval> | undefined
  private readonly handlers = new Map<string, Set<Handler>>()
  private readonly resyncs = new Set<() => void>()
  private readonly statusListeners = new Set<(s: RealtimeStatus) => void>()
  private statusValue: RealtimeStatus = 'idle'
  private detach: Array<() => void> = []

  constructor(opts: RealtimeOptions = {}) {
    this.opts = opts
  }

  get status(): RealtimeStatus {
    return this.statusValue
  }

  start(): void {
    if (!this.stopped) return
    this.stopped = false
    if (this.opts.watchLifecycle !== false && typeof document !== 'undefined') this.attachLifecycle()
    this.connect()
  }

  stop(): void {
    this.stopped = true
    clearTimeout(this.reconnectTimer)
    clearInterval(this.heartbeatTimer)
    for (const off of this.detach.splice(0)) off()
    const s = this.socket
    this.socket = null
    if (s) {
      s.onopen = s.onmessage = s.onclose = s.onerror = null
      try {
        s.close(1000, 'client_stop')
      } catch {
        // already closed
      }
    }
    this.connectedBefore = false
    this.setStatus('stopped')
  }

  /** Subscribes to an event type ('*' for all); returns the unsubscribe function. */
  on(type: string, handler: Handler): () => void {
    let set = this.handlers.get(type)
    if (!set) {
      set = new Set()
      this.handlers.set(type, set)
    }
    set.add(handler)
    return () => set.delete(handler)
  }

  /** Called whenever events may have been missed; screens re-fetch their data. */
  onResync(cb: () => void): () => void {
    this.resyncs.add(cb)
    return () => this.resyncs.delete(cb)
  }

  onStatus(cb: (s: RealtimeStatus) => void): () => void {
    this.statusListeners.add(cb)
    return () => this.statusListeners.delete(cb)
  }

  private setStatus(s: RealtimeStatus): void {
    if (this.statusValue === s) return
    this.statusValue = s
    for (const cb of this.statusListeners) cb(s)
  }

  private connect(): void {
    if (this.stopped) return
    this.setStatus(this.connectedBefore ? 'reconnecting' : 'connecting')
    let socket: SocketLike
    try {
      const url = this.opts.url ?? defaultSocketUrl()
      socket = this.opts.createSocket ? this.opts.createSocket(url) : (new WebSocket(url) as unknown as SocketLike)
    } catch {
      this.scheduleReconnect()
      return
    }
    this.socket = socket
    socket.onmessage = (ev) => this.handleMessage(ev)
    socket.onclose = (ev) => this.handleClose(socket, ev)
    socket.onerror = () => {
      // onclose follows and schedules the reconnect.
    }
  }

  private handleMessage(ev: MessageEvent): void {
    this.lastInbound = Date.now()
    let frame: Record<string, unknown>
    try {
      frame = JSON.parse(String(ev.data)) as Record<string, unknown>
    } catch {
      return
    }
    const type = typeof frame.type === 'string' ? frame.type : ''
    switch (type) {
      case 'ready':
        this.attempt = 0
        this.setStatus('open')
        this.startHeartbeat()
        if (this.connectedBefore) this.fireResync()
        this.connectedBefore = true
        return
      case 'ping':
        this.send({ type: 'pong' })
        return
      case 'pong':
        return
      case 'resync':
        this.fireResync()
        return
      case '':
        return
    }
    const data = frame.data && typeof frame.data === 'object' ? (frame.data as Record<string, unknown>) : {}
    const event: RealtimeEvent = {
      type,
      id: typeof frame.id === 'string' ? frame.id : undefined,
      ts: typeof frame.ts === 'string' ? frame.ts : undefined,
      data,
    }
    for (const key of [type, '*']) {
      for (const h of this.handlers.get(key) ?? []) {
        try {
          h(event)
        } catch (err) {
          console.error('[realtime] handler failed', err)
        }
      }
    }
  }

  private handleClose(socket: SocketLike, ev: CloseEvent): void {
    if (socket !== this.socket) return
    this.socket = null
    clearInterval(this.heartbeatTimer)
    if (this.stopped) return
    if (ev.code === CLOSE_POLICY && SESSION_REASONS.has(ev.reason) && this.opts.checkSession) {
      this.setStatus('reconnecting')
      void this.opts
        .checkSession()
        .catch(() => true)
        .then((alive) => {
          if (alive) this.scheduleReconnect()
          else this.stop()
        })
      return
    }
    this.scheduleReconnect()
  }

  private scheduleReconnect(): void {
    if (this.stopped) return
    this.setStatus('reconnecting')
    clearTimeout(this.reconnectTimer)
    const delay = backoffDelay(this.attempt++, this.opts.random)
    this.reconnectTimer = setTimeout(() => this.connect(), delay)
  }

  private startHeartbeat(): void {
    clearInterval(this.heartbeatTimer)
    const every = this.opts.heartbeatMs ?? 30_000
    const idle = this.opts.idleTimeoutMs ?? 80_000
    this.heartbeatTimer = setInterval(() => {
      if (Date.now() - this.lastInbound > idle) {
        // No traffic (not even a pong): the connection is dead without a close frame.
        const s = this.socket
        this.socket = null
        clearInterval(this.heartbeatTimer)
        try {
          s?.close(4000, 'idle')
        } catch {
          // ignore
        }
        this.scheduleReconnect()
        return
      }
      this.send({ type: 'ping' })
    }, every)
  }

  private send(frame: Record<string, unknown>): void {
    if (this.socket?.readyState === OPEN) {
      try {
        this.socket.send(JSON.stringify(frame))
      } catch {
        // the close handler reconnects
      }
    }
  }

  private fireResync(): void {
    for (const cb of this.resyncs) {
      try {
        cb()
      } catch (err) {
        console.error('[realtime] resync callback failed', err)
      }
    }
  }

  private attachLifecycle(): void {
    let hiddenAt = 0
    const onVisibility = (): void => {
      if (document.visibilityState === 'hidden') {
        hiddenAt = Date.now()
        return
      }
      if (this.stopped) return
      if (!this.socket || this.socket.readyState !== OPEN) {
        clearTimeout(this.reconnectTimer)
        this.attempt = 0
        this.connect()
      } else if (hiddenAt && Date.now() - hiddenAt > 30_000) {
        this.fireResync()
      }
    }
    const onOnline = (): void => {
      if (!this.stopped && !this.socket) {
        clearTimeout(this.reconnectTimer)
        this.attempt = 0
        this.connect()
      }
    }
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('online', onOnline)
    this.detach.push(
      () => document.removeEventListener('visibilitychange', onVisibility),
      () => window.removeEventListener('online', onOnline),
    )
  }
}
