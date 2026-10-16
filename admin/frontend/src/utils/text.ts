// Text length rules shared with the API (§11): limits count Unicode code
// points after NFC normalisation.

/** Number of code points after NFC normalisation. */
export function codePoints(s: string): number {
  return [...s.normalize('NFC')].length
}

/** Trimmed text; empty input becomes ''. */
export function clean(s: string | null | undefined): string {
  return (s ?? '').trim()
}

/** True when the trimmed text is non-empty and within max code points. */
export function withinLimit(s: string, max: number, min = 1): boolean {
  const n = codePoints(clean(s))
  return n >= min && n <= max
}
