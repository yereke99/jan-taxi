// Geometry helpers for the service-zone editor. Rings are edited "open"
// (the first vertex is not repeated at the end) and closed when converted
// to GeoJSON. Coordinates are [lon, lat] as in GeoJSON.

import type { MultiPolygonGeometry, PolygonGeometry, Position } from '../api/config'

export type { Position } from '../api/config'

/** An open ring: at least three distinct vertices once complete. */
export type Ring = Position[]
/** One polygon: outer ring followed by holes. */
export type PolygonRings = Ring[]

export const MIN_RING_VERTICES = 3
/** Screen distance (px) within which a click on the first vertex closes the ring. */
export const CLOSE_TOLERANCE_PX = 12

function samePoint(a: Position, b: Position): boolean {
  return a[0] === b[0] && a[1] === b[1]
}

/** Drops the closing vertex of a GeoJSON ring. */
export function openRing(closed: readonly Position[]): Ring {
  const out = closed.map((p) => [p[0], p[1]] as Position)
  if (out.length > 1 && samePoint(out[0]!, out[out.length - 1]!)) out.pop()
  return out
}

/** Number of distinct vertices of an open ring. */
export function distinctVertices(ring: readonly Position[]): number {
  return new Set(ring.map((p) => `${p[0]},${p[1]}`)).size
}

/** A ring can be closed once it has three distinct vertices and does not cross itself. */
export function canClose(ring: readonly Position[]): boolean {
  return distinctVertices(ring) >= MIN_RING_VERTICES && !ringSelfIntersects(ring)
}

/** GeoJSON ring: the first vertex repeated at the end. */
export function closeRing(ring: readonly Position[]): Position[] {
  if (ring.length === 0) return []
  const out = ring.map((p) => [p[0], p[1]] as Position)
  if (!samePoint(out[0]!, out[out.length - 1]!)) out.push([out[0]![0], out[0]![1]])
  return out
}

export interface ScreenPoint {
  x: number
  y: number
}

export function screenDistance(a: ScreenPoint, b: ScreenPoint): number {
  return Math.hypot(a.x - b.x, a.y - b.y)
}

export interface DraftClickResult {
  /** Vertices of the ring still being drawn (empty once closed). */
  vertices: Ring
  /** The finished ring when this click closed the polygon. */
  closed: Ring | null
}

/**
 * Applies a map click while drawing: a click near the first vertex closes the
 * ring when it can be closed (and is ignored otherwise); any other click adds
 * a vertex. Clicking the last vertex again (double click) is ignored.
 */
export function draftClick(vertices: readonly Position[], point: Position, nearFirst: boolean): DraftClickResult {
  const current = vertices.map((p) => [p[0], p[1]] as Position)
  if (nearFirst && current.length > 0) {
    // A click on the first vertex never adds a duplicate of it.
    return canClose(current) ? { vertices: [], closed: current } : { vertices: current, closed: null }
  }
  const last = current[current.length - 1]
  if (last && samePoint(last, point)) return { vertices: current, closed: null }
  return { vertices: [...current, [point[0], point[1]]], closed: null }
}

function orientation(a: Position, b: Position, c: Position): number {
  const v = (b[1] - a[1]) * (c[0] - b[0]) - (b[0] - a[0]) * (c[1] - b[1])
  return v === 0 ? 0 : v > 0 ? 1 : 2
}

function onSegment(a: Position, b: Position, c: Position): boolean {
  return Math.min(a[0], c[0]) <= b[0] && b[0] <= Math.max(a[0], c[0]) && Math.min(a[1], c[1]) <= b[1] && b[1] <= Math.max(a[1], c[1])
}

function segmentsIntersect(p1: Position, q1: Position, p2: Position, q2: Position): boolean {
  const o1 = orientation(p1, q1, p2)
  const o2 = orientation(p1, q1, q2)
  const o3 = orientation(p2, q2, p1)
  const o4 = orientation(p2, q2, q1)
  if (o1 !== o2 && o3 !== o4) return true
  if (o1 === 0 && onSegment(p1, p2, q1)) return true
  if (o2 === 0 && onSegment(p1, q2, q1)) return true
  if (o3 === 0 && onSegment(p2, p1, q2)) return true
  if (o4 === 0 && onSegment(p2, q1, q2)) return true
  return false
}

/** True when two non-adjacent edges of the (implicitly closed) ring cross. */
export function ringSelfIntersects(ring: readonly Position[]): boolean {
  const n = ring.length
  if (n < 4) return false
  for (let i = 0; i < n; i++) {
    const a1 = ring[i]!
    const a2 = ring[(i + 1) % n]!
    for (let j = i + 1; j < n; j++) {
      // Adjacent edges share a vertex; skip them (including last↔first).
      if (j === i || (j + 1) % n === i || (i + 1) % n === j) continue
      if (segmentsIntersect(a1, a2, ring[j]!, ring[(j + 1) % n]!)) return true
    }
  }
  return false
}

