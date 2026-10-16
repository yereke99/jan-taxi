// Shared with the other Mini App — keep both copies identical (make verify-shared).

/** True when the user asked the OS to minimise motion (§15 "Motion"). */
export function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-reduced-motion: reduce)').matches
    : false
}

/** Animation duration that collapses to 0 under reduced motion. */
export function motionMs(ms: number): number {
  return prefersReducedMotion() ? 0 : ms
}
