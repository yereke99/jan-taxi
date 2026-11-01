import { afterEach, describe, expect, it } from 'vitest'
import type { CommissionArea, CommissionAreaCheck, RadiusSuggestion } from '../../api/config'
import { bpsToPercentInput } from '../../forms/numbers'
import { i18n } from '../../i18n'
import type { Position } from '../../maps/polygon'
import { formatDistance, type Locale } from '../../utils/format'
import {
  RADIUS_PRESETS_KM,
  areaForm,
  boundaryCenter,
  checkBody,
  checkLines,
  dragRadius,
  drawnRadius,
  metersToRadiusInput,
  percentInputToBps,
  radiusInputToMeters,
  resolveText,
  searchQuery,
  shapeText,
  suggestionText,
  validateAreaForm,
  type AreaForm,
  type Words,
} from './commissionAreaModel'

const NBSP = '\u00a0'
const A: Position = [58.1, 48.8]
const B: Position = [58.2, 48.8]
const C: Position = [58.2, 48.9]

function words(locale: Locale): Words {
  i18n.global.locale.value = locale
  return {
    t: (key, named) => (named ? i18n.global.t(key, named) : i18n.global.t(key)),
    te: (key) => i18n.global.te(key, 'kk'),
    distance: (m) => formatDistance(m, locale),
    percent: (bps) => `${bps / 100}%`,
  }
}

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

function form(extra: Partial<AreaForm> = {}): AreaForm {
  return { ...areaForm(area()), ...extra }
}

afterEach(() => {
  i18n.global.locale.value = 'kk'
})

describe('commission percent ↔ basis points', () => {
  it('reads percents with up to two decimals', () => {
    expect(percentInputToBps('7')).toBe(700)
    expect(percentInputToBps(' 7,5 ')).toBe(750)
    expect(percentInputToBps('12.25')).toBe(1225)
    expect(percentInputToBps('0.29')).toBe(29)
    expect(percentInputToBps('10 %')).toBe(1000)
    expect(percentInputToBps('0')).toBe(0)
  })

  it('refuses empty, negative, malformed and finer values', () => {
    for (const bad of ['', ' ', '-1', '7.255', '7.', '.5', 'abc', '1e2', '7,5,1']) expect(percentInputToBps(bad)).toBeNull()
  })

  it('round-trips every allowed value', () => {
    for (let bps = 0; bps <= 5000; bps++) expect(percentInputToBps(bpsToPercentInput(bps))).toBe(bps)
    expect(bpsToPercentInput(700)).toBe('7')
    expect(bpsToPercentInput(725)).toBe('7.25')
  })
})

describe('circle radius', () => {
  it('converts km text to whole metres and back', () => {
    expect(radiusInputToMeters('2,8')).toBe(2800)
    expect(radiusInputToMeters('0.2')).toBe(200)
    expect(radiusInputToMeters('2.8456')).toBe(2846)
    expect(radiusInputToMeters('')).toBeNull()
    expect(radiusInputToMeters('-1')).toBeNull()
    expect(metersToRadiusInput(2800)).toBe('2.8')
    expect(metersToRadiusInput(200)).toBe('0.2')
    expect(metersToRadiusInput(50_000)).toBe('50')
    expect(radiusInputToMeters(metersToRadiusInput(2846))).toBe(2846)
  })

  it('keeps the dragged and the drawn radius within 200 m – 50 km', () => {
    expect(dragRadius(2843.7)).toBe(2840)
    expect(dragRadius(50)).toBe(200)
    expect(dragRadius(80_000)).toBe(50_000)
    expect(drawnRadius('0')).toBe(200)
    expect(drawnRadius('70')).toBe(50_000)
    expect(drawnRadius('3')).toBe(3000)
    expect(drawnRadius('x')).toBeNull()
  })
})

