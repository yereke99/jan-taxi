// Passenger phone sharing (ARCHITECTURE §22.5). Before the first order the
// app asks once to share the phone through Telegram so the driver can call;
// the passenger may skip, and the answer is remembered on this device.
//
// Sharing only asks Telegram: the contact goes to the passenger bot, which
// checks contact.user_id == from.id and stores it through the API. The app
// then waits for GET /me to report phone_verified (realtime
// user.phone_verified, with polling as the fallback). The order never waits
// longer than PHONE_WAIT_MS, and "Not now" stops waiting at once.

import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { useRealtime } from '../services/realtime'
import { requestContact } from '../telegram'
import { useSessionStore } from './session'

export const PHONE_WAIT_MS = 15_000
export const PHONE_POLL_MS = 2_000
export const PHONE_VERIFIED_EVENT = 'user.phone_verified'

export type PhoneChoice = 'shared' | 'skipped'
export type PhoneFlow = 'idle' | 'requesting' | 'waiting'
/** How a share attempt ended. */
export type ShareOutcome = 'verified' | 'timeout' | 'declined' | 'unavailable' | 'failed' | 'stopped'

const STORAGE_PREFIX = 'jt.passenger.phone_prompt.'

function storageKey(userId: string): string {
  return STORAGE_PREFIX + userId
}

export function readPhoneChoice(userId: string): PhoneChoice | null {
  try {
    const v = localStorage.getItem(storageKey(userId))
    return v === 'shared' || v === 'skipped' ? v : null
  } catch {
    return null
  }
}

export function writePhoneChoice(userId: string, choice: PhoneChoice): void {
  try {
    localStorage.setItem(storageKey(userId), choice)
  } catch {
    // Storage unavailable (private mode): the prompt may show again next time.
  }
}

export const usePhoneStore = defineStore('phone', () => {
  const session = useSessionStore()

  /** The pre-order prompt is open. */
  const open = ref(false)
  const flow = ref<PhoneFlow>('idle')
  const verified = computed(() => session.me?.phone_verified === true)

  let release: ((proceed: boolean) => void) | null = null
  let stopWaiting: (() => void) | null = null
  /** skip() ended the current wait. */
  let interrupted = false

  function remember(choice: PhoneChoice): void {
    const id = session.me?.id
    if (id) writePhoneChoice(id, choice)
  }

  /** Not verified and never answered on this device. */
  function needsPrompt(): boolean {
    const me = session.me
    return me !== null && !me.phone_verified && readPhoneChoice(me.id) === null
  }

  /**
   * Shows the prompt when the passenger has not shared or skipped yet.
   * Resolves true when the order may go ahead (shared, skipped or not asked:
   * sharing is optional), false when the prompt was dismissed because the
   * screen closed (abandon()).
   */
  async function beforeOrder(): Promise<boolean> {
    if (!needsPrompt()) return true
    // The phone may have been shared in the bot since the app opened.
    await session.refreshMe()
    if (!needsPrompt()) return true
    open.value = true
    return new Promise<boolean>((resolve) => {
      release = resolve
    })
  }

  function close(proceed = true): void {
    open.value = false
    const r = release
    release = null
    r?.(proceed)
  }

  /** The screen that asked is going away: close without an answer and without ordering. */
  function abandon(): void {
    if (flow.value === 'waiting') interrupted = true
    stopWaiting?.()
    close(false)
  }

  /** "Not now": remembered, and any wait for the bot ends. */
  function skip(): void {
    if (flow.value === 'waiting') interrupted = true
    else remember('skipped')
    stopWaiting?.()
    close()
  }

  /** Resolves true once /me reports a verified phone, false after PHONE_WAIT_MS or skip(). */
  function waitVerified(): Promise<boolean> {
    return new Promise((resolve) => {
      let done = false
      let timer: ReturnType<typeof setTimeout> | undefined
      let offEvent: (() => void) | undefined
      const finish = (ok: boolean): void => {
        if (done) return
        done = true
        clearTimeout(timer)
        offEvent?.()
        stopWaiting = null
        resolve(ok)
      }
      const deadline = Date.now() + PHONE_WAIT_MS
      const check = async (): Promise<void> => {
        if (done) return
        await session.refreshMe()
        if (verified.value) return finish(true)
        if (Date.now() >= deadline) return finish(false)
        clearTimeout(timer)
        timer = setTimeout(() => void check(), Math.min(PHONE_POLL_MS, Math.max(0, deadline - Date.now())))
      }
      stopWaiting = () => finish(false)
      try {
        offEvent = useRealtime().on(PHONE_VERIFIED_EVENT, () => void check())
      } catch {
        // No realtime client (tests, failed start): polling alone.
      }
      void check()
    })
  }

  /**
   * Asks Telegram to share the phone with the passenger bot and waits for the
   * verification. Used by the prompt and by the profile screen.
   */
  async function share(): Promise<ShareOutcome> {
    if (flow.value !== 'idle') return 'stopped'
    flow.value = 'requesting'
    try {
      const result = await requestContact()
      if (result === 'cancelled') {
        remember('skipped')
        return 'declined'
      }
      if (result === 'unavailable') {
        remember('skipped')
        return 'unavailable'
      }
      // 'failed': the request did not complete; the prompt may ask again later.
      if (result === 'failed') return 'failed'
      remember('shared')
      if (verified.value) return 'verified'
      flow.value = 'waiting'
      interrupted = false
      if (await waitVerified()) return 'verified'
      return interrupted ? 'stopped' : 'timeout'
    } finally {
      flow.value = 'idle'
      close()
    }
  }

  return { open, flow, verified, needsPrompt, beforeOrder, share, skip, abandon }
})
