// Top-ups (Kaspi verification and PDF receipts, §6.1), ledger, adjustments
// and wallet reconciliation.

import { api } from '../services/api'
import type { TopUpStatus } from '../utils/status'
import type { AdminRef, DriverRef, Page } from './common'
import { dayEnd, dayStart, opt } from './common'
import type { Wallet } from './drivers'

export interface AdminTopUp {
  id: string
  number: number
  reference: string
  status: TopUpStatus
  provider: string
  amount: number
  verified_amount: number | null
  currency: string
  driver: DriverRef
  payment_url: string | null
  driver_payment_reference: string | null
  driver_note: string | null
  has_receipt: boolean
  receipt_url: string | null
  driver_reported_paid_at: string | null
  provider_transaction_id: string | null
  paid_at: string | null
  confirmed_by: AdminRef | null
  /** Credited by the system from a Kaspi PDF receipt that passed every check. */
  confirmed_automatically: boolean
  confirmed_at: string | null
  rejected_by: AdminRef | null
  rejected_at: string | null
  rejected_reason: string | null
  refunded_by: AdminRef | null
  refunded_at: string | null
  refund_reason: string | null
  expires_at: string
  created_at: string
  updated_at: string
}

export interface TopUpEvent {
  id: number
  from_status: TopUpStatus | null
  to_status: TopUpStatus
  actor_type: 'driver' | 'admin' | 'system' | 'provider'
  actor_id: string | null
  note: string | null
  created_at: string
}

export interface PaymentTransaction {
  id: string
  provider: string
  provider_transaction_id: string
  amount: number
  currency: string
  paid_at: string | null
  payer_info: string | null
  source: PaymentSource
  recorded_by: AdminRef | null
  created_at: string
}

export type PaymentSource = 'manual_admin' | 'webhook' | 'receipt_auto'

export type DuplicateWarningKind = 'same_driver_payment_reference' | 'provider_transaction_recorded' | 'receipt_serial_used'

export interface DuplicateWarning {
  kind: DuplicateWarningKind | (string & {})
  topup_id: string | null
  reference: string | null
  status: string | null
  driver_code: string | null
  payment_transaction_id: string | null
  provider_transaction_id: string | null
}

export type ReceiptDecision = 'auto_confirmed' | 'manual_review'

/** A parsed Kaspi PDF receipt (payments.AdminReceiptView). */
export interface AdminReceipt {
  id: string
  provider: string
  file_id: string
  /** /api/v1/admin/files/{file_id}: the PDF exactly as uploaded. */
  file_url: string
  file_sha256: string
  /** «№ чека», e.g. QR18149336524 — unique across all top-ups. */
  receipt_serial: string | null
  qr_payload_hash: string | null
  amount: number | null
  paid_at: string | null
  recipient_name: string | null
  recipient_bin: string | null
  /** Payer as printed, masked by the API («Е*** Б.»). */
  payer_masked: string | null
  payment_method: string | null
  producer: string | null
  parse_status: 'parsed' | 'partial' | 'unreadable' | (string & {})
  flags: string[]
  decision: ReceiptDecision | (string & {})
  reasons: string[]
  created_at: string
}

/** Confirmation form values taken from the latest parsed receipt. */
export interface ConfirmPrefill {
  verified_amount: number | null
  provider_transaction_id: string | null
  paid_at: string | null
  payer_info: string | null
}

export interface AdminTopUpDetail extends AdminTopUp {
  events: TopUpEvent[]
  payment_transaction: PaymentTransaction | null
  duplicate_warnings: DuplicateWarning[]
  /** Latest parsed PDF receipt (null without one, or when only an image was sent). */
  receipt: AdminReceipt | null
  /** Every PDF receipt attached to the top-up, newest first. */
  receipts: AdminReceipt[]
  confirm_prefill: ConfirmPrefill | null
}

/** Receipt flags that never block automatic crediting (receipt.infoFlags). */
export const INFO_RECEIPT_FLAGS: readonly string[] = ['qr_not_found', 'bin_missing', 'status_kazakh']

/** top_up_requests.reject_reason of a top-up whose receipt was used before. */
export const REJECT_DUPLICATE_RECEIPT = 'duplicate_receipt'

