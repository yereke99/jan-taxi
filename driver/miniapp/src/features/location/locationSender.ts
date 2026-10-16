// Foreground location reporting while the driver is online or on a trip:
// read a real device fix every few seconds and POST it to /driver/location
// (also the online heartbeat). Never invents coordinates: without a fix
// nothing is sent. Paused while the Mini App is hidden or deactivated and
// resumed (with an immediate fix) when it comes back.

export interface Fix {
  lat: number
  lon: number
  accuracy_m: number | null
  heading: number | null
  speed_mps: number | null
  /** Epoch ms when the device took the fix. */
  timestamp: number
}

/** Why fixes are not flowing (shown to the driver). */
export type LocationIssue = 'denied' | 'unavailable' | 'outside_service_area' | null

export type SenderStatus = 'stopped' | 'running' | 'paused'

export interface LocationSenderOptions {
  read: () => Promise<Fix>
  send: (fix: Fix) => Promise<void>
  /** Delay between reads while things work (default 6 s). */
  intervalMs?: number
  /** Minimum gap between two POSTs (default 5 s; the API allows one per 2 s). */
  minSendGapMs?: number
  /** Delay before trying again after a failed read (default 15 s). */
  errorBackoffMs?: number
  now?: () => number
  onFix?: (fix: Fix) => void
  onIssue?: (issue: LocationIssue) => void
}

function errorCode(err: unknown): string {
  return err && typeof err === 'object' && 'code' in err && typeof (err as { code: unknown }).code === 'string'
    ? (err as { code: string }).code
    : ''
}

export class LocationSender {
  private readonly o: Required<Omit<LocationSenderOptions, 'onFix' | 'onIssue'>> & Pick<LocationSenderOptions, 'onFix' | 'onIssue'>
  private wanted = false
  private active = true
  private busy = false
  private timer: ReturnType<typeof setTimeout> | undefined
  private lastSentAt = -Infinity
  private issue: LocationIssue = null

  constructor(opts: LocationSenderOptions) {
    this.o = {
      intervalMs: 6_000,
      minSendGapMs: 5_000,
      errorBackoffMs: 15_000,
      now: () => Date.now(),
      ...opts,
    }
  }

  get status(): SenderStatus {
    if (!this.wanted) return 'stopped'
    return this.active ? 'running' : 'paused'
  }

  /** Starts reporting (idempotent). */
  start(): void {
    if (this.wanted) return
    this.wanted = true
    this.kick()
  }

  /** Stops reporting (idempotent). */
  stop(): void {
    this.wanted = false
    this.clear()
    this.setIssue(null)
  }

  /** The Mini App became visible/active (true) or hidden/deactivated (false). */
  setActive(active: boolean): void {
    if (active === this.active) return
    this.active = active
    if (active) this.kick()
    else this.clear()
  }

  private get shouldRun(): boolean {
    return this.wanted && this.active
  }

  private clear(): void {
    clearTimeout(this.timer)
    this.timer = undefined
  }

  private kick(): void {
    if (!this.shouldRun || this.busy) return
    this.clear()
    void this.tick()
  }

  private schedule(delay: number): void {
    this.clear()
    if (this.shouldRun) this.timer = setTimeout(() => void this.tick(), delay)
  }

  private setIssue(issue: LocationIssue): void {
    if (issue === this.issue) return
    this.issue = issue
    this.o.onIssue?.(issue)
  }

  private async tick(): Promise<void> {
    this.timer = undefined
    if (!this.shouldRun || this.busy) return
    this.busy = true
    let delay = this.o.intervalMs
    try {
      const fix = await this.o.read()
      if (!this.shouldRun) return
      this.o.onFix?.(fix)
      if (this.issue !== 'outside_service_area') this.setIssue(null)
      if (this.o.now() - this.lastSentAt >= this.o.minSendGapMs) {
        this.lastSentAt = this.o.now()
        try {
          await this.o.send(fix)
          this.setIssue(null)
        } catch (err) {
          // 429 and network failures: the next tick tries again.
          if (errorCode(err) === 'outside_service_area') this.setIssue('outside_service_area')
        }
      }
    } catch (err) {
      this.setIssue(errorCode(err) === 'denied' ? 'denied' : 'unavailable')
      delay = this.o.errorBackoffMs
    } finally {
      this.busy = false
      this.schedule(delay)
    }
  }
}
