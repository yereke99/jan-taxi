// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { retrieveRawInitData } from '@tma.js/sdk'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getTelegramPhotoUrl } from './photo'

vi.mock('@tma.js/sdk', () => ({ retrieveRawInitData: vi.fn() }))

const photo = 'https://t.me/i/userpic/320/portrait.jpg'

function launch(user: unknown): void {
  vi.mocked(retrieveRawInitData).mockReturnValue(new URLSearchParams({ user: JSON.stringify(user) }).toString())
}

beforeEach(() => {
  vi.mocked(retrieveRawInitData).mockReset()
})

describe('Telegram profile photo', () => {
  it('uses the current authenticated user’s launch photo, including SVG', () => {
    launch({ id: 424242, photo_url: photo })
    expect(getTelegramPhotoUrl(424242)).toBe(photo)
    launch({ id: 424242, photo_url: 'https://t.me/i/userpic/320/portrait.svg' })
    expect(getTelegramPhotoUrl(424242)).toBe('https://t.me/i/userpic/320/portrait.svg')
  })

  it('does not reuse a different account’s photo or show one before authentication', () => {
    launch({ id: 424242, photo_url: photo })
    for (const id of [undefined, 0, -1, 42, NaN, Infinity, 424242.5]) expect(getTelegramPhotoUrl(id)).toBeNull()
    launch({ id: '424242', photo_url: photo })
    expect(getTelegramPhotoUrl(424242)).toBeNull()
  })

  it.each([undefined, null, '', '  ', 123, 'http://t.me/a.jpg', 'javascript:alert(1)', 'data:image/svg+xml,test', '/photo.jpg', 'https://name:secret@t.me/a.jpg'])(
    'ignores unavailable or unsafe photos: %s',
    (value) => {
      launch({ id: 424242, photo_url: value })
      expect(getTelegramPhotoUrl(424242)).toBeNull()
    },
  )

  it('handles unavailable or malformed launch data without breaking the page', () => {
    for (const raw of ['', 'user=bad-json', 'user=null', 'user=42', 'user=%5B%5D']) {
      vi.mocked(retrieveRawInitData).mockReturnValue(raw)
      expect(getTelegramPhotoUrl(424242)).toBeNull()
    }
    vi.mocked(retrieveRawInitData).mockImplementation(() => { throw new Error('outside Telegram') })
    expect(getTelegramPhotoUrl(424242)).toBeNull()
  })
})
