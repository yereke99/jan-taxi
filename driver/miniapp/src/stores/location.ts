// The driver's own position: foreground reporting to the API while online or
// on a trip (LocationSender), plus the latest real fix for maps and 2GIS.

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { reportLocation } from '../api/driver'
import { LocationSender, type Fix, type LocationIssue } from '../features/location/locationSender'
import { getLocation, LocationError, useTelegramFullscreen } from '../telegram'
import { roundCoord } from '../utils/geo'

/** A fix older than this is not used as "current position". */
const FRESH_MS = 60_000

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

async function readFix(): Promise<Fix> {
  const loc = await getLocation({ timeoutMs: 8_000, maxAgeMs: 10_000 })
  return { lat: loc.lat, lon: loc.lon, accuracy_m: loc.accuracy_m, heading: loc.heading, speed_mps: loc.speed_mps, timestamp: loc.timestamp }
}

export const useLocationStore = defineStore('location', () => {
  const lastFix = shallowRef<Fix | null>(null)
  const issue = ref<LocationIssue>(null)
  const reporting = ref(false)
  const now = ref(Date.now())
  let clock: ReturnType<typeof setInterval> | undefined

  /** Re-evaluates freshness while fixes are not arriving. */
  function touch(): void {
    now.value = Date.now()
    clock ??= setInterval(() => (now.value = Date.now()), 10_000)
  }

  const sender = new LocationSender({
    read: readFix,
    send: (fix) => reportLocation(toInput(fix)),
    onFix: (fix) => {
      lastFix.value = fix
      touch()
    },
    onIssue: (i) => (issue.value = i),
  })

  let lifecycleAttached = false
  function attachLifecycle(): void {
    if (lifecycleAttached || typeof document === 'undefined') return
    lifecycleAttached = true
    const tg = useTelegramFullscreen()
    const sync = (): void => sender.setActive(document.visibilityState !== 'hidden' && tg.state.isActive)
    tg.onActiveChange(sync)
    document.addEventListener('visibilitychange', sync)
    sync()
  }

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

  return { lastFix, current, issue, reporting, setReporting, locateOnce, sendNow }
})
