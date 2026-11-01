// Village commissions (/admin/commission-areas): percent ↔ basis points, the
// circle radius in km, the editor form with the API's field checks and the
// one-line texts of a suggested radius and a checked point. Pure functions;
// CommissionAreasPage and CommissionAreaMap render them.

import type { LatLon } from '../../api/common'
import type {
  CommissionArea,
  CommissionAreaCheck,
  CommissionAreaCheckInput,
  CommissionAreaInput,
  CommissionAreaResolve,
  CommissionAreaShape,
  MultiPolygonGeometry,
  RadiusSuggestion,
} from '../../api/config'
import { bpsToPercentInput, parseDecimal } from '../../forms/numbers'
import type { FieldErrors } from '../../forms/validation'
import { inKazakhstanBBox } from '../../maps/constants'
import { fromGeometry, polygonsBounds, toGeometry, type PolygonRings } from '../../maps/polygon'
import { codePoints } from '../../utils/text'

/** 50 %, the API's upper bound for commission_bps. */
export const MAX_COMMISSION_BPS = 5000
export const RADIUS_MIN_M = 200
export const RADIUS_MAX_M = 50_000
/** Circle radius when no suggestion could be loaded. */
export const DEFAULT_RADIUS_M = 2000
/** Ready circle radii for a village drawn by hand, km. */
export const RADIUS_PRESETS_KM = [1, 3, 5, 10, 15] as const
export const NAME_MAX = 120
export const SEARCH_MIN = 2
export const SEARCH_MAX = 120

/** Percent as typed ("7", "7,5", "12.25 %") → basis points; null when empty, malformed or finer than hundredths. */
export function percentInputToBps(text: string): number | null {
  const m = /^(\d{1,3})(?:[.,](\d{1,2}))?\s*%?$/.exec(text.trim())
  if (!m) return null
  return Number(m[1]) * 100 + Number((m[2] ?? '').padEnd(2, '0'))
}

/** Radius typed in km ("2,8", "0.2") → whole metres; null when empty or not a number. */
export function radiusInputToMeters(text: string): number | null {
  const km = parseDecimal(text)
  return km === null || km < 0 ? null : Math.round(km * 1000)
}

/** Metres → the km input text (2800 → "2.8", 200 → "0.2"). */
export function metersToRadiusInput(m: number): string {
  return String(Math.round(m) / 1000)
}

function clampRadius(m: number): number {
  return Math.min(RADIUS_MAX_M, Math.max(RADIUS_MIN_M, m))
}

/** Radius set with the handle on the circle edge: whole 10 m within 200 m – 50 km. */
export function dragRadius(m: number): number {
  return clampRadius(Math.round(m / 10) * 10)
}

/** Radius to draw for the typed text, kept within the allowed range; null when the text is not a number. */
export function drawnRadius(text: string): number | null {
  const m = radiusInputToMeters(text)
  return m === null ? null : clampRadius(m)
}

/** Editor state of one village. */
export interface AreaForm {
  name: string
  /** Commission in percent, as typed. */
  percent: string
  shape: CommissionAreaShape
  /** The village point (search result, map click or the dragged pin). */
  center: LatLon | null
  /** Circle radius in km, as typed. */
  radiusKm: string
  /** Boundary being drawn or edited (open rings, see maps/polygon). */
  polygons: PolygonRings[]
  active: boolean
}

/** Form of an existing village, or an empty one for a new village. */
export function areaForm(a: CommissionArea | null): AreaForm {
  return {
    name: a?.name ?? '',
    percent: a ? bpsToPercentInput(a.commission_bps) : '',
    shape: a?.shape ?? 'circle',
    center: a ? { lat: a.center.lat, lon: a.center.lon } : null,
    radiusKm: a && a.radius_m !== null ? metersToRadiusInput(a.radius_m) : '',
    polygons: fromGeometry(a?.boundary ?? null),
    active: a?.is_active ?? true,
  }
}

