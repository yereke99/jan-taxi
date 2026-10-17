// Shared with the other Mini App — keep both copies identical (make verify-shared).
import { describe, expect, it } from 'vitest'
import { declutterOffsets, type ScreenPoint } from './declutter'

const opts = { minDistance: 30, width: 390, height: 844, edge: 16 }

function placed(points: ScreenPoint[], offsets: Array<[number, number]>): ScreenPoint[] {
  return points.map((p, i) => ({ x: p.x + offsets[i]![0], y: p.y + offsets[i]![1] }))
}

function minGap(points: ScreenPoint[]): number {
  let min = Infinity
  for (let i = 0; i < points.length; i++) {
    for (let j = i + 1; j < points.length; j++) min = Math.min(min, Math.hypot(points[i]!.x - points[j]!.x, points[i]!.y - points[j]!.y))
  }
  return min
}

describe('declutterOffsets', () => {
  it('keeps separated markers on their points', () => {
    const pts = [
      { x: 100, y: 100 },
      { x: 200, y: 300 },
    ]
    expect(declutterOffsets(pts, opts)).toEqual([
      [0, 0],
      [0, 0],
    ])
  })

  it('fans out markers on the same spot in stop order', () => {
    const pts = [
      { x: 200, y: 400 },
      { x: 201, y: 401 },
      { x: 199, y: 399 },
    ]
    const out = placed(pts, declutterOffsets(pts, opts))
    expect(minGap(out)).toBeGreaterThanOrEqual(29.999)
    expect(out[0]!.x).toBeLessThan(out[1]!.x)
    expect(out[1]!.x).toBeLessThan(out[2]!.x)
  })

  it('merges a fanned row with a neighbour it would cover', () => {
    const pts = [
      { x: 200, y: 400 },
      { x: 200, y: 400 },
      { x: 200, y: 400 },
      { x: 236, y: 400 },
    ]
    expect(minGap(placed(pts, declutterOffsets(pts, opts)))).toBeGreaterThanOrEqual(29.999)
  })

  it('keeps a fanned row inside the viewport', () => {
    const pts = Array.from({ length: 5 }, () => ({ x: 380, y: 10 }))
    const out = placed(pts, declutterOffsets(pts, opts))
    for (const p of out) {
      expect(p.x).toBeGreaterThanOrEqual(16)
      expect(p.x).toBeLessThanOrEqual(390 - 16)
      expect(p.y).toBeGreaterThanOrEqual(16)
    }
    expect(minGap(out)).toBeGreaterThanOrEqual(29.999)
  })
})
