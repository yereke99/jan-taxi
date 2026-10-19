// The driver's own position: foreground reporting to the API while online or
// on a trip (LocationSender), plus the latest real fix for maps and 2GIS.
//
// One device location source for the whole app: an on-screen map that
// follows the car asks for the live stream (acquireLive); while it runs, the
// reporting reuses its fixes instead of reading the GPS a second time, and the
// upload keeps its own throttle. Everything pauses while the Mini App is
// hidden or deactivated.

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { reportLocation } from '../api/driver'
import { LocationSender, type Fix, type LocationIssue } from '../features/location/locationSender'
import { getLocation, LocationError, useTelegramFullscreen, watchLocation, type DeviceLocation, type LocationSource } from '../telegram'
import { roundCoord } from '../utils/geo'

/** A fix older than this is not used as "current position". */
const FRESH_MS = 60_000
/** The live stream publishes at most one fix per this interval (~1 Hz). */
export const LIVE_MIN_GAP_MS = 1_000
/** Reporting reuses a live fix up to this old instead of reading the GPS again. */
const LIVE_REUSE_MS = 4_000

function toInput(fix: Fix) {
  return {
    lat: roundCoord(fix.lat),
    lon: roundCoord(fix.lon),
    ...(fix.accuracy_m !== null ? { accuracy_m: Math.min(100_000, Math.round(fix.accuracy_m * 10) / 10) } : {}),
    ...(fix.heading !== null && fix.heading >= 0 && fix.heading <= 360 ? { heading: Math.round(fix.heading) } : {}),
    ...(fix.speed_mps !== null && fix.speed_mps >= 0 && fix.speed_mps <= 100 ? { speed_mps: Math.round(fix.speed_mps * 10) / 10 } : {}),
    recorded_at: new Date(Math.min(fix.timestamp, Date.now())).toISOString(),
  }
}

function toFix(loc: DeviceLocation): Fix {
  return { lat: loc.lat, lon: loc.lon, accuracy_m: loc.accuracy_m, heading: loc.heading, speed_mps: loc.speed_mps, timestamp: loc.timestamp }
}

