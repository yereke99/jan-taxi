// Clean axis ticks (0 / 500 / 1 000 …) for the dashboard charts.

/** Rounds up to 1, 2, 2.5 or 5 × 10^n. */
export function niceStep(raw: number): number {
  if (!Number.isFinite(raw) || raw <= 0) return 1
  const exp = Math.floor(Math.log10(raw))
  const base = 10 ** exp
  const f = raw / base
  const nice = f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10
  return nice * base
}

/** Ticks from 0 to at least max, about `count` intervals, integer steps when values are integers. */
export function niceTicks(max: number, count = 4): number[] {
  const top = Math.max(1, max)
  let step = niceStep(top / count)
  if (step < 1) step = 1
  step = Math.ceil(step)
  const ticks: number[] = []
  for (let v = 0; v < top + step; v += step) {
    ticks.push(v)
    if (v >= top) break
  }
  return ticks
}

/** Compact axis label: 1 200 → "1,2K", 3 400 000 → "3,4M". */
export function compactNumber(n: number, locale: 'kk' | 'ru' | 'en'): string {
  const abs = Math.abs(n)
  const sep = locale === 'en' ? '.' : ','
  const fmt = (v: number, unit: string): string => {
    const s = v >= 10 ? String(Math.round(v)) : String(Math.round(v * 10) / 10)
    return s.replace('.', sep) + unit
  }
  if (abs >= 1_000_000) return fmt(n / 1_000_000, 'M')
  if (abs >= 10_000) return fmt(n / 1000, 'K')
  return String(n)
}
