// Flat before/after comparison for audit entries: nested objects become
// dotted paths ("vehicle.plate"), arrays are compared as whole values.

export type DiffKind = 'added' | 'removed' | 'changed' | 'same'

export interface DiffRow {
  path: string
  kind: DiffKind
  before: string | undefined
  after: string | undefined
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v)
}

/** Parses JSON strings (some audit payloads arrive double-encoded). */
function normalize(v: unknown): unknown {
  if (typeof v === 'string') {
    const s = v.trim()
    if ((s.startsWith('{') && s.endsWith('}')) || (s.startsWith('[') && s.endsWith(']'))) {
      try {
        return JSON.parse(s) as unknown
      } catch {
        return v
      }
    }
  }
  return v
}

export function flatten(value: unknown, prefix = '', out: Map<string, string> = new Map()): Map<string, string> {
  const v = normalize(value)
  if (isPlainObject(v) && Object.keys(v).length > 0) {
    for (const [k, child] of Object.entries(v)) flatten(child, prefix ? `${prefix}.${k}` : k, out)
  } else if (v !== undefined) {
    out.set(prefix || '·', JSON.stringify(v))
  }
  return out
}

/** Rows for every path present before or after, changed ones first. */
export function jsonDiff(before: unknown, after: unknown): DiffRow[] {
  const a = before === null || before === undefined ? new Map<string, string>() : flatten(before)
  const b = after === null || after === undefined ? new Map<string, string>() : flatten(after)
  const paths = [...new Set([...a.keys(), ...b.keys()])]
  const rows = paths.map<DiffRow>((path) => {
    const x = a.get(path)
    const y = b.get(path)
    const kind: DiffKind = x === undefined ? 'added' : y === undefined ? 'removed' : x === y ? 'same' : 'changed'
    return { path, kind, before: x, after: y }
  })
  const order: Record<DiffKind, number> = { changed: 0, added: 1, removed: 2, same: 3 }
  return rows.sort((r1, r2) => order[r1.kind] - order[r2.kind] || r1.path.localeCompare(r2.path))
}