describe('area form', () => {
  it('opens a saved circle and an empty new village', () => {
    expect(areaForm(area())).toEqual({
      name: 'Шұбарқұдық',
      percent: '7',
      shape: 'circle',
      center: { lat: 49.1436, lon: 56.4867 },
      radiusKm: '2.8',
      polygons: [],
      active: true,
    })
    expect(areaForm(null)).toMatchObject({ name: '', percent: '', shape: 'circle', center: null, radiusKm: '', polygons: [], active: true })
    const drawn = areaForm(area({ shape: 'polygon', radius_m: null, boundary: { type: 'MultiPolygon', coordinates: [[[A, B, C, A]]] } }))
    expect(drawn.polygons).toEqual([[[A, B, C]]])
    expect(drawn.radiusKm).toBe('')
  })

  it('builds a circle body with the radius and no boundary', () => {
    const r = validateAreaForm(form({ name: '  Шұбарқұдық   ауылы ', percent: '7,5', radiusKm: '3' }))
    expect(r.errors).toEqual({})
    expect(r.body).toEqual({
      name: 'Шұбарқұдық ауылы',
      commission_bps: 750,
      shape: 'circle',
      center: { lat: 49.1436, lon: 56.4867 },
      radius_m: 3000,
      is_active: true,
    })
  })

  it('builds a polygon body with the boundary and no radius', () => {
    const r = validateAreaForm(form({ shape: 'polygon', polygons: [[[A, B, C]]], radiusKm: '2.8', active: false }))
    expect(r.errors).toEqual({})
    expect(r.body).toEqual({
      name: 'Шұбарқұдық',
      commission_bps: 700,
      shape: 'polygon',
      center: { lat: 49.1436, lon: 56.4867 },
      boundary: { type: 'MultiPolygon', coordinates: [[[A, B, C, A]]] },
      is_active: false,
    })
  })

  it('reports the API field names and reasons', () => {
    expect(validateAreaForm(form({ name: ' ', percent: '', center: null, radiusKm: '' })).errors).toEqual({
      name: 'required',
      commission_bps: 'required',
      center: 'required',
      radius_m: 'required',
    })
    expect(validateAreaForm(form({ name: 'А'.repeat(121), percent: '50.01', radiusKm: '0.1', center: { lat: 55.75, lon: 37.62 } })).errors).toEqual({
      name: 'too_long',
      commission_bps: 'out_of_range',
      center: 'outside_service_area',
      radius_m: 'out_of_range',
    })
    expect(validateAreaForm(form({ percent: '7.555', radiusKm: 'km' })).errors).toEqual({ commission_bps: 'invalid', radius_m: 'invalid' })
    expect(validateAreaForm(form({ shape: 'polygon', polygons: [[[A, B]]] })).errors).toEqual({ boundary: 'required' })
    expect(validateAreaForm(form({ percent: '50', radiusKm: '50' })).body).toMatchObject({ commission_bps: 5000, radius_m: 50_000 })
    expect(validateAreaForm(form({ percent: '0', radiusKm: '0.2' })).body).toMatchObject({ commission_bps: 0, radius_m: 200 })
  })

  it('takes the middle of a boundary as the village point', () => {
    expect(boundaryCenter([[[A, B, C]]])).toEqual({ lat: 48.85, lon: 58.15 })
    expect(boundaryCenter([])).toBeNull()
  })

  it('searches from two characters, at most 120', () => {
    expect(searchQuery(' Ш ')).toBeNull()
    expect(searchQuery(' Шұ ')).toBe('Шұ')
    expect(searchQuery('ә'.repeat(130))).toHaveLength(120)
  })
})

describe('texts', () => {
  const suggestion: RadiusSuggestion = {
    radius_m: 2800,
    basis: 'osm_extent',
    settlement: { name: 'Шұбарқұдық', kind: 'village' },
    neighbor: { name: 'Жаңажол', distance_m: 7100, source: 'osm' },
  }

  it('explains a suggested radius in one line', () => {
    expect(suggestionText(suggestion, words('ru'))).toBe(`Предложено 2,8${NBSP}км: размер села по OpenStreetMap; соседнее село Жаңажол в 7,1${NBSP}км`)
    expect(suggestionText(suggestion, words('en'))).toBe(`Suggested 2.8${NBSP}km: the village size in OpenStreetMap; neighbouring village Жаңажол is 7.1${NBSP}km away`)
    expect(suggestionText({ ...suggestion, neighbor: null }, words('kk'))).toBe(`2,8${NBSP}км ұсынылды: ауылдың OpenStreetMap-тағы өлшемі`)
  })

  it('names the settlement type or falls back to the default radius', () => {
    const w = words('ru')
    expect(suggestionText({ ...suggestion, basis: 'osm_type', neighbor: null }, w)).toBe(`Предложено 2,8${NBSP}км: тип поселения в OpenStreetMap: село`)
    expect(suggestionText({ ...suggestion, basis: 'osm_type', settlement: { name: 'X', kind: 'allotments' }, neighbor: null }, w)).toBe(
      `Предложено 2,8${NBSP}км: тип поселения в OpenStreetMap`,
    )
    expect(suggestionText({ radius_m: 2000, basis: 'default', settlement: null, neighbor: null }, w)).toBe(
      `Предложено 2,0${NBSP}км: стандартный радиус, в OpenStreetMap нет размера села`,
    )
  })

  it('describes the area and a checked point', () => {
    const w = words('ru')
    expect(shapeText(area(), w)).toBe(`круг 2,8${NBSP}км`)
    expect(shapeText(area({ shape: 'polygon', radius_m: null }), w)).toBe('граница')
    expect(resolveText({ area: { id: 'a1', name: 'Шұбарқұдық', commission_bps: 700 }, distance_m: 1200 }, w)).toBe('Здесь: Шұбарқұдық — 7%')
    expect(resolveText({ area: null, distance_m: null }, w)).toBe('Вне сёл — общая комиссия из Настроек')
  })
})

