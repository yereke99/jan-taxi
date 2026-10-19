// Welcome bonus state of a driver for the detail page (§22.1). The API only
// reports a granted bonus; a missing one is explained from the driver's
// approval: not approved yet, or approved without it (the bonus was off at
// that moment, or the phone or plate had already received it).

import type { AdminDriverDetail } from '../../api/drivers'

export type WelcomeState =
  | { kind: 'granted'; amount: number; at: string }
  | { kind: 'awaiting_approval' }
  | { kind: 'not_granted' }

export function welcomeState(d: Pick<AdminDriverDetail, 'welcome_bonus' | 'approved_at' | 'status'>): WelcomeState {
  if (d.welcome_bonus) return { kind: 'granted', amount: d.welcome_bonus.amount, at: d.welcome_bonus.granted_at }
  if (!d.approved_at && d.status !== 'approved' && d.status !== 'suspended') return { kind: 'awaiting_approval' }
  return { kind: 'not_granted' }
}
