// Error sentences for the driver app: the shared code → text mapping plus the
// driver-specific 409 conflict reasons (details.reason) and the 422 reasons
// of PATCH /driver/availability.

import { useI18n } from 'vue-i18n'
import { useErrorText } from '../i18n/core'
import { isApiError } from '../services/http'

export function useDriverErrorText(): (err: unknown) => string {
  const { t, te } = useI18n()
  const base = useErrorText()
  return (err) => {
    if (isApiError(err)) {
      const reason = typeof err.details.reason === 'string' ? err.details.reason : ''
      if (err.code === 'conflict' || err.code === 'validation_failed') {
        const key = `errors.conflict_reasons.${reason}`
        if (reason && te(key)) return t(key)
      }
      if (err.code === 'location_required') return t('errors.location_required')
    }
    return base(err)
  }
}
