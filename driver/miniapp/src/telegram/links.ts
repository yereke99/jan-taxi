// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { openLink, openTelegramLink } from '@tma.js/sdk'
import { safeBool, safeCall } from './safe'

function parseUrl(url: string): URL | null {
  return safeCall(() => new URL(url)) ?? null
}

/**
 * Opens an external http(s) link (2GIS, Kaspi, support site) in the system
 * browser. Must be called from a user gesture: Telegram ignores openLink
 * otherwise. Returns false for a rejected URL.
 */
export function openExternal(url: string): boolean {
  const u = parseUrl(url)
  if (!u || (u.protocol !== 'https:' && u.protocol !== 'http:')) return false
  if (safeBool(() => openLink.isAvailable())) {
    const ok = safeCall(() => {
      openLink(u.toString(), { tryInstantView: false })
      return true
    })
    if (ok) return true
  }
  window.open(u.toString(), '_blank', 'noopener,noreferrer')
  return true
}

/**
 * Opens a t.me link inside Telegram (bot chats, direct Mini App links).
 * Only https://t.me/... is accepted, matching the SDK's own rule.
 */
export function openTelegram(url: string): boolean {
  const u = parseUrl(url)
  if (!u || u.protocol !== 'https:' || u.hostname !== 't.me') return false
  if (safeBool(() => openTelegramLink.isAvailable())) {
    const ok = safeCall(() => {
      openTelegramLink(u.toString())
      return true
    })
    if (ok) return true
  }
  window.open(u.toString(), '_blank', 'noopener,noreferrer')
  return true
}

/**
 * Starts a phone call (tel: link, e.g. "112" or "+77011234567"). Must be
 * called from a user gesture. openLink accepts only http(s), so this follows
 * a tel: anchor, and in a new window: Telegram for iOS lets its WebView load
 * a same-window tel: link, which WKWebView cannot open, so the tap silently
 * does nothing, while a new-window link goes to the system call prompt.
 * Android hands tel: links to the dialer either way; Telegram Desktop passes
 * new-window links to the operating system. Call buttons in templates use the
 * same attributes (docs/TESTING.md §6).
 */
export function callPhone(number: string): boolean {
  const digits = number.replace(/[^\d+]/g, '')
  if (!/^\+?\d{2,15}$/.test(digits)) return false
  const a = document.createElement('a')
  a.href = `tel:${digits}`
  a.target = '_blank'
  a.rel = 'noopener noreferrer'
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  a.remove()
  return true
}

/** Builds a t.me direct link that opens a bot's Main Mini App (§18). */
export function directLink(botUsername: string, startParam?: string, fullscreen = true): string {
  const u = new URL(`https://t.me/${encodeURIComponent(botUsername.replace(/^@/, ''))}`)
  u.searchParams.set('startapp', startParam ?? '')
  if (fullscreen) u.searchParams.set('mode', 'fullscreen')
  return u.toString()
}
