// Top-ups (Kaspi manual verification, §6.1), ledger, adjustments and
// wallet reconciliation.

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
  source: 'manual_admin' | 'webhook'
  recorded_by: AdminRef | null
  created_at: string
}

export interface DuplicateWarning {
  kind: string
  topup_id: string | null
  reference: string | null
  status: string | null
  driver_code: string | null
  payment_transaction_id: string | null
  provider_transaction_id: string | null
}

export interface AdminTopUpDetail extends AdminTopUp {
  events: TopUpEvent[]
  payment_transaction: PaymentTransaction | null
  duplicate_warnings: DuplicateWarning[]
}

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
