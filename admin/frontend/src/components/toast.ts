// Toast notifications for the admin panel; rendered by <ToastHost /> in App.vue.

import { reactive, readonly } from 'vue'

export type ToastTone = 'neutral' | 'success' | 'error'

export interface ToastItem {
  id: number
  message: string
  tone: ToastTone
}

const DURATION_MS = 4000
const MAX_VISIBLE = 4
const items = reactive<ToastItem[]>([])
let nextId = 1

function dismiss(id: number): void {
  const i = items.findIndex((t) => t.id === id)
  if (i >= 0) items.splice(i, 1)
}

function show(message: string, tone: ToastTone = 'neutral', duration = DURATION_MS): number {
  const id = nextId++
  items.push({ id, message, tone })
  while (items.length > MAX_VISIBLE) items.shift()
  if (duration > 0) setTimeout(() => dismiss(id), duration)
  return id
}

export const toast = {
  show,
  success: (message: string) => show(message, 'success'),
  error: (message: string) => show(message, 'error', 6000),
  dismiss,
  items: readonly(items),
}
