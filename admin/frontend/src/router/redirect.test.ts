import { describe, expect, it, vi } from 'vitest'

// The router module installs guards that need Pinia; only the pure helper is tested here.
vi.mock('../stores/auth', () => ({ useAuthStore: vi.fn() }))

const { safeRedirect } = await import('./index')

describe('safeRedirect', () => {
  it.each([
    ['/orders?status=searching', '/orders?status=searching'],
    ['/', '/'],
    ['//evil.example/x', null],
    ['https://evil.example', null],
    ['/login?redirect=/x', null],
    [undefined, null],
    [['/orders'], null],
  ])('%j → %j', (input, expected) => {
    expect(safeRedirect(input)).toBe(expected)
  })
})