/** Names are cleaned like the API does (§11): single spaces, trimmed. */
function cleanName(s: string): string {
  return s.replace(/\s+/g, ' ').trim()
}

/**
 * Same rules as POST/PATCH /admin/commission-areas, with the API's field
 * names and reason codes. The body carries the radius of a circle or the
 * boundary of a polygon, never both.
 */
export function validateAreaForm(f: AreaForm): { errors: FieldErrors; body: CommissionAreaInput | null } {
  const errors: FieldErrors = {}
  const name = cleanName(f.name)
  if (!name) errors.name = 'required'
  else if (codePoints(name) > NAME_MAX) errors.name = 'too_long'

  const bps = percentInputToBps(f.percent)
  if (!f.percent.trim()) errors.commission_bps = 'required'
  else if (bps === null) errors.commission_bps = 'invalid'
  else if (bps > MAX_COMMISSION_BPS) errors.commission_bps = 'out_of_range'

  const c = f.center
  if (!c) errors.center = 'required'
  else if (!inKazakhstanBBox(c)) errors.center = 'outside_service_area'

  let radius: number | null = null
  let boundary: MultiPolygonGeometry | null = null
  if (f.shape === 'circle') {
    radius = radiusInputToMeters(f.radiusKm)
    if (!f.radiusKm.trim()) errors.radius_m = 'required'
    else if (radius === null) errors.radius_m = 'invalid'
    else if (radius < RADIUS_MIN_M || radius > RADIUS_MAX_M) errors.radius_m = 'out_of_range'
  } else {
    boundary = toGeometry(f.polygons)
    if (!boundary) errors.boundary = 'required'
  }

  if (Object.keys(errors).length > 0 || !c || bps === null) return { errors, body: null }
  const body: CommissionAreaInput = { name, commission_bps: bps, shape: f.shape, center: { lat: c.lat, lon: c.lon }, is_active: f.active }
  if (radius !== null) body.radius_m = radius
  if (boundary) body.boundary = boundary
  return { errors, body }
}

/** Middle of the boundary's bounding box: the village point when none was chosen before drawing. */
export function boundaryCenter(polygons: readonly PolygonRings[]): LatLon | null {
  const b = polygonsBounds(polygons)
  if (!b) return null
  return { lat: Number(((b[0][1] + b[1][1]) / 2).toFixed(6)), lon: Number(((b[0][0] + b[1][0]) / 2).toFixed(6)) }
}

/** Text to search for: trimmed and at most 120 characters; null while shorter than two. */
export function searchQuery(text: string): string | null {
  const chars = [...text.trim().normalize('NFC')]
  return chars.length < SEARCH_MIN ? null : chars.slice(0, SEARCH_MAX).join('')
}

/** Translation and formatting for the texts below (vue-i18n and useFormat on the page). */
export interface Words {
  t: (key: string, named?: Record<string, unknown>) => string
  te: (key: string) => boolean
  distance: (m: number) => string
  percent: (bps: number) => string
}

/** «Предложено 2,8 км: размер села по OpenStreetMap; соседнее село Жаңажол в 7,1 км». */
export function suggestionText(s: RadiusSuggestion, w: Words): string {
  const kind = s.settlement?.kind ?? ''
  const kindKey = /^[a-z_]+$/.test(kind) ? `commission_areas.kind_${kind}` : ''
  let reason: string
  if (s.basis === 'osm_extent') reason = w.t('commission_areas.basis_osm_extent')
  else if (s.basis === 'osm_type') reason = kindKey && w.te(kindKey) ? w.t('commission_areas.basis_osm_type', { kind: w.t(kindKey) }) : w.t('commission_areas.basis_osm_type_unknown')
  else reason = w.t('commission_areas.basis_default')
  const reasons = [reason]
  if (s.neighbor) reasons.push(w.t('commission_areas.basis_neighbor', { name: s.neighbor.name, distance: w.distance(s.neighbor.distance_m) }))
  return w.t('commission_areas.suggested', { radius: w.distance(s.radius_m), reason: reasons.join('; ') })
}

