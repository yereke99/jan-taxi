// Wallet, ledger and Kaspi top-up endpoints.

import { http, type RequestOptions } from '../services/http'
import { normalizeTopUp } from './normalize'
import type { CursorList, LedgerEntry, TopUp, TopUpSubmit, Wallet } from './types'
import { uploadWithProgress, type UploadProgress } from './upload'

function id(topupId: string): string {
  return encodeURIComponent(topupId)
}

function list<T>(res: CursorList<T> | undefined): CursorList<T> {
  return { items: Array.isArray(res?.items) ? res.items : [], next_cursor: res?.next_cursor ?? null }
}

export function getWallet(opts?: RequestOptions): Promise<Wallet> {
  return http.get<Wallet>('/driver/wallet', opts)
}

export async function listTransactions(before: string | null, limit = 20): Promise<CursorList<LedgerEntry>> {
  return list(await http.get<CursorList<LedgerEntry>>('/driver/wallet/transactions', { query: { limit, before } }))
}

export async function listTopUps(before: string | null, limit = 20): Promise<CursorList<TopUp>> {
  const res = list(await http.get<CursorList<unknown>>('/driver/wallet/topups', { query: { limit, before } }))
  return { items: res.items.map(normalizeTopUp), next_cursor: res.next_cursor }
}

export async function getTopUp(topupId: string, opts?: RequestOptions): Promise<TopUp> {
  return normalizeTopUp(await http.get<unknown>(`/driver/wallet/topups/${id(topupId)}`, opts))
}

export async function createTopUp(amount: number, idempotencyKey: string): Promise<TopUp> {
  return normalizeTopUp(await http.post<unknown>('/driver/wallet/topups', { amount, idempotency_key: idempotencyKey }))
}

export async function submitTopUp(topupId: string, body: TopUpSubmit): Promise<TopUp> {
  return normalizeTopUp(await http.post<unknown>(`/driver/wallet/topups/${id(topupId)}/submit`, body))
}

export async function cancelTopUp(topupId: string): Promise<TopUp> {
  return normalizeTopUp(await http.post<unknown>(`/driver/wallet/topups/${id(topupId)}/cancel`))
}

/**
 * Attaches a receipt: the Kaspi PDF (checked and possibly credited at once) or
 * an image (evidence only). `fileName` is sent as the part's file name; Kaspi
 * names its PDFs after the receipt number, which the server cross-checks.
 * A receipt used before answers 409 receipt_already_used.
 */
export async function uploadReceipt(
  topupId: string,
  file: Blob,
  fileName: string,
  opts: { onProgress?: (p: UploadProgress) => void; signal?: AbortSignal } = {},
): Promise<TopUp> {
  const form = new FormData()
  form.append('file', file, fileName)
  return normalizeTopUp(await uploadWithProgress<unknown>(`/driver/wallet/topups/${id(topupId)}/receipt`, form, opts))
}
