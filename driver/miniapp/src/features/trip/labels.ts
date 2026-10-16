// Labels for trip actions and the driver's cancellation reasons. The chosen
// reason is sent as text in the driver's language (the API shows it to the
// passenger and operators, DriverOrderCancelRequest.reason).

import type { Category, OrderStatus } from '../../api/types'
import type { TripAction } from './sequence'

export const CANCEL_REASONS = [
  'passenger_no_show',
  'sender_no_show',
  'passenger_asked',
  'cannot_reach',
  'vehicle_problem',
  'unsafe_cargo',
] as const
export type CancelReason = (typeof CANCEL_REASONS)[number]

/** Reasons that make sense now: "no show" only once waiting at the pickup, cargo reasons only for deliveries. */
export function cancelReasons(category: Category, status: OrderStatus): CancelReason[] {
  const taxi = category === 'taxi'
  return CANCEL_REASONS.filter((r) => {
    if (r === 'passenger_no_show') return taxi && status === 'driver_arriving'
    if (r === 'sender_no_show') return !taxi && status === 'driver_arriving'
    if (r === 'unsafe_cargo') return !taxi
    return true
  })
}

/** i18n key of the primary button for an action ("trip.stop_done" takes {letter}). */
export function actionLabelKey(a: TripAction): string {
  switch (a.kind) {
    case 'arrive':
      return 'trip.arrive'
    case 'start':
      return 'trip.start'
    case 'complete_stop':
      return 'trip.stop_done'
    case 'complete':
      return 'trip.complete'
  }
}