/** List line of a village's area: «круг 2,8 км» or «граница». */
export function shapeText(a: Pick<CommissionArea, 'shape' | 'radius_m'>, w: Words): string {
  return a.shape === 'circle' && a.radius_m !== null ? w.t('commission_areas.shape_circle', { radius: w.distance(a.radius_m) }) : w.t('commission_areas.shape_boundary')
}

/** «Здесь: Шұбарқұдық — 7%» or «Вне сёл — общая комиссия из Настроек». */
export function resolveText(r: CommissionAreaResolve, w: Words): string {
  return r.area ? w.t('commission_areas.check_inside', { name: r.area.name, percent: w.percent(r.area.commission_bps) }) : w.t('commission_areas.check_outside')
}

/**
 * What the database check gets from the form: whatever is filled in (a name,
 * the point, the whole circle or boundary), null while nothing is. The
 * village being edited is left out of the comparison.
 */
export function checkBody(f: AreaForm, excludeId: string | null): CommissionAreaCheckInput | null {
  const body: CommissionAreaCheckInput = {}
  const name = cleanName(f.name)
  if (name && codePoints(name) <= NAME_MAX) body.name = name
  const c = f.center
  if (c) body.center = { lat: c.lat, lon: c.lon }
  if (f.shape === 'circle') {
    const r = radiusInputToMeters(f.radiusKm)
    if (c && r !== null && r >= RADIUS_MIN_M && r <= RADIUS_MAX_M) {
      body.shape = 'circle'
      body.radius_m = r
    }
  } else {
    const b = toGeometry(f.polygons)
    if (b) {
      body.shape = 'polygon'
      body.boundary = b
    }
  }
  if (body.name === undefined && body.center === undefined && body.shape === undefined) return null
  if (excludeId) body.exclude_id = excludeId
  return body
}

export type CheckTone = 'error' | 'warning' | 'info' | 'ok'

/** One line of the database check; `open` is a saved village to open from it. */
export interface CheckLine {
  tone: CheckTone
  text: string
  open?: string
}

/**
 * The database check as lines: a taken name (a save would be refused), the
 * villages already holding the point (likely the same village), the villages
 * the area overlaps (allowed: the nearest centre decides), else that the
 * village is new, and the nearest village for orientation.
 */
export function checkLines(r: CommissionAreaCheck, sent: CommissionAreaCheckInput, w: Words): CheckLine[] {
  const off = (a: { is_active: boolean }): string => (a.is_active ? '' : w.t('commission_areas.db_inactive'))
  const lines: CheckLine[] = []
  if (r.name_taken) lines.push({ tone: 'error', text: w.t('commission_areas.db_name_taken', { name: r.name_taken.name }) + off(r.name_taken), open: r.name_taken.id })
  for (const a of r.covering) {
    lines.push({
      tone: 'warning',
      text: w.t('commission_areas.db_covering', { name: a.name, percent: w.percent(a.commission_bps), distance: w.distance(a.distance_m ?? 0) }) + off(a),
      open: a.id,
    })
  }
  if (r.overlapping.length > 0) lines.push({ tone: 'info', text: w.t('commission_areas.db_overlapping', { names: r.overlapping.map((a) => a.name + off(a)).join(', ') }) })
  if (lines.length === 0) lines.push({ tone: 'ok', text: w.t(sent.shape ? 'commission_areas.db_clear' : sent.center ? 'commission_areas.db_clear_point' : 'commission_areas.db_clear_name') })
  if (r.nearest && r.covering.length === 0) lines.push({ tone: 'info', text: w.t('commission_areas.db_nearest', { name: r.nearest.name, distance: w.distance(r.nearest.distance_m ?? 0) }) })
  return lines
}