describe('database check of a village drawn by hand', () => {
  const empty = form({ name: '', center: null, radiusKm: '', polygons: [] })

  it('sends whatever is filled in, never the village being edited, nothing while the form is empty', () => {
    expect(checkBody(empty, null)).toBeNull()
    expect(checkBody({ ...empty, name: '  Жаңа   ауыл ' }, null)).toEqual({ name: 'Жаңа ауыл' })
    expect(checkBody({ ...empty, center: { lat: 48.8, lon: 58.1 } }, null)).toEqual({ center: { lat: 48.8, lon: 58.1 } })
    // A circle is checked once it has a point and a radius within the limits.
    expect(checkBody({ ...empty, center: { lat: 48.8, lon: 58.1 }, radiusKm: '5' }, 'a1')).toEqual({
      center: { lat: 48.8, lon: 58.1 },
      shape: 'circle',
      radius_m: 5000,
      exclude_id: 'a1',
    })
    expect(checkBody({ ...empty, center: { lat: 48.8, lon: 58.1 }, radiusKm: '0.1' }, null)).toEqual({ center: { lat: 48.8, lon: 58.1 } })
    const drawn = checkBody({ ...empty, shape: 'polygon', polygons: [[[A, B, C]]] }, null)
    expect(drawn?.shape).toBe('polygon')
    expect(drawn?.boundary?.type).toBe('MultiPolygon')
  })

  it('offers the ready radii the owner asked for', () => {
    expect([...RADIUS_PRESETS_KM]).toEqual([1, 3, 5, 10, 15])
  })

  it('turns the answer into lines: a taken name, the point inside a village, overlaps, else new', () => {
    const w = words('ru')
    const item = (name: string, extra: Partial<CommissionAreaCheck['covering'][number]> = {}) => ({
      id: name, name, commission_bps: 700, is_active: true, distance_m: 1200, ...extra,
    })
    const busy: CommissionAreaCheck = {
      name_taken: item('Ақкөл', { is_active: false, distance_m: null }),
      covering: [item('Ақкөл')],
      overlapping: [item('Жаңажол'), item('Сағыз', { is_active: false })],
      nearest: item('Ақкөл'),
    }
    expect(checkLines(busy, { name: 'Ақкөл', shape: 'circle', center: { lat: 48.8, lon: 58.1 }, radius_m: 3000 }, w)).toEqual([
      { tone: 'error', text: 'Село «Ақкөл» уже есть в базе (выключено)', open: 'Ақкөл' },
      { tone: 'warning', text: `Точка уже внутри села «Ақкөл»: 7%, 1,2${NBSP}км от центра`, open: 'Ақкөл' },
      { tone: 'info', text: 'Пересекается с: Жаңажол, Сағыз (выключено). В общей части действует комиссия села с ближайшим центром.' },
    ])

    const free: CommissionAreaCheck = { name_taken: null, covering: [], overlapping: [], nearest: item('Жаңажол', { distance_m: 8400 }) }
    expect(checkLines(free, { name: 'Жаңа ауыл', shape: 'circle', center: { lat: 48.8, lon: 58.1 }, radius_m: 3000 }, w)).toEqual([
      { tone: 'ok', text: 'Такого села в базе нет, с другими сёлами не пересекается.' },
      { tone: 'info', text: `Ближайшее село: Жаңажол, 8,4${NBSP}км` },
    ])
    expect(checkLines({ ...free, nearest: null }, { name: 'Жаңа ауыл' }, w)).toEqual([{ tone: 'ok', text: 'Села с таким названием в базе нет.' }])
    expect(checkLines({ ...free, nearest: null }, { center: { lat: 48.8, lon: 58.1 } }, w)[0]?.text).toBe('Эта точка не входит ни в одно село.')
  })
})

