// Promise-based confirmation dialog: `if (await confirmAction({...})) ...`.
// Rendered by <ConfirmHost /> in App.vue.

import { shallowRef } from 'vue'

export interface ConfirmRequest {
  title: string
  message?: string
  confirmLabel?: string
  cancelLabel?: string
  destructive?: boolean
}

interface Active extends ConfirmRequest {
  resolve: (ok: boolean) => void
}

export const activeConfirm = shallowRef<Active | null>(null)

export function confirmAction(req: ConfirmRequest): Promise<boolean> {
  activeConfirm.value?.resolve(false)
  return new Promise((resolve) => {
    activeConfirm.value = {
      ...req,
      resolve: (ok) => {
        activeConfirm.value = null
        resolve(ok)
      },
    }
  })
}
