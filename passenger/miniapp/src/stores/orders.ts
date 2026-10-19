// The passenger's order lists: active orders (one per service type at most)
// and the finished history with cursor pagination (GET /orders).

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { listOrders } from '../api/orders'
import type { OrderSummary } from '../api/types'
import { recentDestinations, recentPlaces } from '../domain/places'

export const useOrdersStore = defineStore('orders', () => {
  const active = ref<OrderSummary[]>([])
  const activeLoaded = ref(false)
  const activeError = shallowRef<unknown>(null)

  const history = ref<OrderSummary[]>([])
  const nextCursor = ref<string | null>(null)
  const historyLoaded = ref(false)
  const historyLoading = ref(false)
  const historyError = shallowRef<unknown>(null)
  let historyRequest = 0
  let activeRequest = 0

  /**
   * Locality line of pickup A for orders placed in this session, as the
   * passenger saw it while booking; shown when the API has no locality for
   * the order. Kept in memory only.
   */
  const pickupSubtitles = ref<Record<string, string>>({})

  function rememberPickupSubtitle(orderId: string, subtitle: string | null | undefined): void {
    const text = subtitle?.trim()
    if (text) pickupSubtitles.value = { ...pickupSubtitles.value, [orderId]: text }
  }

  function pickupSubtitle(orderId: string): string | null {
    return pickupSubtitles.value[orderId] ?? null
  }

  const hasMore = computed(() => nextCursor.value !== null)
  const destinations = computed(() => recentDestinations(history.value))
  const places = computed(() => recentPlaces(history.value))

  async function loadActive(): Promise<void> {
    const id = ++activeRequest
    try {
      const page = await listOrders('active', { limit: 10 })
      if (id !== activeRequest) return
      active.value = page.items
      activeError.value = null
      activeLoaded.value = true
    } catch (err) {
      if (id === activeRequest) activeError.value = err
    }
  }

  /** First page (replaces the list). */
  async function loadHistory(): Promise<void> {
    const id = ++historyRequest
    historyLoading.value = true
    try {
      const page = await listOrders('history')
      if (id !== historyRequest) return
      history.value = page.items
      nextCursor.value = page.next_cursor
      historyError.value = null
      historyLoaded.value = true
    } catch (err) {
      if (id === historyRequest) historyError.value = err
    } finally {
      if (id === historyRequest) historyLoading.value = false
    }
  }

  /** Next page after the current cursor; no-op while loading or at the end. */
  async function loadMore(): Promise<void> {
    const cursor = nextCursor.value
    if (!cursor || historyLoading.value) return
    const id = ++historyRequest
    historyLoading.value = true
    try {
      const page = await listOrders('history', { before: cursor })
      if (id !== historyRequest) return
      const seen = new Set(history.value.map((o) => o.id))
      history.value = [...history.value, ...page.items.filter((o) => !seen.has(o.id))]
      nextCursor.value = page.next_cursor
      historyError.value = null
    } catch (err) {
      if (id === historyRequest) historyError.value = err
    } finally {
      if (id === historyRequest) historyLoading.value = false
    }
  }

  /** Loads both lists once per session (home screen, search suggestions). */
  async function ensureLoaded(): Promise<void> {
    await Promise.all([activeLoaded.value ? null : loadActive(), historyLoaded.value ? null : loadHistory()])
  }

  return {
    active,
    activeLoaded,
    activeError,
    history,
    nextCursor,
    historyLoaded,
    historyLoading,
    historyError,
    hasMore,
    destinations,
    places,
    loadActive,
    loadHistory,
    loadMore,
    ensureLoaded,
    rememberPickupSubtitle,
    pickupSubtitle,
  }
})
