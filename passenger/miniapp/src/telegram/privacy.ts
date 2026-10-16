// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { requestPhoneAccess, requestWriteAccess as sdkRequestWriteAccess } from '@tma.js/sdk'
import { safeBool } from './safe'

export type ContactRequestResult = 'sent' | 'cancelled' | 'failed' | 'unavailable'
export type WriteAccessResult = 'allowed' | 'denied' | 'unavailable'

/**
 * Asks the user to share their phone number with the bot (Mini Apps 6.9+).
 *
 * Only the request is made here: Telegram delivers the contact to the bot as a
 * message, the bot verifies contact.user_id == from.id and reports it to the
 * API (ARCHITECTURE §11). The client never forwards the number itself, so the
 * phone cannot be spoofed from the WebView. Callers re-fetch their state after
 * 'sent'. 'cancelled' means the user declined in Telegram's dialog; 'failed'
 * means the request itself did not complete (no client answer, timeout), so
 * the UI must not tell the user they refused.
 */
export async function requestContact(): Promise<ContactRequestResult> {
  if (!safeBool(() => requestPhoneAccess.isAvailable())) return 'unavailable'
  try {
    const status = await requestPhoneAccess()
    return status === 'sent' ? 'sent' : 'cancelled'
  } catch {
    return 'failed'
  }
}

/** Asks for permission for the bot to message the user (Mini Apps 6.9+). */
export async function requestWriteAccess(): Promise<WriteAccessResult> {
  if (!safeBool(() => sdkRequestWriteAccess.isAvailable())) return 'unavailable'
  try {
    const status = await sdkRequestWriteAccess()
    return status === 'allowed' ? 'allowed' : 'denied'
  } catch {
    return 'denied'
  }
}
