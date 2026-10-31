// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { callPhone, openExternal } from './links'

const sdk = vi.hoisted(() => ({
  openLink: Object.assign(vi.fn(), { isAvailable: vi.fn(() => false) }),
  openTelegramLink: Object.assign(vi.fn(), { isAvailable: vi.fn(() => false) }),
}))

vi.mock('@tma.js/sdk', () => ({ openLink: sdk.openLink, openTelegramLink: sdk.openTelegramLink }))

describe('callPhone', () => {
  let clicked: HTMLAnchorElement[]

  beforeEach(() => {
    clicked = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this)
    })
  })

  it('follows the tel: link in a new window, which Telegram hands to the phone app', () => {
    expect(callPhone('+7 701 234-56-78')).toBe(true)
    expect(clicked).toHaveLength(1)
    const a = clicked[0]!
    expect(a.getAttribute('href')).toBe('tel:+77012345678')
    expect(a.target).toBe('_blank')
    expect(a.rel).toBe('noopener noreferrer')
    expect(a.isConnected).toBe(false)

    expect(callPhone('112')).toBe(true)
    expect(clicked[1]!.getAttribute('href')).toBe('tel:112')
  })

  it.each(['', '1', 'call me', '+7701234567890123'])('refuses %j without following anything', (number) => {
    expect(callPhone(number)).toBe(false)
    expect(clicked).toHaveLength(0)
  })
})

describe('openExternal', () => {
  beforeEach(() => {
    sdk.openLink.mockReset()
    sdk.openLink.isAvailable.mockReturnValue(false)
    // safeCall reports skipped SDK calls in development builds.
    vi.spyOn(console, 'debug').mockImplementation(() => undefined)
  })

  it('opens an https link through Telegram when the client supports openLink', () => {
    sdk.openLink.isAvailable.mockReturnValue(true)
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    expect(openExternal('https://wa.me/77017654321')).toBe(true)
    expect(sdk.openLink).toHaveBeenCalledExactlyOnceWith('https://wa.me/77017654321', { tryInstantView: false })
    expect(open).not.toHaveBeenCalled()
  })

  it('uses a new browser window outside Telegram or when the client refuses openLink', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    expect(openExternal('https://wa.me/77017654321')).toBe(true)
    expect(open).toHaveBeenLastCalledWith('https://wa.me/77017654321', '_blank', 'noopener,noreferrer')

    sdk.openLink.isAvailable.mockReturnValue(true)
    sdk.openLink.mockImplementation(() => {
      throw new Error('unsupported')
    })
    expect(openExternal('https://wa.me/77017654321')).toBe(true)
    expect(open).toHaveBeenCalledTimes(2)
  })

  it.each(['tel:+77012345678', 'javascript:alert(1)', 'whatsapp://send?phone=77012345678', 'wa.me/77012345678'])('rejects %j', (url) => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    expect(openExternal(url)).toBe(false)
    expect(sdk.openLink).not.toHaveBeenCalled()
    expect(open).not.toHaveBeenCalled()
  })
})
