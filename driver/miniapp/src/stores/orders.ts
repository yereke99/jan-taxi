// Available orders (GET /driver/orders): refreshed on realtime hints
// (order.available / order.taken), pull-to-refresh and a polling fallback
// while the list is on screen.

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { listAvailable } from '../api/orders'
import type { DriverOrderCard, Eligibility } from '../api/types'

export const useOrdersStore = defineStore('orders', () => {
  const items = ref<DriverOrderCard[]>([])
  const eligibility = ref<Eligibility | null>(null)
  const loading = ref(false)
  const loaded = ref(false)
  const error = shallowRef<unknown>(null)
  /** Ids removed locally (taken by someone else) until the next refresh confirms it. */
  const removed = new Set<string>()
  let inFlight: Promise<void> | null = null
  let again = false

  const visible = computed(() => items.value)
  const count = computed(() => items.value.length)

  async function load(): Promise<void> {
    loading.value = true
    try {
      const res = await listAvailable()
      items.value = res.items.filter((o) => !removed.has(o.id))
      removed.clear()
      eligibility.value = res.eligibility
      error.value = null
      loaded.value = true
    } catch (err) {
      error.value = err
    } finally {
      loading.value = false
    }
  }

  /** Re-fetches the list; concurrent calls share one request (and one follow-up). */
  function refresh(): Promise<void> {
    if (inFlight) {
      again = true
      return inFlight
    }
    inFlight = (async () => {
      do {
        again = false
        await load()
      } while (again)
    })().finally(() => {
      inFlight = null
    })
    return inFlight
  }

  /** Drops an order that is no longer available (taken, cancelled, expired). */
  function remove(orderId: string): void {
    removed.add(orderId)
    items.value = items.value.filter((o) => o.id !== orderId)
  }

  function find(orderId: string): DriverOrderCard | undefined {
    return items.value.find((o) => o.id === orderId)
  }

  function reset(): void {
    items.value = []
    eligibility.value = null
    loaded.value = false
    error.value = null
  }

  return { items: visible, eligibility, loading, loaded, error, count, refresh, remove, find, reset }
})
