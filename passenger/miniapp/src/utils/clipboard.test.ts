// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { afterEach, describe, expect, it, vi } from 'vitest'
import { copyText } from './clipboard'

function stub(target: object, key: string, value: unknown): void {
  Object.defineProperty(target, key, { value, configurable: true, writable: true })
}

afterEach(() => {
  Reflect.deleteProperty(navigator, 'clipboard')
  Reflect.deleteProperty(document, 'execCommand')
})

describe('copyText', () => {
  it('uses the Clipboard API when the WebView allows it', async () => {
    const writeText = vi.fn(async () => undefined)
    stub(navigator, 'clipboard', { writeText })
    await expect(copyText('+77012345678')).resolves.toBe(true)
    expect(writeText).toHaveBeenCalledExactlyOnceWith('+77012345678')
  })

  it('falls back to a temporary selection and removes it again', async () => {
    stub(navigator, 'clipboard', { writeText: vi.fn(async () => Promise.reject(new Error('denied'))) })
    let selected = ''
    stub(document, 'execCommand', vi.fn(() => {
      selected = document.querySelector('textarea')?.value ?? ''
      return true
    }))
    await expect(copyText('+77012345678')).resolves.toBe(true)
    expect(selected).toBe('+77012345678')
    expect(document.querySelector('textarea')).toBeNull()
  })

  it('reports a failure when no method can copy', async () => {
    stub(document, 'execCommand', vi.fn(() => {
      throw new Error('unsupported')
    }))
    await expect(copyText('+77012345678')).resolves.toBe(false)
    expect(document.querySelector('textarea')).toBeNull()
  })
})
