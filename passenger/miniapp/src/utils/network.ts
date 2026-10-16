// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { readonly, ref, type Ref } from 'vue'

const online = ref(typeof navigator === 'undefined' ? true : navigator.onLine)
let listening = false

function listen(): void {
  if (listening || typeof window === 'undefined') return
  listening = true
  window.addEventListener('online', () => (online.value = true))
  window.addEventListener('offline', () => (online.value = false))
}

/** Reactive browser connectivity flag (navigator.onLine + online/offline events). */
export function useOnlineStatus(): Readonly<Ref<boolean>> {
  listen()
  return readonly(online)
}
