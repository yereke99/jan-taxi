// Kaspi receipt handling for top-ups (ARCHITECTURE §6.1): which file the
// driver picked, how it is named on upload, what the server decided and how
// an upload error reads. Pure functions, unit-tested.

import type { TopUp } from '../../api/types'
import { isApiError } from '../../services/http'

/** The API keeps receipt PDFs up to 5 MiB (Kaspi receipts are about 25 KB). */
export const MAX_RECEIPT_PDF_BYTES = 5 * 1024 * 1024

/** File types the picker offers: the Kaspi PDF first, screenshots as evidence. */
export const RECEIPT_ACCEPT = 'application/pdf,.pdf,image/jpeg,image/png,image/webp'

const PDF_MAGIC = '%PDF-'

/** Kaspi names shared receipts receipt_QR<number>.pdf. */
const SAFE_NAME = /[^A-Za-z0-9._-]+/g

export type ReceiptFileKind = 'pdf' | 'image' | 'unsupported'

/** Classifies a picked file by its type and name (the content is checked separately). */
export function receiptFileKind(file: { type: string; name?: string }): ReceiptFileKind {
  const type = (file.type || '').toLowerCase()
  const name = (file.name || '').toLowerCase()
  if (type === 'application/pdf' || name.endsWith('.pdf')) return 'pdf'
  if (type === 'image/jpeg' || type === 'image/png' || type === 'image/webp' || /\.(jpe?g|png|webp)$/.test(name)) return 'image'
  // Some Android pickers report no type for files from the Downloads folder.
  if (!type && !name.includes('.')) return 'pdf'
  return 'unsupported'
}

/** True when the blob starts with the %PDF- header. */
export async function hasPdfHeader(blob: Blob): Promise<boolean> {
  try {
    const head = await blob.slice(0, PDF_MAGIC.length).arrayBuffer()
    return new TextDecoder('latin1').decode(head) === PDF_MAGIC
  } catch {
    return false
  }
}

/**
 * The file name sent with a PDF: the original one (it carries the receipt
 * number the server cross-checks) reduced to safe characters, else receipt.pdf.
 */
export function receiptUploadName(original: string | undefined): string {
  const base = (original ?? '').split(/[\\/]/).pop() ?? ''
  const cleaned = base.replace(SAFE_NAME, '_').replace(/^[._]+/, '').slice(0, 96)
  if (!cleaned || cleaned.toLowerCase() === 'pdf') return 'receipt.pdf'
  return cleaned.toLowerCase().endsWith('.pdf') ? cleaned : `${cleaned}.pdf`
}

/** What happened to a top-up's receipt, as shown to the driver. */
export type ReceiptOutcome =
  /** Every automatic check passed: the balance is already credited. */
  | 'credited'
  /** A finance administrator checks it (reasons say why it was not automatic). */
  | 'review'
  /** The receipt was used before: the top-up is rejected. */
  | 'duplicate'
  /** Credited by an administrator after a review or without a PDF. */
  | 'confirmed'
  | 'rejected'
  | 'expired'
  | 'refunded'
  /** Waiting for the payment and the receipt. */
  | 'waiting'

export function receiptOutcome(t: Pick<TopUp, 'status' | 'receipt' | 'rejected_reason'>): ReceiptOutcome {
  switch (t.status) {
    case 'confirmed':
      return t.receipt?.decision === 'auto_confirmed' ? 'credited' : 'confirmed'
    case 'pending_verification':
      return 'review'
    case 'rejected':
      return t.rejected_reason === 'duplicate_receipt' || t.receipt?.reasons.includes('duplicate_receipt') ? 'duplicate' : 'rejected'
    case 'expired':
      return 'expired'
    case 'refunded':
      return 'refunded'
    default:
      return 'waiting'
  }
}

/** Review reasons the app explains (payments/receipt.go Reason* constants). */
export const RECEIPT_REASONS = [
  'amount_mismatch',
  'recipient_mismatch',
  'paid_before_topup',
  'paid_in_future',
  'receipt_too_old',
  'daily_count_limit',
  'daily_amount_limit',
  'auto_confirm_disabled',
  'receipt_unreadable',
  'receipt_incomplete',
  'receipt_inconsistent',
  'tamper_suspected',
  'image_receipt',
] as const
export type ReceiptReason = (typeof RECEIPT_REASONS)[number]

/** The known reasons of a receipt, in server order, without duplicates. */
export function knownReasons(reasons: readonly string[] | undefined): ReceiptReason[] {
  const out: ReceiptReason[] = []
  for (const r of reasons ?? []) {
    if ((RECEIPT_REASONS as readonly string[]).includes(r) && !out.includes(r as ReceiptReason)) out.push(r as ReceiptReason)
  }
  return out
}

/** How a failed receipt upload reads (topup.upload_errors.<kind>). */
export type ReceiptUploadError =
  | 'duplicate'
  | 'not_pdf'
  | 'too_large'
  | 'unreadable'
  | 'not_allowed'
  | 'closed'
  | 'rate_limited'
  | 'network'
  | 'other'

export function receiptUploadError(err: unknown): ReceiptUploadError {
  if (!isApiError(err)) return 'other'
  if (err.isNetworkError || err.code === 'timeout') return 'network'
  switch (err.code) {
    case 'receipt_already_used':
      return 'duplicate'
    case 'unsupported_file_type':
    case 'unsupported_media_type':
      return 'not_pdf'
    case 'file_too_large':
    case 'payload_too_large':
      return 'too_large'
    case 'pdf_unreadable':
      return 'unreadable'
    case 'pdf_not_allowed':
      return 'not_allowed'
    case 'invalid_transition':
    case 'no_open_topup':
      return 'closed'
    case 'too_many_requests':
      return 'rate_limited'
    default:
      return err.status === 429 ? 'rate_limited' : 'other'
  }
}
