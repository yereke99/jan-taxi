// The nationwide default region (KAZ): marked on the cities screen, its
// "active" switch is locked and a 409 default_region_locked is explained.

import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { DEFAULT_REGION_CODE, isDefaultRegion } from '../../api/common'
import { citiesApi, type City } from '../../api/config'
import { i18n } from '../../i18n'
import { ApiError } from '../../services/api'
import CityForm from './CityForm.vue'

vi.mock('maplibre-gl', () => ({
  Marker: class {
    setLngLat() {
      return this
    }
    addTo() {
      return this
    }
    remove() {}
  },
}))
vi.mock('../../maps/MapView.vue', () => ({ default: { name: 'MapView', template: '<div class="map-stub" />' } }))

const toastError = vi.fn()
vi.mock('../../components/toast', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../components/toast')>()
  return { ...real, toast: { ...real.toast, error: (m: string) => toastError(m), success: vi.fn() } }
})

function city(code: string, extra: Partial<City> = {}): City {
  return {
    id: `id-${code}`,
    code,
    name: code === 'KAZ' ? { kk: 'Қазақстан', ru: 'Казахстан', en: 'Kazakhstan' } : { kk: 'Ембі', ru: 'Эмба', en: 'Embi' },
    center: { lat: 48.8264, lon: 58.1442 },
    default_zoom: 12,
    timezone: 'Asia/Aqtobe',
    is_active: true,
    search_timeout_s: 900,
    created_at: '2026-10-19T08:00:00Z',
    updated_at: '2026-10-19T08:00:00Z',
    ...extra,
  }
}

beforeEach(() => {
  i18n.global.locale.value = 'kk'
  toastError.mockReset()
})

afterEach(() => {
  i18n.global.locale.value = 'kk'
})

describe('default region', () => {
  it('is recognised by its code or the API flag', () => {
    expect(DEFAULT_REGION_CODE).toBe('KAZ')
    expect(isDefaultRegion({ code: 'KAZ' })).toBe(true)
    expect(isDefaultRegion({ code: 'kaz' })).toBe(true)
    expect(isDefaultRegion({ code: 'ALA', is_default_region: true })).toBe(true)
    expect(isDefaultRegion({ code: 'EMB' })).toBe(false)
    expect(isDefaultRegion(null)).toBe(false)
  })

  it('locks the active switch and always saves it as active', async () => {
    const update = vi.spyOn(citiesApi, 'update').mockResolvedValue(city('KAZ'))
    const w = mount(CityForm, { props: { city: city('KAZ') }, global: { plugins: [i18n] } })
    const toggle = w.find('input[role="switch"]')
    expect(toggle.attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('Негізгі өңірді өшіруге болмайды')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(update).toHaveBeenCalledWith('id-KAZ', expect.objectContaining({ is_active: true }))
  })

  it('a city keeps a free switch', () => {
    const w = mount(CityForm, { props: { city: city('EMB') }, global: { plugins: [i18n] } })
    expect(w.find('input[role="switch"]').attributes('disabled')).toBeUndefined()
    expect(w.text()).not.toContain('Негізгі өңірді өшіруге болмайды')
  })

  it('explains a 409 default_region_locked', async () => {
    vi.spyOn(citiesApi, 'update').mockRejectedValue(new ApiError(409, 'default_region_locked', 'locked'))
    i18n.global.locale.value = 'ru'
    const w = mount(CityForm, { props: { city: city('KAZ', { is_default_region: true }) }, global: { plugins: [i18n] } })
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(toastError).toHaveBeenCalledWith('Основной регион «Казахстан» нельзя отключить.')
  })
})
