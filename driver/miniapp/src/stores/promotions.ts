import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { getDriverOffers, type DriverOffer } from '../api/promotions'
import { useRealtime } from '../services/realtime'

export const useDriverPromotionsStore = defineStore('driver-promotions', () => {
  const offers = shallowRef<DriverOffer[]>([])
  const loading = ref(false)
  const loaded = ref(false)
  const error = shallowRef<unknown>(null)
  const gift = computed(() => offers.value.find((o) => !o.rewarded) ?? null)
  let pending: Promise<void> | null = null
  let watching = false
  function refresh(): Promise<void> {
    if (pending) return pending
    if (!watching) {
      try {
        const rt = useRealtime()
        for (const event of ['wallet.updated', 'order.completed', 'promotions.updated']) rt.on(event, () => void refresh())
        rt.onResync(() => void refresh())
        watching = true
      } catch { /* A later read retries after startup. */ }
    }
    loading.value = true
    pending = getDriverOffers().then((next) => { offers.value = next; error.value = null; loaded.value = true })
      .catch((err: unknown) => { error.value = err; offers.value = [] })
      .finally(() => { loading.value = false; pending = null })
    return pending
  }
  return { offers, gift, loading, loaded, error, refresh }
})
