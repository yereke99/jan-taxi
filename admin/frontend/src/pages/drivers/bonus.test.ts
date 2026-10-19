import { describe, expect, it } from 'vitest'
import { welcomeState } from './bonus'

describe('welcomeState', () => {
  it('reports a granted welcome bonus', () => {
    expect(welcomeState({ welcome_bonus: { amount: 500, granted_at: '2026-10-19T08:00:00Z' }, approved_at: '2026-10-19T08:00:00Z', status: 'approved' })).toEqual({
      kind: 'granted',
      amount: 500,
      at: '2026-10-19T08:00:00Z',
    })
  })

  it('waits for the first approval', () => {
    expect(welcomeState({ welcome_bonus: null, approved_at: null, status: 'pending' })).toEqual({ kind: 'awaiting_approval' })
  })

  it('explains an approved driver without the bonus', () => {
    expect(welcomeState({ welcome_bonus: null, approved_at: '2026-10-19T08:00:00Z', status: 'approved' })).toEqual({ kind: 'not_granted' })
    expect(welcomeState({ welcome_bonus: null, approved_at: null, status: 'suspended' })).toEqual({ kind: 'not_granted' })
  })
})
