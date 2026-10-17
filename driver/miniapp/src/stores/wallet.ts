// Wallet, ledger and Kaspi top-ups. "I paid" only moves a top-up to
// pending_verification; the balance changes when a Kaspi PDF receipt passes
// every automatic check (the upload response) or when finance confirms it
// (topup.confirmed / wallet.updated events).

import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'
import * as api from '../api/wallet'
import type { UploadProgress } from '../api/upload'
import type { LedgerEntry, TopUp, TopUpSubmit, Wallet } from '../api/types'
import { isApiError, newIdempotencyKey } from '../services/http'
import type { TopUpLimits } from '../services/config'
import { useSessionStore } from './session'

export type AmountError = 'invalid' | 'too_small' | 'too_large'

/** Whole tenge within the configured limits. */
export function validateTopUpAmount(amount: number, limits: TopUpLimits): AmountError | null {
  if (!Number.isInteger(amount) || amount <= 0) return 'invalid'
  if (amount < limits.min) return 'too_small'
  if (amount > limits.max) return 'too_large'
  return null
}

/** Parses digits typed by the driver ("2 000", "2000 ₸") into whole tenge. */
export function parseAmountInput(s: string): number | null {
  const digits = s.replace(/[^\d]/g, '')
  if (!digits || digits.length > 9) return null
  return Number(digits)
}

interface Paged<T> {
  items: T[]
  next: string | null
  loading: boolean
  loaded: boolean
  error: unknown
}

function paged<T>(): Paged<T> {
  return { items: [], next: null, loading: false, loaded: false, error: null }
}

export const useWalletStore = defineStore('wallet', () => {
  const wallet = shallowRef<Wallet | null>(null)
  const walletError = shallowRef<unknown>(null)
  const ledger = ref<Paged<LedgerEntry>>(paged())
  const topups = ref<Paged<TopUp>>(paged())
  const byId = ref<Record<string, TopUp>>({})
  const creating = ref(false)
  /** Key of the create request in progress, reused when the same amount is retried. */
  let pendingCreate: { amount: number; key: string } | null = null
  let walletInFlight: Promise<void> | null = null

  function remember(t: TopUp): TopUp {
    byId.value = { ...byId.value, [t.id]: t }
    const i = topups.value.items.findIndex((x) => x.id === t.id)
    if (i >= 0) topups.value.items.splice(i, 1, t)
    else if (topups.value.loaded) topups.value.items.unshift(t)
    return t
  }

  function refreshWallet(): Promise<void> {
    walletInFlight ??= (async () => {
      try {
        wallet.value = await api.getWallet()
        walletError.value = null
      } catch (err) {
        walletError.value = useSessionStore().handleAccessError(err) ? null : err
      }
    })().finally(() => {
      walletInFlight = null
    })
    return walletInFlight
  }

  async function loadPage<T>(state: Paged<T>, fetch: (before: string | null) => Promise<{ items: T[]; next_cursor: string | null }>, reset: boolean): Promise<void> {
    if (state.loading || (!reset && state.loaded && state.next === null)) return
    state.loading = true
    try {
      const res = await fetch(reset ? null : state.next)
      state.items = reset ? res.items : [...state.items, ...res.items]
      state.next = res.next_cursor
      state.loaded = true
      state.error = null
    } catch (err) {
      state.error = useSessionStore().handleAccessError(err) ? null : err
    } finally {
      state.loading = false
    }
  }

  function loadLedger(reset = true): Promise<void> {
    return loadPage(ledger.value, (b) => api.listTransactions(b), reset)
  }

  async function loadTopUps(reset = true): Promise<void> {
    await loadPage(topups.value, (b) => api.listTopUps(b), reset)
    for (const t of topups.value.items) byId.value[t.id] = t
  }

  async function fetchTopUp(id: string): Promise<TopUp> {
    return remember(await api.getTopUp(id))
  }

  /**
   * Starts a top-up. A retry with the same amount after a failure reuses the
   * idempotency key, so a request that did reach the server is not duplicated.
   */
  async function createTopUp(amount: number): Promise<TopUp> {
    if (!pendingCreate || pendingCreate.amount !== amount) pendingCreate = { amount, key: newIdempotencyKey() }
    creating.value = true
    try {
      const t = await api.createTopUp(amount, pendingCreate.key)
      pendingCreate = null
      return remember(t)
    } catch (err) {
      // A definite rejection (validation, conflict) frees the key; network errors keep it.
      if (isApiError(err) && !err.isNetworkError && err.status !== 429 && err.status < 500) pendingCreate = null
      throw err
    } finally {
      creating.value = false
    }
  }

  async function submitTopUp(id: string, body: TopUpSubmit): Promise<TopUp> {
    const clean: TopUpSubmit = {}
    if (body.payment_reference?.trim()) clean.payment_reference = body.payment_reference.trim()
    if (body.note?.trim()) clean.note = body.note.trim()
    return remember(await api.submitTopUp(id, clean))
  }

  async function cancelTopUp(id: string): Promise<TopUp> {
    return remember(await api.cancelTopUp(id))
  }

  /**
   * Attaches a receipt. A Kaspi PDF that passes every check credits the
   * wallet at once, so the balance is re-read; a receipt used before rejects
   * the top-up (409), so the top-up is re-read before the error is rethrown.
   */
  async function uploadReceipt(
    id: string,
    file: Blob,
    fileName: string,
    onProgress?: (p: UploadProgress) => void,
    signal?: AbortSignal,
  ): Promise<TopUp> {
    try {
      const t = remember(await api.uploadReceipt(id, file, fileName, { onProgress, signal }))
      void refreshWallet()
      if (ledger.value.loaded && t.status === 'confirmed') void loadLedger(true)
      return t
    } catch (err) {
      if (isApiError(err) && err.code === 'receipt_already_used') await fetchTopUp(id).catch(() => undefined)
      throw err
    }
  }

  /** Realtime hint for one top-up (topup.updated / topup.confirmed). */
  async function onTopUpEvent(id: string | null): Promise<void> {
    const tasks: Array<Promise<unknown>> = [refreshWallet()]
    if (id && byId.value[id]) tasks.push(fetchTopUp(id).catch(() => undefined))
    else if (topups.value.loaded) tasks.push(loadTopUps(true))
    if (ledger.value.loaded) tasks.push(loadLedger(true))
    await Promise.all(tasks)
  }

  /** Realtime wallet.updated, reconnect or foreground. */
  async function resync(): Promise<void> {
    const tasks: Array<Promise<unknown>> = [refreshWallet()]
    if (ledger.value.loaded) tasks.push(loadLedger(true))
    if (topups.value.loaded) tasks.push(loadTopUps(true))
    await Promise.all(tasks)
  }

  return {
    wallet,
    walletError,
    ledger,
    topups,
    byId,
    creating,
    refreshWallet,
    loadLedger,
    loadTopUps,
    fetchTopUp,
    createTopUp,
    submitTopUp,
    cancelTopUp,
    uploadReceipt,
    onTopUpEvent,
    resync,
  }
})
