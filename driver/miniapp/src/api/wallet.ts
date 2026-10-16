// Wallet, ledger and Kaspi top-up endpoints.

import { http, type RequestOptions } from '../services/http'
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
  return list(await http.get<CursorList<TopUp>>('/driver/wallet/topups', { query: { limit, before } }))
}

export function getTopUp(topupId: string, opts?: RequestOptions): Promise<TopUp> {
  return http.get<TopUp>(`/driver/wallet/topups/${id(topupId)}`, opts)
}

export function createTopUp(amount: number, idempotencyKey: string): Promise<TopUp> {
  return http.post<TopUp>('/driver/wallet/topups', { amount, idempotency_key: idempotencyKey })
}

export function submitTopUp(topupId: string, body: TopUpSubmit): Promise<TopUp> {
  return http.post<TopUp>(`/driver/wallet/topups/${id(topupId)}/submit`, body)
}

export function cancelTopUp(topupId: string): Promise<TopUp> {
  return http.post<TopUp>(`/driver/wallet/topups/${id(topupId)}/cancel`)
}

export function uploadReceipt(
  topupId: string,
  file: Blob,
  opts: { onProgress?: (p: UploadProgress) => void; signal?: AbortSignal } = {},
): Promise<TopUp> {
  const form = new FormData()
  form.append('file', file, 'receipt.jpg')
  return uploadWithProgress<TopUp>(`/driver/wallet/topups/${id(topupId)}/receipt`, form, opts)
}