export const useLocationStore = defineStore('location', () => {
  const lastFix = shallowRef<Fix | null>(null)
  const issue = ref<LocationIssue>(null)
  const reporting = ref(false)
  /** True while the live stream (about one fix per second) is running. */
  const live = ref(false)
  const now = ref(Date.now())
  let clock: ReturnType<typeof setInterval> | undefined

  /** Re-evaluates freshness while fixes are not arriving. */
  function touch(): void {
    now.value = Date.now()
    clock ??= setInterval(() => (now.value = Date.now()), 10_000)
  }

  /** Provider of the last successful one-shot read; the live stream prefers it. */
  let source: LocationSource | null = null
  let appActive = true
  // Live stream state (see acquireLive).
  let liveUsers = 0
  let liveDenied = false
  let stopLive: (() => void) | null = null
  let lastPublished = -Infinity
  let pending: Fix | null = null
  let publishTimer: ReturnType<typeof setTimeout> | undefined

  // --- Device reads ------------------------------------------------------------

  async function readFix(): Promise<Fix> {
    const loc = await getLocation({ timeoutMs: 8_000, maxAgeMs: 10_000 })
    source = loc.source
    // A working provider again (e.g. access granted in the settings).
    liveDenied = false
    syncLive()
    return toFix(loc)
  }

  /** A live fix recent enough to report instead of reading the GPS again. */
  function liveFix(): Fix | null {
    if (!stopLive) return null
    const f = lastFix.value
    return f && Date.now() - f.timestamp <= LIVE_REUSE_MS ? f : null
  }

  const sender = new LocationSender({
    read: () => {
      const f = liveFix()
      return f ? Promise.resolve(f) : readFix()
    },
    send: (fix) => reportLocation(toInput(fix)),
    onFix: (fix) => {
      lastFix.value = fix
      touch()
    },
    onIssue: (i) => (issue.value = i),
  })

  // --- Lifecycle -----------------------------------------------------------------

  let lifecycleAttached = false
  function attachLifecycle(): void {
    if (lifecycleAttached || typeof document === 'undefined') return
    lifecycleAttached = true
    const tg = useTelegramFullscreen()
    const sync = (): void => {
      appActive = document.visibilityState !== 'hidden' && tg.state.isActive
      sender.setActive(appActive)
      syncLive()
    }
    tg.onActiveChange(sync)
    document.addEventListener('visibilitychange', sync)
    sync()
  }

  // --- Live stream (on-screen maps) ---------------------------------------------

  function publish(fix: Fix): void {
    pending = null
    lastPublished = Date.now()
    lastFix.value = fix
    touch()
    // While reporting, the sender owns `issue`; otherwise fixes flowing mean location works.
    if (!reporting.value && (issue.value === 'denied' || issue.value === 'unavailable')) issue.value = null
  }

  /** At most one fix per LIVE_MIN_GAP_MS; a burst keeps only its newest fix. */
  function onLiveFix(loc: DeviceLocation): void {
    const fix = toFix(loc)
    const wait = lastPublished + LIVE_MIN_GAP_MS - Date.now()
    if (wait <= 0) {
      clearTimeout(publishTimer)
      publishTimer = undefined
      publish(fix)
      return
    }
    pending = fix
    publishTimer ??= setTimeout(() => {
      publishTimer = undefined
      if (pending) publish(pending)
    }, wait)
  }

  function onLiveError(err: LocationError): void {
    // A denial stops the stream (no repeated prompts); the next successful
    // one-shot read (locate button, reporting) starts it again.
    if (err.code !== 'denied') return
    liveDenied = true
    if (!reporting.value) issue.value = 'denied'
    syncLive()
  }

  /** Runs the stream while a map wants it, the app is in front and a provider has worked. */
  function syncLive(): void {
    const want = liveUsers > 0 && appActive && source !== null && !liveDenied
    if (want && !stopLive) {
      stopLive = watchLocation(onLiveFix, onLiveError, { prefer: source ?? undefined })
      live.value = true
    } else if (!want && stopLive) {
      stopLive()
      stopLive = null
      clearTimeout(publishTimer)
      publishTimer = undefined
      pending = null
      live.value = false
    }
  }

  /**
   * Asks for the live stream (about one fix per second) while a map follows
   * the car. Returns the release function; the stream stops when the last
   * user releases it.
   */
  function acquireLive(): () => void {
    attachLifecycle()
    liveUsers += 1
    syncLive()
    let released = false
    return () => {
      if (released) return
      released = true
      liveUsers -= 1
      syncLive()
    }
  }

  // --- Reporting and one-shot reads ---------------------------------------------

  /** Turns reporting on (online or on a trip) or off. */
  function setReporting(on: boolean): void {
    attachLifecycle()
    reporting.value = on
    if (on) sender.start()
    else sender.stop()
  }

  /** The latest fix when it is recent enough to act on, else null. */
  const current = computed<Fix | null>(() => {
    const f = lastFix.value
    return f && now.value - f.timestamp <= FRESH_MS ? f : null
  })

  /** One explicit fix (locate button, going online). Rejects with LocationError. */
  async function locateOnce(): Promise<Fix> {
    try {
      const fix = await readFix()
      lastFix.value = fix
      touch()
      if (issue.value === 'denied' || issue.value === 'unavailable') issue.value = null
      return fix
    } catch (err) {
      if (err instanceof LocationError) issue.value = err.code === 'denied' ? 'denied' : 'unavailable'
      throw err
    }
  }

  /** Sends one fix immediately (before going online). */
  async function sendNow(fix: Fix): Promise<void> {
    await reportLocation(toInput(fix))
  }

  return { lastFix, current, issue, reporting, live, setReporting, acquireLive, locateOnce, sendNow }
})
