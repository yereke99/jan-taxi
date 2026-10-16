// Turns API errors into operator-facing text: a toast for the error code and
// per-field messages for validation_failed (fields: {"name.kk": "too_long"}).

import { useI18n } from 'vue-i18n'
import { toast } from '../components/toast'
import { DEFAULT_LOCALE } from '../i18n'
import { isApiError } from '../services/api'
import { formatTime } from '../utils/format'

export function useErrors() {
  const { t, te } = useI18n()

  function text(err: unknown): string {
    if (isApiError(err)) {
      // A machine reason in details is more specific than the code
      // (conflict/active_order_exists, validation_failed/different_city …).
      if (typeof err.details.reason === 'string') {
        const k = `errors.reason_${err.details.reason}`
        if (te(k, DEFAULT_LOCALE)) return t(k)
      }
      const key = `errors.${err.code}`
      if (te(key, DEFAULT_LOCALE)) {
        if (err.code === 'account_locked' && typeof err.details.locked_until === 'string') {
          return t('errors.account_locked_until', { time: formatTime(err.details.locked_until) })
        }
        return t(key)
      }
      if (err.status >= 500) return t('errors.internal')
    }
    return t('errors.unknown')
  }

  /** Translated reason for one field ("too_long" → "Too long"). */
  function reason(code: string): string {
    const key = `field_errors.${code}`
    return te(key, DEFAULT_LOCALE) ? t(key) : t('field_errors.invalid')
  }

  /** Field → message map for a validation error (empty for other errors). */
  function fields(err: unknown): Record<string, string> {
    if (!isApiError(err)) return {}
    const out: Record<string, string> = {}
    for (const [k, v] of Object.entries(err.fields)) out[k] = reason(v)
    return out
  }

  /** Shows a toast for err; returns the field messages for forms. */
  function report(err: unknown): Record<string, string> {
    const f = fields(err)
    toast.error(text(err))
    return f
  }

  /** Translates client-side reason codes ({field: code}) for a form. */
  function translate(codes: Record<string, string>): Record<string, string> {
    const out: Record<string, string> = {}
    for (const [k, v] of Object.entries(codes)) out[k] = reason(v)
    return out
  }

  return { text, reason, fields, report, translate }
}
