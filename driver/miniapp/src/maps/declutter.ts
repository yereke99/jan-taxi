// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Screen-space layout for stop markers that would cover each other at the
// current zoom (e.g. three stops in one town on a 300 km route): overlapping
// markers are fanned out side by side around their common centre, in stop
// order, and kept inside the viewport. Markers far enough apart stay exactly
// on their points (offset 0).

export interface ScreenPoint {
  x: number
  y: number
}

export interface DeclutterOptions {
  /** Centre-to-centre distance below which two markers overlap, px. */
  minDistance: number
  /** Viewport size; fanned rows are shifted to stay inside it. */
  width: number
  height: number
  /** Margin kept from the viewport edges, px. */
  edge?: number
}

/** Pixel offsets per point ([dx, dy]); [0, 0] for markers drawn on their point. */
export function declutterOffsets(points: readonly ScreenPoint[], opts: DeclutterOptions): Array<[number, number]> {
  const n = points.length
  const offsets: Array<[number, number]> = points.map(() => [0, 0])
  if (n < 2) return offsets
  const gap = opts.minDistance
  const edge = opts.edge ?? gap / 2

  // Union-find over overlapping pairs; a fanned row can create new overlaps,
  // so clusters are merged until the layout is stable (at most n rounds).
  const parent = points.map((_, i) => i)
  const find = (i: number): number => (parent[i] === i ? i : (parent[i] = find(parent[i]!)))
  const union = (a: number, b: number): boolean => {
    const ra = find(a)
    const rb = find(b)
    if (ra === rb) return false
    parent[Math.max(ra, rb)] = Math.min(ra, rb)
    return true
  }
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      if (Math.hypot(points[i]!.x - points[j]!.x, points[i]!.y - points[j]!.y) < gap) union(i, j)
    }
  }

  for (let round = 0; round < n; round++) {
    const clusters = new Map<number, number[]>()
    for (let i = 0; i < n; i++) {
      const r = find(i)
      clusters.set(r, [...(clusters.get(r) ?? []), i])
    }
    const placed: ScreenPoint[] = points.map((p) => ({ ...p }))
    for (const members of clusters.values()) {
      if (members.length < 2) continue
      const cx = members.reduce((s, i) => s + points[i]!.x, 0) / members.length
      const cy = members.reduce((s, i) => s + points[i]!.y, 0) / members.length
      const span = gap * (members.length - 1)
      let start = cx - span / 2
      // Keep the whole row on screen.
      start = Math.min(Math.max(start, edge), Math.max(edge, opts.width - edge - span))
      const y = Math.min(Math.max(cy, edge), Math.max(edge, opts.height - edge))
      members.forEach((i, k) => {
        placed[i] = { x: start + gap * k, y }
      })
    }
    let merged = false
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        if (find(i) === find(j)) continue
        if (Math.hypot(placed[i]!.x - placed[j]!.x, placed[i]!.y - placed[j]!.y) < gap) merged = union(i, j) || merged
      }
    }
    if (!merged || round === n - 1) {
      for (let i = 0; i < n; i++) offsets[i] = [placed[i]!.x - points[i]!.x, placed[i]!.y - points[i]!.y]
      return offsets
    }
  }
  return offsets
}
