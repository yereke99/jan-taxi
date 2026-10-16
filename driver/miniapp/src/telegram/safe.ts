// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// SDK calls can throw when a method is unsupported by the client version, the
// component is not mounted, or the app runs outside Telegram. None of those may
// break the UI, so every optional SDK call goes through these wrappers.

/** Runs fn and returns its result, or undefined if it throws. */
export function safeCall<T>(fn: () => T): T | undefined {
  try {
    return fn()
  } catch (err) {
    if (import.meta.env.DEV) console.debug('[telegram] call skipped:', err)
    return undefined
  }
}

/** Runs a boolean getter, treating a throw as false. */
export function safeBool(fn: () => boolean): boolean {
  return safeCall(fn) === true
}

/** Error name, robust to classes created by error-kid and to cross-realm errors. */
export function errorName(err: unknown): string {
  return err && typeof err === 'object' && 'name' in err ? String((err as { name: unknown }).name) : ''
}

/** Error message, or an empty string. */
export function errorMessage(err: unknown): string {
  return err && typeof err === 'object' && 'message' in err ? String((err as { message: unknown }).message) : ''
}
