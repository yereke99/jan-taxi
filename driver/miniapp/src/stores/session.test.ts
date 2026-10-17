import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { ApiError } from '../services/http'
import { useSessionStore } from './session'

describe('session access errors', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('recognises a profile that is missing or no longer approved', () => {
    const s = useSessionStore()
    expect(s.handleAccessError(new ApiError(403, 'driver_not_registered', ''))).toBe(true)
    expect(s.handleAccessError(new ApiError(403, 'driver_not_approved', ''))).toBe(true)
  })

  it('leaves every other error to the screen', () => {
    const s = useSessionStore()
    expect(s.handleAccessError(new ApiError(403, 'forbidden', ''))).toBe(false)
    expect(s.handleAccessError(new ApiError(409, 'driver_not_approved', ''))).toBe(false)
    expect(s.handleAccessError(new ApiError(0, 'network_error', ''))).toBe(false)
    expect(s.handleAccessError(new Error('x'))).toBe(false)
  })
})
