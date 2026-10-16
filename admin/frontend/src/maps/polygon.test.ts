import { describe, expect, it } from 'vitest'
import { canClose, closeRing, draftClick, fromGeometry, parseGeoJSON, ringSelfIntersects, toGeometry, type Position } from './polygon'

const A: Position = [58.1, 48.8]
const B: Position = [58.2, 48.8]
const C: Position = [58.2, 48.9]
const D: Position = [58.1, 48.9]

describe('zone editor drawing', () => {
  it('adds vertices until the first vertex is clicked', () => {
    let v: Position[] = []
    for (const p of [A, B, C]) {
      const r = draftClick(v, p, false)
      expect(r.closed).toBeNull()
      v = r.vertices
    }
    expect(v).toEqual([A, B, C])
  })

  it('closes the polygon when the first vertex is clicked with three or more vertices', () => {
    const r = draftClick([A, B, C, D], A, true)
    expect(r.closed).toEqual([A, B, C, D])
    expect(r.vertices).toEqual([])
    expect(toGeometry([[r.closed!]])).toEqual({ type: 'MultiPolygon', coordinates: [[[A, B, C, D, A]]] })
  })

  it('does not close with fewer than three vertices', () => {
    const r = draftClick([A, B], A, true)
    expect(r.closed).toBeNull()
    expect(r.vertices).toEqual([A, B])
  })

  it('does not close a self-intersecting ring', () => {
    const bowtie: Position[] = [A, C, B, D]
    expect(ringSelfIntersects(bowtie)).toBe(true)
    expect(canClose(bowtie)).toBe(false)
    expect(draftClick(bowtie, A, true).closed).toBeNull()
    expect(canClose([A, B, C, D])).toBe(true)
  })

  it('ignores a repeated click on the last vertex', () => {
    expect(draftClick([A, B], B, false).vertices).toEqual([A, B])
  })

  it('closes rings once and round-trips stored geometry', () => {
    expect(closeRing([A, B, C])).toEqual([A, B, C, A])
    expect(closeRing([A, B, C, A])).toEqual([A, B, C, A])
    const stored = { type: 'MultiPolygon' as const, coordinates: [[[A, B, C, D, A]]] }
    const editable = fromGeometry(stored)
    expect(editable).toEqual([[[A, B, C, D]]])
    expect(toGeometry(editable)).toEqual(stored)
  })
})

describe('parseGeoJSON', () => {
  it('accepts a Feature with a Polygon and a FeatureCollection', () => {
    const feature = JSON.stringify({ type: 'Feature', properties: {}, geometry: { type: 'Polygon', coordinates: [[A, B, C, A]] } })
    expect(parseGeoJSON(feature)).toEqual({ geometry: { type: 'MultiPolygon', coordinates: [[[A, B, C, A]]] } })
    const fc = JSON.stringify({
      type: 'FeatureCollection',
      features: [
        { type: 'Feature', geometry: { type: 'Polygon', coordinates: [[A, B, C, A]] } },
        { type: 'Feature', geometry: { type: 'MultiPolygon', coordinates: [[[A, C, D, A]]] } },
      ],
    })
    const r = parseGeoJSON(fc)
    expect('geometry' in r && r.geometry.coordinates.length).toBe(2)
  })

  it('reports unusable input', () => {
    expect(parseGeoJSON('{not json')).toEqual({ error: 'invalid_json' })
    expect(parseGeoJSON(JSON.stringify({ type: 'Point', coordinates: A }))).toEqual({ error: 'unsupported_type' })
    expect(parseGeoJSON(JSON.stringify({ type: 'Polygon', coordinates: [[A, B]] }))).toEqual({ error: 'empty' })
    expect(parseGeoJSON(JSON.stringify({ type: 'Polygon', coordinates: [[['x', 1]]] }))).toEqual({ error: 'invalid_coordinates' })
  })
})
