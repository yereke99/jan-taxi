// Order details form rules per category (ARCHITECTURE §5, §11), identical to
// the API's checks so the passenger sees problems before submitting:
//   taxi    comment optional (≤120)
//   parcel  cargo description required (≤120), KZ recipient phone, ack
//   other   cargo description required (≤120), ack
// Lengths are code points after NFC and cleaning, exactly like the backend.

import type { OrderCreateRequest } from '../api/types'
import { normalizeKzPhone } from '../utils/phone'
import type { Category } from '../utils/status'
import { cleanedLength, cleanText, TEXT_MAX } from '../utils/text'

export interface DetailsInput {
  category: Category
  comment: string
  cargoDescription: string
  /** As typed (masked input); normalised to E.164 on submit. */
  recipientPhone: string
  prohibitedItemsAck: boolean
}

export type DetailsField = 'comment' | 'cargo_description' | 'recipient_phone' | 'prohibited_items_ack'

/** Field → reason code (translated through field_errors.*). */
export type DetailsErrors = Partial<Record<DetailsField, string>>

export function isDelivery(category: Category): boolean {
  return category !== 'taxi'
}

export function validateDetails(d: DetailsInput): DetailsErrors {
  const errors: DetailsErrors = {}
  if (cleanedLength(d.comment) > TEXT_MAX) errors.comment = 'too_long'
  if (!isDelivery(d.category)) return errors
  const cargoLen = cleanedLength(d.cargoDescription)
  if (cargoLen === 0) errors.cargo_description = 'required'
  else if (cargoLen > TEXT_MAX) errors.cargo_description = 'too_long'
  if (d.category === 'parcel') {
    if (d.recipientPhone.trim() === '') errors.recipient_phone = 'required'
    else if (!normalizeKzPhone(d.recipientPhone)) errors.recipient_phone = 'invalid_phone'
  }
  if (!d.prohibitedItemsAck) errors.prohibited_items_ack = 'must_be_true'
  return errors
}

/** The category-specific part of POST /orders (fields not allowed for the category are omitted). */
export function detailsPayload(d: DetailsInput): Pick<OrderCreateRequest, 'comment' | 'cargo_description' | 'recipient_phone' | 'prohibited_items_ack'> {
  const out: Pick<OrderCreateRequest, 'comment' | 'cargo_description' | 'recipient_phone' | 'prohibited_items_ack'> = {}
  const comment = cleanText(d.comment)
  if (comment) out.comment = comment
  if (!isDelivery(d.category)) return out
  out.cargo_description = cleanText(d.cargoDescription)
  out.prohibited_items_ack = d.prohibitedItemsAck
  if (d.category === 'parcel') out.recipient_phone = normalizeKzPhone(d.recipientPhone) ?? d.recipientPhone.trim()
  return out
}
