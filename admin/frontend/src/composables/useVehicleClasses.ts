// Vehicle classes shared by tariffs, drivers, orders and the classes screen.
// Operators with config.manage read every class (inactive ones too) from the
// admin endpoint; everyone else gets the active classes from the public config.

import { computed, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LocalizedText } from '../api/common'
import { vehicleClassesApi, type PublicVehicleClass, type VehicleClass } from '../api/vehicleClasses'
import { api } from '../services/api'
import { useAuthStore } from '../stores/auth'
import { useFormat } from './useFormat'

/** A class as far as the operator may see it (serves/sort/is_active only with config.manage). */
export type KnownVehicleClass = PublicVehicleClass & Partial<Pick<VehicleClass, 'serves' | 'sort' | 'is_active'>>

const classes = shallowRef<KnownVehicleClass[]>([])
const loaded = ref(false)
let inflight: Promise<void> | null = null

export function loadVehicleClasses(full: boolean, force = false): Promise<void> {
  if (loaded.value && !force) return Promise.resolve()
  inflight ??= (full
    ? vehicleClassesApi.list().then((r) => r.items)
    : api.get<{ vehicle_classes?: PublicVehicleClass[] }>('/public/config', { query: { app: 'admin' } }).then((c) => c.vehicle_classes ?? [])
  )
    .then((items) => {
      classes.value = items
      loaded.value = true
    })
    .catch((err: unknown) => {
      // Codes are shown instead of names; the classes screen reports the error.
      console.warn('[vehicle classes] list failed', err)
    })
    .finally(() => {
      inflight = null
    })
  return inflight
}

/** Replaces the cached list after the classes screen changed something. */
export function setVehicleClasses(list: VehicleClass[]): void {
  classes.value = list
  loaded.value = true
}

export function useVehicleClasses() {
  const { t } = useI18n()
  const fmt = useFormat()
  const auth = useAuthStore()
  void loadVehicleClasses(auth.can('config.manage'))

  const byCode = (code: string | null | undefined): KnownVehicleClass | undefined => classes.value.find((c) => c.code === code)

  /** Localized class name; the API's own name, then the cached class, then the code. */
  function name(code: string | null | undefined, apiName?: LocalizedText | null): string {
    if (apiName) return fmt.name(apiName)
    if (!code) return '—'
    const c = byCode(code)
    return c ? fmt.name(c.name) : code
  }

  const options = computed(() => classes.value.map((c) => ({ value: c.code, label: c.is_active === false ? `${fmt.name(c.name)} · ${t('classes.inactive')}` : fmt.name(c.name) })))

  return { classes, loaded, byCode, name, options }
}
