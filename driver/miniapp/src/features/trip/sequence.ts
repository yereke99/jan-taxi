// What the driver does next on an active trip (ARCHITECTURE §5):
//   accepted         → "arrived" (driver_arriving = waiting at the pickup)
//   driver_arriving  → start the trip
//   in_progress      → complete intermediate stops strictly in seq order,
//                      then complete the trip (marks the destination).
// The server's can_* flags are authoritative; this only orders the actions.

import { STOP_LETTERS } from '../../maps/constants'
import type { DriverOrderDetail, Stop } from '../../api/types'

export type TripAction =
  | { kind: 'arrive' }
  | { kind: 'start' }
  | { kind: 'complete_stop'; stopId: string; seq: number; letter: string }
  | { kind: 'complete' }

export type TripState = Pick<DriverOrderDetail, 'status' | 'stops' | 'can_arrive' | 'can_start' | 'can_complete' | 'can_cancel'>

export function stopLetter(seq: number): string {
  return STOP_LETTERS[seq] ?? String(seq + 1)
}

export function sortedStops<T extends Pick<Stop, 'seq'>>(stops: readonly T[]): T[] {
  return [...stops].sort((a, b) => a.seq - b.seq)
}

/** Intermediate stops not yet completed, in seq order. */
export function pendingIntermediate(stops: readonly Stop[]): Stop[] {
  return sortedStops(stops).filter((s) => s.kind === 'intermediate' && !s.completed_at)
}

/** The single primary action for the current state, or null when none applies. */
export function primaryAction(o: TripState): TripAction | null {
  switch (o.status) {
    case 'accepted':
      if (o.can_arrive) return { kind: 'arrive' }
      return o.can_start ? { kind: 'start' } : null
    case 'driver_arriving':
      return o.can_start ? { kind: 'start' } : null
    case 'in_progress': {
      const next = pendingIntermediate(o.stops)[0]
      if (next) return { kind: 'complete_stop', stopId: next.id, seq: next.seq, letter: stopLetter(next.seq) }
      return o.can_complete ? { kind: 'complete' } : null
    }
    default:
      return null
  }
}

/** Cancelling is only possible before the trip starts. */
export function canDriverCancel(o: TripState): boolean {
  return o.can_cancel && (o.status === 'accepted' || o.status === 'driver_arriving')
}

/** Stops still to drive to: the pickup before the start, then every uncompleted later stop. */
export function remainingStops(o: Pick<DriverOrderDetail, 'status' | 'stops'>): Stop[] {
  const stops = sortedStops(o.stops)
  if (o.status === 'accepted' || o.status === 'driver_arriving') return stops
  if (o.status === 'in_progress') return stops.filter((s) => s.seq > 0 && !s.completed_at)
  return []
}

/** The stop the driver is heading to now. */
export function currentTarget(o: Pick<DriverOrderDetail, 'status' | 'stops'>): Stop | null {
  return remainingStops(o)[0] ?? null
}

export function isActiveTripStatus(status: string): boolean {
  return status === 'accepted' || status === 'driver_arriving' || status === 'in_progress'
}
