// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Public entry point for everything Telegram. App code imports from
// '@/telegram' only, never from '@tma.js/sdk' directly, so SDK upgrades stay
// contained in this folder.

import { retrieveRawInitData } from '@tma.js/sdk'
import { applyLowEndFlag } from '../utils/device'
import { safeCall } from './safe'
import { useTelegramFullscreen, type TelegramFullscreen } from './useTelegramFullscreen'

export { useTelegramFullscreen, createTelegramFullscreen, sanitizeStartParam, effectiveSafeInsets } from './useTelegramFullscreen'
export type { Insets, TelegramViewportState, TelegramFullscreen, FullscreenResult } from './useTelegramFullscreen'
export { haptics } from './haptics'
export type { ImpactStyle, NotificationType } from './haptics'
export { openExternal, openTelegram, directLink } from './links'
export { requestContact, requestWriteAccess } from './privacy'
export type { ContactRequestResult, WriteAccessResult } from './privacy'
export { canOpenLocationSettings, getLocation, openLocationSettings, LocationError, validateLocation, watchLocation } from './location'
export type { DeviceLocation, LocationErrorCode, LocationSource, GetLocationOptions, WatchLocationOptions } from './location'
export { navigateBack, useBackButton, shouldShowBack } from './backButton'
export { useSignal } from '@tma.js/sdk-vue'

/**
 * Starts the Telegram shell. Call (and await) before app.mount() so the first
 * route already renders with the final safe areas and viewport height.
 * Never throws: outside Telegram it configures browser fallbacks.
 */
export async function bootTelegram(): Promise<TelegramFullscreen> {
  const tg = useTelegramFullscreen()
  await tg.start()
  applyLowEndFlag(tg.state.platform)
  return tg
}

/** True when running inside Telegram (after bootTelegram). */
export function isTelegram(): boolean {
  return useTelegramFullscreen().state.isTelegram
}

/**
 * Raw, signed initData for POST /auth/telegram/{app}. Undefined outside
 * Telegram or when the launch carried no initData (e.g. reply-keyboard
 * web_app buttons, which §18 forbids).
 */
export function getRawInitData(): string | undefined {
  const raw = safeCall(() => retrieveRawInitData())
  return raw && raw.length > 0 ? raw : undefined
}