export interface TopUpConfirmBody {
  verified_amount: number
  provider_transaction_id: string
  paid_at: string
  payer_info?: string
  note?: string
}

export const LEDGER_TYPES = [
  'topup_credit',
  'topup_refund',
  'commission_reserve',
  'commission_release',
  'commission_charge',
  'commission_refund',
  'adjustment_credit',
  'adjustment_debit',
] as const
export type LedgerEntryType = (typeof LEDGER_TYPES)[number]

export interface AdminLedgerEntry {
  id: number
  entry_type: LedgerEntryType
  balance_delta: number
  reserved_delta: number
  balance_after: number
  reserved_after: number
  currency: string
  order_id: string | null
  order_number: number | null
  topup_id: string | null
  topup_reference: string | null
  description: string | null
  created_at: string
  wallet_id: string
  driver: DriverRef
  admin: AdminRef | null
  reservation_id: string | null
  idempotency_key: string
}

export interface ReconciliationMismatch {
  wallet_id: string
  driver: DriverRef
  balance: number
  reserved: number
  ledger_balance: number
  ledger_reserved: number
  balance_diff: number
  reserved_diff: number
  active_reservations: number
  reasons: string[]
}

export interface ReconciliationResult {
  checked_at: string
  wallets_checked: number
  ok: boolean
  mismatches: ReconciliationMismatch[]
}

export interface WalletAdjustmentResult {
  wallet: Wallet
  entry: AdminLedgerEntry
}

export interface TopUpFilters {
  status?: string
  driver_id?: string
  from?: string
  to?: string
  q?: string
}

export interface LedgerFilters {
  driver_id?: string
  type?: string
  from?: string
  to?: string
}

export function topUpQuery(f: TopUpFilters): Record<string, string | undefined> {
  return { status: opt(f.status), driver_id: opt(f.driver_id), from: dayStart(f.from), to: dayEnd(f.to), q: opt(f.q?.trim()) }
}

export function ledgerQuery(f: LedgerFilters): Record<string, string | undefined> {
  return { driver_id: opt(f.driver_id), type: opt(f.type), from: dayStart(f.from), to: dayEnd(f.to) }
}

export const topUpsApi = {
  list(f: TopUpFilters, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<AdminTopUp>> {
    return api.get('/admin/topups', { query: { ...topUpQuery(f), page, page_size: pageSize }, signal })
  },
  get(id: string, signal?: AbortSignal): Promise<AdminTopUpDetail> {
    return api.get(`/admin/topups/${encodeURIComponent(id)}`, { signal })
  },
  receiptPath(id: string): string {
    return `/admin/topups/${encodeURIComponent(id)}/receipt`
  },
  confirmPayment(id: string, body: TopUpConfirmBody): Promise<AdminTopUpDetail> {
    return api.post(`/admin/topups/${encodeURIComponent(id)}/confirm`, body)
  },
  reject(id: string, reason: string): Promise<AdminTopUpDetail> {
    return api.post(`/admin/topups/${encodeURIComponent(id)}/reject`, { reason })
  },
  refund(id: string, reason: string): Promise<AdminTopUpDetail> {
    return api.post(`/admin/topups/${encodeURIComponent(id)}/refund`, { reason })
  },
}

export const ledgerApi = {
  list(f: LedgerFilters, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<AdminLedgerEntry>> {
    return api.get('/admin/ledger', { query: { ...ledgerQuery(f), page, page_size: pageSize }, signal })
  },
  driverLedger(driverId: string, type: string, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<AdminLedgerEntry>> {
    return api.get(`/admin/drivers/${encodeURIComponent(driverId)}/ledger`, {
      query: { type: opt(type), page, page_size: pageSize },
      signal,
    })
  },
  reconcile(signal?: AbortSignal): Promise<ReconciliationResult> {
    return api.get('/admin/wallets/reconciliation', { signal })
  },
  adjust(driverId: string, body: { amount: number; reason: string; idempotency_key: string }): Promise<WalletAdjustmentResult> {
    return api.post(`/admin/drivers/${encodeURIComponent(driverId)}/wallet/adjustments`, body)
  },
}
