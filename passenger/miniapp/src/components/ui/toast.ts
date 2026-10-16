// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Top-banner toasts. Any code calls toast.show(...); <JToast /> in App.vue
// renders the queue. Messages are plain text (never HTML).

import { reactive, readonly } from 'vue'
import { haptics } from '../../telegram/haptics'

export type ToastTone = 'neutral' | 'success' | 'error'

export interface ToastOptions {
  message: string
  tone?: ToastTone
  /** Milliseconds before auto-dismiss; 0 keeps it until dismissed. */
  duration?: number
  /** Optional single action, e.g. "Open settings". */
  action?: { label: string; run: () => void }
}

export interface ToastItem extends Required<Omit<ToastOptions, 'action'>> {
  id: number
  action: ToastOptions['action'] | null
}

const MAX_VISIBLE = 3
const DEFAULT_DURATION = 3500

const items = reactive<ToastItem[]>([])
const timers = new Map<number, ReturnType<typeof setTimeout>>()
let nextId = 1

function dismiss(id: number): void {
  const i = items.findIndex((t) => t.id === id)
  if (i >= 0) items.splice(i, 1)
  clearTimeout(timers.get(id))
  timers.delete(id)
}

function show(opts: ToastOptions | string): number {
  const o: ToastOptions = typeof opts === 'string' ? { message: opts } : opts
  const item: ToastItem = {
    id: nextId++,
    message: o.message,
    tone: o.tone ?? 'neutral',
    duration: o.duration ?? DEFAULT_DURATION,
    action: o.action ?? null,
  }
  // Identical message already visible: refresh it instead of stacking.
  const dup = items.find((t) => t.message === item.message && t.tone === item.tone)
  if (dup) dismiss(dup.id)
  items.push(item)
  while (items.length > MAX_VISIBLE) dismiss(items[0]!.id)
  if (item.tone === 'error') haptics.notification('error')
  else if (item.tone === 'success') haptics.notification('success')
  if (item.duration > 0) timers.set(item.id, setTimeout(() => dismiss(item.id), item.duration))
  return item.id
}

export const toast = {
  show,
  success: (message: string, duration?: number) => show({ message, tone: 'success', duration }),
  error: (message: string, duration?: number) => show({ message, tone: 'error', duration }),
  dismiss,
  clear(): void {
    for (const t of [...items]) dismiss(t.id)
  },
  /** Reactive read-only queue for the host component. */
  items: readonly(items),
}

export function useToast(): typeof toast {
  return toast
}
