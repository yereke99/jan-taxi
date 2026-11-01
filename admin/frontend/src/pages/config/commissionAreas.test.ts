// Villages and commission: the list, the search → suggested radius → save
// flow, the boundary mode, the API's field errors and «Check a point».

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { commissionAreasApi, type CommissionArea } from '../../api/config'
import { activeConfirm } from '../../components/confirm'
import { i18n } from '../../i18n'
import { ApiError } from '../../services/api'
import CommissionAreasPage from './CommissionAreasPage.vue'

vi.mock('./CommissionAreaMap.vue', () => ({
  default: {
    name: 'CommissionAreaMap',
    props: ['others', 'shape', 'center', 'radius', 'polygons', 'draft', 'checking', 'checkPoint'],
    emits: ['update:center', 'update:radius', 'update:polygons', 'update:draft', 'closed', 'pick'],
    setup: () => ({ fit: () => undefined }),
    template: '<div class="map-stub" />',
  },
}))

const NBSP = '\u00a0'

function area(extra: Partial<CommissionArea> = {}): CommissionArea {
  return {
    id: 'a1',
    name: 'Шұбарқұдық',
    commission_bps: 700,
    shape: 'circle',
    center: { lat: 49.1436, lon: 56.4867 },
    radius_m: 2800,
    boundary: null,
    is_active: true,
    created_at: '2026-11-01T08:00:00Z',
    updated_at: '2026-11-01T08:00:00Z',
    ...extra,
  }
}

const boundary = { type: 'MultiPolygon' as const, coordinates: [[[[58.1, 48.8], [58.2, 48.8], [58.2, 48.9], [58.1, 48.8]] as [number, number][]]] }

let mounted: VueWrapper | null = null

async function mountPage(items: CommissionArea[]): Promise<VueWrapper> {
  vi.spyOn(commissionAreasApi, 'list').mockResolvedValue({ items })
  mounted = mount(CommissionAreasPage, { global: { plugins: [i18n] }, attachTo: document.body })
  await flushPromises()
  return mounted
}

function map(w: VueWrapper) {
  return w.findComponent({ name: 'CommissionAreaMap' })
}

async function click(w: VueWrapper, text: string): Promise<void> {
  const button = w.findAll('button').find((b) => b.text() === text)
  if (!button) throw new Error(`no button «${text}»`)
  await button.trigger('click')
  await flushPromises()
}

beforeEach(() => {
  i18n.global.locale.value = 'ru'
})

afterEach(() => {
  mounted?.unmount()
  mounted = null
  i18n.global.locale.value = 'kk'
  activeConfirm.value?.resolve(false)
  document.body.innerHTML = ''
})

