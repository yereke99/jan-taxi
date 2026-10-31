// Error sentences for the driver app: the shared code → text mapping plus the
// driver-specific 409 conflict reasons (details.reason), the 422 reasons of
// PATCH /driver/availability and the registration answers (IIN taken or
// invalid, what is still missing).

import { useI18n } from 'vue-i18n'
import { REGISTRATION_FIELDS, type RegistrationField } from '../api/types'
import { useErrorText } from '../i18n/core'
import { isApiError } from '../services/http'

function registrationFields(v: unknown): RegistrationField[] {
  return Array.isArray(v) ? v.filter((f): f is RegistrationField => (REGISTRATION_FIELDS as readonly unknown[]).includes(f)) : []
}

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
      if (err.code === 'iin_taken') return t('registration.personal.iin_errors.taken')
      if (err.code === 'validation_failed' && (err.fields.iin === 'checksum' || err.fields.iin === 'invalid')) {
        return t(`registration.personal.iin_errors.${err.fields.iin === 'checksum' ? 'checksum' : 'length'}`)
      }
      if (err.code === 'registration_incomplete') {
        const missing = registrationFields(err.details.missing_fields)
        return missing.length > 0
          ? t('registration.missing', { fields: missing.map((f) => t(`registration.field.${f}`)).join(', ') })
          : t('errors.registration_incomplete')
      }
    }
    return base(err)
  }
}
