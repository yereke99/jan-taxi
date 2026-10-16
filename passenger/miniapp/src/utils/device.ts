// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Low-end device detection for the Liquid Glass fallback (§15.1): blur over a
// live WebGL map is expensive on weak Android WebViews, so those devices get
// solid white chrome instead.

interface NavigatorHints {
  userAgent?: string
  hardwareConcurrency?: number
  /** Chromium only: approximate RAM in GiB. */
  deviceMemory?: number
}

/** True for Android devices with little memory or few cores. */
export function isLowEndDevice(platform: string, nav: NavigatorHints | undefined = globalThis.navigator): boolean {
  if (!nav) return false
  const android = platform.startsWith('android') || /Android/i.test(nav.userAgent ?? '')
  if (!android) return false
  const memory = typeof nav.deviceMemory === 'number' ? nav.deviceMemory : null
  const cores = typeof nav.hardwareConcurrency === 'number' ? nav.hardwareConcurrency : null
  return (memory !== null && memory <= 3) || (cores !== null && cores <= 4)
}

/** Sets html[data-jt-low-end] so .jt-glass renders as solid white. */
export function applyLowEndFlag(platform: string): void {
  if (typeof document === 'undefined') return
  if (isLowEndDevice(platform)) document.documentElement.setAttribute('data-jt-low-end', '')
  else document.documentElement.removeAttribute('data-jt-low-end')
}