describe('CommissionAreasPage', () => {
  it('explains the rule and lists villages with their commission and area', async () => {
    const w = await mountPage([area(), area({ id: 'a2', name: 'Жаңажол', commission_bps: 1000, shape: 'polygon', radius_m: null, boundary, is_active: false })])
    expect(w.text()).toContain('Село определяется по точке A заказа')
    expect(w.text()).toContain('водители комиссию не видят')
    const rows = w.findAll('.area-item')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('Шұбарқұдық')
    expect(rows[0]!.text()).toContain('7%')
    expect(rows[0]!.text()).toContain(`круг 2,8${NBSP}км`)
    expect(rows[1]!.text()).toContain('10%')
    expect(rows[1]!.text()).toContain('граница')
    expect((rows[1]!.find('input[role="switch"]').element as HTMLInputElement).checked).toBe(false)
    expect(map(w).props('others')).toHaveLength(2)
    expect(map(w).props('shape')).toBeNull()
  })

  it('says the general commission applies while there are no villages', async () => {
    const w = await mountPage([])
    expect(w.text()).toContain('Сёл пока нет: для всех заказов действует общая комиссия.')
  })

  it('switches a village off and deletes one after confirmation', async () => {
    const w = await mountPage([area()])
    const update = vi.spyOn(commissionAreasApi, 'update').mockResolvedValue(area({ is_active: false }))
    await w.find('input[role="switch"]').setValue(false)
    await flushPromises()
    expect(update).toHaveBeenCalledWith('a1', { is_active: false })

    const remove = vi.spyOn(commissionAreasApi, 'remove').mockResolvedValue(undefined)
    await click(w, 'Удалить')
    expect(activeConfirm.value?.title).toBe('Удалить село «Шұбарқұдық»')
    expect(activeConfirm.value?.message).toContain('Комиссия уже созданных заказов не изменится.')
    activeConfirm.value!.resolve(true)
    await flushPromises()
    expect(remove).toHaveBeenCalledWith('a1')
  })

  it('finds a village, takes the suggested radius and saves the circle', async () => {
    const w = await mountPage([])
    const search = vi.spyOn(commissionAreasApi, 'search').mockResolvedValue({ items: [{ title: 'Шұбарқұдық', subtitle: 'Темір ауданы, Ақтөбе облысы', lat: 49.1436, lon: 56.4867 }] })
    const suggest = vi.spyOn(commissionAreasApi, 'suggest').mockResolvedValue({
      radius_m: 2800,
      basis: 'osm_extent',
      settlement: { name: 'Шұбарқұдық', kind: 'village' },
      neighbor: { name: 'Жаңажол', distance_m: 7100, source: 'area' },
    })
    const create = vi.spyOn(commissionAreasApi, 'create').mockResolvedValue(area())
    await click(w, 'Новое село')

    const input = w.find('input[type="search"]')
    await input.setValue('Ш')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(search).not.toHaveBeenCalled()
    await input.setValue('Шұбар')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(search).toHaveBeenCalledWith('Шұбар', expect.any(AbortSignal))

    expect(w.find('.hit').text()).toContain('Темір ауданы, Ақтөбе облысы')
    await w.find('.hit').trigger('click')
    await flushPromises()
    expect(suggest).toHaveBeenCalledWith({ lat: 49.1436, lon: 56.4867 }, null, expect.any(AbortSignal))
    expect(w.text()).toContain(`Предложено 2,8${NBSP}км: размер села по OpenStreetMap; соседнее село Жаңажол в 7,1${NBSP}км`)
    expect(map(w).props('center')).toEqual({ lat: 49.1436, lon: 56.4867 })
    expect(map(w).props('radius')).toBe(2800)

    // The edge point of the circle sets the radius; the km field follows.
    map(w).vm.$emit('update:radius', 3400)
    await flushPromises()
    const fields = w.findAll('input.input')
    expect((fields[2]!.element as HTMLInputElement).value).toBe('3.4')

    await fields[1]!.setValue('7,5')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(create).toHaveBeenCalledWith({
      name: 'Шұбарқұдық',
      commission_bps: 750,
      shape: 'circle',
      center: { lat: 49.1436, lon: 56.4867 },
      radius_m: 3400,
      is_active: true,
    })
    expect(w.find('form').exists()).toBe(false)
  })

  it('draws a boundary and keeps a village point for overlaps', async () => {
    const w = await mountPage([area()])
    const create = vi.spyOn(commissionAreasApi, 'create').mockResolvedValue(area({ id: 'a2' }))
    await click(w, 'Новое село')
    expect(map(w).props('others')).toHaveLength(1)
    await click(w, 'Граница')
    expect(map(w).props('shape')).toBe('polygon')
    expect(map(w).props('draft')).toEqual([])

    map(w).vm.$emit('closed', [[58.1, 48.8], [58.2, 48.8], [58.2, 48.9]])
    map(w).vm.$emit('update:draft', null)
    await flushPromises()
    expect(map(w).props('center')).toEqual({ lat: 48.85, lon: 58.15 })
    expect(w.text()).toContain('Часть 1: 3 точек')

    await w.findAll('input.input')[0]!.setValue('Жаңажол')
    await w.findAll('input.input')[1]!.setValue('10')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(create).toHaveBeenCalledWith({
      name: 'Жаңажол',
      commission_bps: 1000,
      shape: 'polygon',
      center: { lat: 48.85, lon: 58.15 },
      boundary: { type: 'MultiPolygon', coordinates: [[[[58.1, 48.8], [58.2, 48.8], [58.2, 48.9], [58.1, 48.8]]]] },
      is_active: true,
    })
  })

  it('sets the centre with the first map click and proposes a radius there', async () => {
    const w = await mountPage([area()])
    const suggest = vi.spyOn(commissionAreasApi, 'suggest').mockRejectedValue(new ApiError(503, 'internal', 'down'))
    await click(w, 'Изменить')
    expect(map(w).props('shape')).toBe('circle')
    expect(map(w).props('others')).toHaveLength(0)
    expect(map(w).props('center')).toEqual({ lat: 49.1436, lon: 56.4867 })
    await click(w, 'Закрыть')

    await click(w, 'Новое село')
    map(w).vm.$emit('update:center', { lat: 48.8264, lon: 58.1442 })
    await flushPromises()
    expect(suggest).toHaveBeenCalledWith({ lat: 48.8264, lon: 58.1442 }, null, expect.any(AbortSignal))
    expect(map(w).props('radius')).toBe(2000)
    expect(w.text()).toContain('Не удалось предложить радиус — проверьте его сами.')
    expect((w.findAll('input.input')[2]!.element as HTMLInputElement).value).toBe('2')

    // Dragging the pin later does not ask again.
    map(w).vm.$emit('update:center', { lat: 48.83, lon: 58.15 })
    await flushPromises()
    expect(suggest).toHaveBeenCalledTimes(1)
  })

  it('shows the API field errors next to the fields', async () => {
    const w = await mountPage([area()])
    vi.spyOn(commissionAreasApi, 'update').mockRejectedValue(
      new ApiError(422, 'validation_failed', 'invalid', { name: 'duplicate', radius_m: 'out_of_range', center: 'outside_service_area' }),
    )
    await click(w, 'Изменить')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.text()).toContain('Село с таким названием уже есть.')
    expect(w.text()).toContain('Вне допустимого диапазона.')
    expect(w.text()).toContain('За пределами Казахстана.')
    expect(w.find('form').exists()).toBe(true)
  })

  it('checks which commission a pickup point gets', async () => {
    const w = await mountPage([area()])
    const resolve = vi
      .spyOn(commissionAreasApi, 'resolve')
      .mockResolvedValueOnce({ area: { id: 'a1', name: 'Шұбарқұдық', commission_bps: 700 }, distance_m: 1200 })
      .mockResolvedValueOnce({ area: null, distance_m: null })
    await click(w, 'Проверить точку')
    expect(map(w).props('checking')).toBe(true)
    expect(w.text()).toContain('Нажмите на карту')

    map(w).vm.$emit('pick', { lat: 49.15, lon: 56.49 })
    await flushPromises()
    expect(resolve).toHaveBeenCalledWith({ lat: 49.15, lon: 56.49 }, expect.any(AbortSignal))
    expect(map(w).props('checkPoint')).toEqual({ lat: 49.15, lon: 56.49 })
    expect(w.text()).toContain('Здесь: Шұбарқұдық — 7%')
    expect(w.text()).toContain(`1,2${NBSP}км от центра`)

    map(w).vm.$emit('pick', { lat: 47, lon: 60 })
    await flushPromises()
    expect(w.text()).toContain('Вне сёл — общая комиссия из Настроек')

    await click(w, 'Закончить проверку')
    expect(map(w).props('checking')).toBe(false)
    expect(map(w).props('checkPoint')).toBeNull()
  })
})
