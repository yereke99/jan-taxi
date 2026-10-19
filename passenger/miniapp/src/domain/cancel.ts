// Passenger cancellation (ARCHITECTURE §22.3): only the passenger cancels, a
// reason is required, and a comment is required for "other". The reasons come
// from GET /public/config (labels in kk/ru/en); when the config has none, the
// five reasons of §22.3 are used with the app's own labels. The rules match
// the API so problems show before the request is sent.

import { CANCEL_REASON_CODES, type CancelOrderRequest, type CancelReasonCode } from '../api/types'
import type { OrderStatus } from '../utils/status'
import { pickLocalized, type CancelReasonInfo } from '../services/config'
import type { Locale } from '../utils/format'
import { cleanedLength, cleanText, TEXT_MAX } from '../utils/text'

export const CANCEL_COMMENT_MAX = TEXT_MAX

export interface CancelReasonOption {
  code: CancelReasonCode
  label: string
  commentRequired: boolean
}

export function isCancelReasonCode(v: unknown): v is CancelReasonCode {
  return typeof v === 'string' && (CANCEL_REASON_CODES as readonly string[]).includes(v)
}

/**
 * The reasons to offer, in display order: the config's list (codes the API
 * accepts only), or all of §22.3 with `fallbackLabel` when the config has none.
 */
export function cancelReasonOptions(
  fromConfig: readonly CancelReasonInfo[],
  lang: Locale,
  fallbackLabel: (code: CancelReasonCode) => string,
): CancelReasonOption[] {
  const known = fromConfig.filter((r): r is CancelReasonInfo & { code: CancelReasonCode } => isCancelReasonCode(r.code))
  if (known.length === 0) {
    return CANCEL_REASON_CODES.map((code) => ({ code, label: fallbackLabel(code), commentRequired: code === 'other' }))
  }
  return known.map((r) => ({
    code: r.code,
    label: pickLocalized(r.label, lang) || fallbackLabel(r.code),
    // "other" always needs a comment: the API requires it whatever the config says.
    commentRequired: r.comment_required || r.code === 'other',
  }))
}

export type CancelField = 'reason_code' | 'comment'

/** Field → reason code (translated through field_errors.*); empty when the request can be sent. */
export function validateCancel(reason: CancelReasonOption | null, comment: string): Partial<Record<CancelField, string>> {
  const errors: Partial<Record<CancelField, string>> = {}
  if (!reason) errors.reason_code = 'required'
  const len = cleanedLength(comment)
  if (len > CANCEL_COMMENT_MAX) errors.comment = 'too_long'
  else if (len === 0 && reason?.commentRequired) errors.comment = 'required'
  return errors
}

/**
 * POST /orders/{id}/cancel body; the comment is sent cleaned and only when
 * given. `seen` is the order status on screen: the API cancels only that
 * order, so a driver who took it meanwhile is shown first (409 order_changed).
 */
export function cancelPayload(reason: CancelReasonOption, comment: string, seen: OrderStatus): CancelOrderRequest {
  const text = cleanText(comment)
  const body: CancelOrderRequest = { reason_code: reason.code, expected_status: seen }
  if (text) body.comment = text
  return body
}
