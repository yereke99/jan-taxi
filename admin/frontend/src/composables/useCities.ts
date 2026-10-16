// City list shared by filters and forms; loaded once per session and
// refreshed after the cities screen changes something.

import { computed, ref, shallowRef } from 'vue'
import { citiesApi, type City } from '../api/config'
import { useFormat } from './useFormat'

const cities = shallowRef<City[]>([])
const loaded = ref(false)
let inflight: Promise<void> | null = null

export function loadCities(force = false): Promise<void> {
  if (loaded.value && !force) return Promise.resolve()
  inflight ??= citiesApi
    .list()
    .then((page) => {
      cities.value = page.items
      loaded.value = true
    })
    .catch((err: unknown) => {
      // Filters still work without names; the error surfaces on the cities page.
      console.warn('[cities] list failed', err)
    })
    .finally(() => {
      inflight = null
    })
  return inflight
}

export function setCities(list: City[]): void {
  cities.value = list
  loaded.value = true
}

export function useCities() {
  const fmt = useFormat()
  void loadCities()
  const options = computed(() => cities.value.map((c) => ({ value: c.id, label: `${fmt.name(c.name)} · ${c.code}` })))
  const byId = (id: string | null | undefined): City | undefined => cities.value.find((c) => c.id === id)
  return { cities, loaded, options, byId }
}
