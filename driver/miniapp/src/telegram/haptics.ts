// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Telegram HapticFeedback (§15 "Feedback"): selection on segmented/picker
// changes, light impact on primary taps, notification on results. Every call
// is a no-op outside Telegram or on clients without haptics.

import { hapticFeedback } from '@tma.js/sdk'
import { safeCall } from './safe'

export type ImpactStyle = 'light' | 'medium' | 'heavy' | 'rigid' | 'soft'
export type NotificationType = 'success' | 'error' | 'warning'

export const haptics = {
  selection(): void {
    safeCall(() => hapticFeedback.selectionChanged.ifAvailable())
  },
  impact(style: ImpactStyle = 'light'): void {
    safeCall(() => hapticFeedback.impactOccurred.ifAvailable(style))
  },
  notification(type: NotificationType): void {
    safeCall(() => hapticFeedback.notificationOccurred.ifAvailable(type))
  },
}