/** Editable polygons from a stored zone geometry. */
export function fromGeometry(g: PolygonGeometry | MultiPolygonGeometry | null | undefined): PolygonRings[] {
  if (!g) return []
  const polys = g.type === 'Polygon' ? [g.coordinates] : g.coordinates
  return polys.map((rings) => rings.map((r) => openRing(r))).filter((rings) => rings.length > 0 && rings[0]!.length > 0)
}

/** GeoJSON MultiPolygon for the API; null when there is nothing to save. */
export function toGeometry(polygons: readonly PolygonRings[]): MultiPolygonGeometry | null {
  const coords = polygons
    .filter((rings) => rings.length > 0 && distinctVertices(rings[0]!) >= MIN_RING_VERTICES)
    .map((rings) => rings.filter((r) => distinctVertices(r) >= MIN_RING_VERTICES).map((r) => closeRing(r)))
  return coords.length > 0 ? { type: 'MultiPolygon', coordinates: coords } : null
}

export function vertexCount(polygons: readonly PolygonRings[]): number {
  return polygons.reduce((n, rings) => n + rings.reduce((m, r) => m + r.length, 0), 0)
}

export type GeoJSONImportError = 'invalid_json' | 'unsupported_type' | 'empty' | 'invalid_coordinates'

function isPosition(v: unknown): v is Position {
  return Array.isArray(v) && v.length >= 2 && v.slice(0, 2).every((n) => typeof n === 'number' && Number.isFinite(n))
}

function cleanRings(raw: unknown): Position[][] | null {
  if (!Array.isArray(raw)) return null
  const rings: Position[][] = []
  for (const ring of raw) {
    if (!Array.isArray(ring) || !ring.every(isPosition)) return null
    rings.push((ring as Position[]).map((p) => [p[0], p[1]]))
  }
  return rings
}

/**
 * Reads a pasted or uploaded GeoJSON text: a Polygon or MultiPolygon geometry,
 * a Feature holding one, or a FeatureCollection whose polygon features are
 * merged into one MultiPolygon.
 */
export function parseGeoJSON(text: string): { geometry: MultiPolygonGeometry } | { error: GeoJSONImportError } {
  let doc: unknown
  try {
    doc = JSON.parse(text)
  } catch {
    return { error: 'invalid_json' }
  }
  const geometries: unknown[] = []
  const collect = (node: unknown): void => {
    if (!node || typeof node !== 'object') return
    const o = node as Record<string, unknown>
    if (o.type === 'FeatureCollection' && Array.isArray(o.features)) o.features.forEach(collect)
    else if (o.type === 'Feature') collect(o.geometry)
    else if (o.type === 'GeometryCollection' && Array.isArray(o.geometries)) o.geometries.forEach(collect)
    else geometries.push(o)
  }
  collect(doc)
  const polys: Position[][][] = []
  for (const g of geometries) {
    const o = g as Record<string, unknown>
    if (o.type === 'Polygon') {
      const rings = cleanRings(o.coordinates)
      if (!rings) return { error: 'invalid_coordinates' }
      polys.push(rings)
    } else if (o.type === 'MultiPolygon') {
      if (!Array.isArray(o.coordinates)) return { error: 'invalid_coordinates' }
      for (const p of o.coordinates) {
        const rings = cleanRings(p)
        if (!rings) return { error: 'invalid_coordinates' }
        polys.push(rings)
      }
    } else {
      return { error: 'unsupported_type' }
    }
  }
  const geometry = toGeometry(polys.map((rings) => rings.map((r) => openRing(r))))
  return geometry ? { geometry } : { error: 'empty' }
}

/** Pretty GeoJSON Feature for export. */
export function toFeatureJSON(geometry: MultiPolygonGeometry | PolygonGeometry, properties: Record<string, unknown>): string {
  return JSON.stringify({ type: 'Feature', properties, geometry }, null, 2)
}

/** [[west, south], [east, north]] around every vertex, or null. */
export function polygonsBounds(polygons: readonly PolygonRings[]): [[number, number], [number, number]] | null {
  let w = Infinity
  let s = Infinity
  let e = -Infinity
  let n = -Infinity
  for (const rings of polygons) {
    for (const p of rings[0] ?? []) {
      w = Math.min(w, p[0])
      e = Math.max(e, p[0])
      s = Math.min(s, p[1])
      n = Math.max(n, p[1])
    }
  }
  return Number.isFinite(w) ? [[w, s], [e, n]] : null
}

/** Approximate circle polygon (for accuracy radii on maps). */
export function circleRing(center: { lat: number; lon: number }, radiusM: number, steps = 64): Position[] {
  const out: Position[] = []
  const latR = (center.lat * Math.PI) / 180
  const dLat = radiusM / 111_320
  const dLon = radiusM / (111_320 * Math.max(0.01, Math.cos(latR)))
  for (let i = 0; i <= steps; i++) {
    const a = (i / steps) * 2 * Math.PI
    out.push([center.lon + dLon * Math.cos(a), center.lat + dLat * Math.sin(a)])
  }
  return out
}
