// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Promise-based iOS action sheet for confirmations (§15: never browser
// confirm()). <JActionSheetHost /> in App.vue renders the active request.
//
//   const choice = await showActionSheet({ title, actions: [{ id: 'cancel_order', label, destructive: true }] })
//   if (choice === 'cancel_order') ...

import { shallowRef } from 'vue'
import type { ActionSheetAction } from './types'

export interface ActionSheetRequest {
  title?: string
  message?: string
  actions: ActionSheetAction[]
  /** Defaults to the localised "Cancel". */
  cancelLabel?: string
}

interface ActiveRequest extends ActionSheetRequest {
  resolve: (id: string | null) => void
}

export const activeActionSheet = shallowRef<ActiveRequest | null>(null)

/** Resolves with the chosen action id, or null when cancelled/dismissed. */
export function showActionSheet(req: ActionSheetRequest): Promise<string | null> {
  activeActionSheet.value?.resolve(null)
  return new Promise((resolve) => {
    activeActionSheet.value = {
      ...req,
      resolve: (id) => {
        activeActionSheet.value = null
        resolve(id)
      },
    }
  })
}

/** Convenience wrapper for a single destructive confirmation. */
export async function confirmDestructive(opts: { title?: string; message?: string; confirmLabel: string; cancelLabel?: string }): Promise<boolean> {
  const id = await showActionSheet({
    title: opts.title,
    message: opts.message,
    cancelLabel: opts.cancelLabel,
    actions: [{ id: 'confirm', label: opts.confirmLabel, destructive: true }],
  })
  return id === 'confirm'
}
