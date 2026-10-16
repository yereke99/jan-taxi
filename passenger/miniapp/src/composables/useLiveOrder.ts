// Reactive wrapper around LiveOrderSync for a screen showing one order.

import { onBeforeUnmount, ref, shallowRef, watch, type Ref } from 'vue'
import { getOrder } from '../api/orders'
import type { Order } from '../api/types'
import { LiveOrderSync } from '../domain/liveOrder'
import { useRealtime } from '../services/realtime'

export interface LiveOrder {
  order: Readonly<Ref<Order | null>>
  /** First load still running. */
  loading: Readonly<Ref<boolean>>
  /** Last load error (cleared by the next success). */
  error: Readonly<Ref<unknown>>
  refresh(): void
  /** Applies a fresher order the screen already has (e.g. after cancel/rate). */
  accept(order: Order): void
}

export function useLiveOrder(orderId: Readonly<Ref<string>>): LiveOrder {
  const order = shallowRef<Order | null>(null)
  const loading = ref(true)
  const error = shallowRef<unknown>(null)
  let sync: LiveOrderSync | null = null

  function start(id: string): void {
    sync?.stop()
    order.value = null
    loading.value = true
    error.value = null
    sync = new LiveOrderSync({
      orderId: id,
      fetchOrder: (oid, signal) => getOrder(oid, signal),
      realtime: useRealtime(),
      onOrder: (o) => {
        order.value = o
        loading.value = false
        error.value = null
      },
      onError: (err) => {
        error.value = err
        loading.value = false
      },
    })
    sync.start()
  }

  watch(orderId, (id) => start(id), { immediate: true })
  onBeforeUnmount(() => sync?.stop())

  return {
    order,
    loading,
    error,
    refresh: () => sync?.refresh(),
    accept: (o) => sync?.accept(o),
  }
}
