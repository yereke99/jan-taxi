// Labels for trip actions. Drivers have no cancel action (§22.3).

import type { TripAction } from './sequence'

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
