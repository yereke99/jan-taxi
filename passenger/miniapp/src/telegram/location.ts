// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Device location: Telegram's LocationManager (Mini Apps 8.0+) first, the
// browser Geolocation API otherwise. There is deliberately no fallback
// coordinate: when the position is unknown the caller must say so and let the
// user pick a point on the map.
//
// watchLocation() streams fixes for the live "you are here" dot: the browser's
// watchPosition() where it works, polling the LocationManager about once a
// second otherwise (Telegram clients whose WebView has no geolocation).

import { locationManager } from '@tma.js/sdk'
import { isValidLatLon } from '../utils/geo'
import { safeBool, safeCall } from './safe'
import { useTelegramFullscreen } from './useTelegramFullscreen'

export type LocationSource = 'telegram' | 'browser'

export interface DeviceLocation {
  lat: number
  lon: number
  /** Horizontal accuracy radius in metres, when the device reports it. */
  accuracy_m: number | null
  /** Heading in degrees clockwise from north, when moving. */
  heading: number | null
  speed_mps: number | null
  /** Epoch milliseconds when the fix was taken. */
  timestamp: number
  source: LocationSource
}

export type LocationErrorCode = 'denied' | 'unavailable' | 'timeout' | 'invalid' | 'stale' | 'unsupported'

export class LocationError extends Error {
  override readonly name = 'LocationError'
  constructor(
    readonly code: LocationErrorCode,
    message: string = code,
  ) {
    super(message)
  }
}

export interface GetLocationOptions {
  /** Overall timeout per provider, ms (default 10 000). */
  timeoutMs?: number
  /** Oldest acceptable fix, ms (default 15 000). */
  maxAgeMs?: number
}

const DEFAULT_TIMEOUT_MS = 10_000
const DEFAULT_MAX_AGE_MS = 15_000
/** Tolerance for clock skew between the positioning service and Date.now(). */
const CLOCK_SKEW_MS = 2_000

function finiteOrNull(v: number | null | undefined, min = -Infinity): number | null {
  return typeof v === 'number' && Number.isFinite(v) && v >= min ? v : null
}

/** Rejects impossible or outdated fixes. Exported for tests. */
export function validateLocation(loc: DeviceLocation, now: number, maxAgeMs: number): DeviceLocation {
  if (!isValidLatLon(loc)) throw new LocationError('invalid')
  if (!Number.isFinite(loc.timestamp) || now - loc.timestamp > maxAgeMs + CLOCK_SKEW_MS) {
    throw new LocationError('stale')
  }
  return loc
}

async function fromTelegram(timeoutMs: number): Promise<DeviceLocation | null> {
  if (!safeBool(() => locationManager.mount.isAvailable())) return null
  try {
    if (!locationManager.isMounted()) await locationManager.mount({ timeout: timeoutMs })
  } catch {
    return null
  }
  // isAvailable is a state value here: whether the device can provide location at all.
  if (!safeBool(() => locationManager.isAvailable())) return null
  let data
  try {
    data = await locationManager.requestLocation({ timeout: timeoutMs })
  } catch (err) {
    const name = err && typeof err === 'object' && 'name' in err ? String(err.name) : ''
    if (name === 'TimeoutError') throw new LocationError('timeout')
    return null
  }
  if (data === null) throw new LocationError('denied')
  return {
    lat: data.latitude,
    lon: data.longitude,
    accuracy_m: finiteOrNull(data.horizontal_accuracy, 0),
    heading: finiteOrNull(data.course, 0),
    speed_mps: finiteOrNull(data.speed, 0),
    timestamp: Date.now(),
    source: 'telegram',
  }
}

function fromPosition(pos: GeolocationPosition): DeviceLocation {
  return {
    lat: pos.coords.latitude,
    lon: pos.coords.longitude,
    accuracy_m: finiteOrNull(pos.coords.accuracy, 0),
    heading: finiteOrNull(pos.coords.heading, 0),
    speed_mps: finiteOrNull(pos.coords.speed, 0),
    timestamp: pos.timestamp,
    source: 'browser',
  }
}

function positionErrorCode(err: GeolocationPositionError): LocationErrorCode {
  return err.code === err.PERMISSION_DENIED ? 'denied' : err.code === err.TIMEOUT ? 'timeout' : 'unavailable'
}

function fromBrowser(timeoutMs: number, maxAgeMs: number): Promise<DeviceLocation> {
  return new Promise((resolve, reject) => {
    if (typeof navigator === 'undefined' || !navigator.geolocation) {
      reject(new LocationError('unsupported'))
      return
    }
    navigator.geolocation.getCurrentPosition(
      (pos) => resolve(fromPosition(pos)),
      (err) => reject(new LocationError(positionErrorCode(err), err.message)),
      { enableHighAccuracy: true, timeout: timeoutMs, maximumAge: maxAgeMs },
    )
  })
}

/**
 * Returns the current device position or rejects with a LocationError
 * ('denied' | 'unavailable' | 'timeout' | 'invalid' | 'stale' | 'unsupported').
 */
export async function getLocation(options: GetLocationOptions = {}): Promise<DeviceLocation> {
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS
  const maxAgeMs = options.maxAgeMs ?? DEFAULT_MAX_AGE_MS
  if (useTelegramFullscreen().state.isTelegram) {
    const tg = await fromTelegram(timeoutMs)
    if (tg) return validateLocation(tg, Date.now(), maxAgeMs)
  }
  const loc = await fromBrowser(timeoutMs, maxAgeMs)
  return validateLocation(loc, Date.now(), maxAgeMs)
}

export interface WatchLocationOptions {
  /** Provider that produced the first fix; 'telegram' polls the LocationManager. */
  prefer?: LocationSource
  /** Polling interval for the LocationManager, ms (default 1 000). */
  intervalMs?: number
}

const WATCH_INTERVAL_MS = 1_000

/**
 * Streams device positions (about one per second) until the returned function
 * is called. Invalid or outdated fixes are skipped; provider failures go to
 * onError, and a failing watchPosition() falls back to LocationManager polling
 * inside Telegram. Never throws.
 */
export function watchLocation(
  onFix: (loc: DeviceLocation) => void,
  onError: (err: LocationError) => void = () => undefined,
  options: WatchLocationOptions = {},
): () => void {
  const intervalMs = options.intervalMs ?? WATCH_INTERVAL_MS
  const inTelegram = useTelegramFullscreen().state.isTelegram
  const hasBrowser = typeof navigator !== 'undefined' && !!navigator.geolocation
  let stopped = false
  let watchId: number | null = null
  let timer: ReturnType<typeof setTimeout> | undefined

  function emit(loc: DeviceLocation): void {
    if (stopped) return
    try {
      onFix(validateLocation(loc, Date.now(), DEFAULT_MAX_AGE_MS))
    } catch {
      // An impossible or stale sample: wait for the next one.
    }
  }

  async function poll(): Promise<void> {
    if (stopped) return
    try {
      const loc = await fromTelegram(DEFAULT_TIMEOUT_MS)
      if (loc) emit(loc)
      else onError(new LocationError('unavailable'))
    } catch (err) {
      if (!stopped) onError(err instanceof LocationError ? err : new LocationError('unavailable'))
    }
    if (!stopped) timer = setTimeout(() => void poll(), intervalMs)
  }

  function startBrowser(): void {
    watchId = navigator.geolocation.watchPosition(
      (pos) => emit(fromPosition(pos)),
      (err) => {
        if (stopped) return
        const code = positionErrorCode(err)
        if (code !== 'timeout' && inTelegram && watchId !== null) {
          navigator.geolocation.clearWatch(watchId)
          watchId = null
          void poll()
          return
        }
        onError(new LocationError(code, err.message))
      },
      { enableHighAccuracy: true, timeout: DEFAULT_TIMEOUT_MS, maximumAge: intervalMs },
    )
  }

  if (hasBrowser && !(inTelegram && options.prefer === 'telegram')) startBrowser()
  else if (inTelegram) void poll()
  else onError(new LocationError('unsupported'))

  return () => {
    stopped = true
    clearTimeout(timer)
    if (watchId !== null && hasBrowser) navigator.geolocation.clearWatch(watchId)
    watchId = null
  }
}

/** True when openLocationSettings() can work (Telegram 8.0+ after access was requested). */
export function canOpenLocationSettings(): boolean {
  return safeBool(() => locationManager.openSettings.isAvailable())
}

/**
 * Opens Telegram's location settings for this bot so the user can grant
 * access after a denial. Must run inside a user gesture. Returns false when
 * unavailable (browser or old client).
 */
export function openLocationSettings(): boolean {
  if (!safeBool(() => locationManager.openSettings.isAvailable())) return false
  return (
    safeCall(() => {
      locationManager.openSettings()
      return true
    }) ?? false
  )
}
