import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { TopUp, Wallet } from '../api/types'
import * as api from '../api/wallet'
import { ApiError } from '../services/http'
import { parseAmountInput, useWalletStore, validateTopUpAmount } from './wallet'

vi.mock('../api/wallet', () => ({
  getWallet: vi.fn(),
  listTransactions: vi.fn(),
  listTopUps: vi.fn(),
  getTopUp: vi.fn(),
  createTopUp: vi.fn(),
  submitTopUp: vi.fn(),
  cancelTopUp: vi.fn(),
  uploadReceipt: vi.fn(),
}))

const mocked = vi.mocked(api)

function topup(over: Partial<TopUp> = {}): TopUp {
  return {
    id: 't1',
    number: 123,
    reference: 'EMB001-TOPUP-000123',
    amount: 2000,
    verified_amount: null,
    currency: 'KZT',
    status: 'created',
    provider: 'kaspi_manual',
    payment: {
      method: 'kaspi_manual',
      url: 'https://pay.kaspi.kz/pay/abc',
      recipient_name: 'Jan Taxi',
      recipient_phone: null,
      instructions: '',
      reference: 'EMB001-TOPUP-000123',
      amount: 2000,
      currency: 'KZT',
    },
    driver_payment_reference: null,
    driver_note: null,
    has_receipt: false,
    can_upload_receipt: true,
    can_submit: true,
    can_cancel: true,
    created_at: '2026-10-16T10:00:00Z',
    driver_reported_paid_at: null,
    expires_at: '2026-10-17T10:00:00Z',
    confirmed_at: null,
    rejected_at: null,
    rejected_reason: null,
    refunded_at: null,
    receipt: null,
    ...over,
  }
}

const WALLET: Wallet = { balance: 1000, reserved: 0, available: 1000, currency: 'KZT', min_online_balance: 500, updated_at: '' }

describe('top-up amount rules', () => {
  const limits = { min: 500, max: 100_000, presets: [1000, 2000, 5000] }

  it('accepts whole tenge within the limits', () => {
    expect(validateTopUpAmount(2000, limits)).toBeNull()
    expect(validateTopUpAmount(499, limits)).toBe('too_small')
    expect(validateTopUpAmount(100_001, limits)).toBe('too_large')
    expect(validateTopUpAmount(1500.5, limits)).toBe('invalid')
    expect(validateTopUpAmount(0, limits)).toBe('invalid')
  })

  it('parses typed amounts', () => {
    expect(parseAmountInput('2 000 ₸')).toBe(2000)
    expect(parseAmountInput('abc')).toBeNull()
    expect(parseAmountInput('1234567890')).toBeNull()
  })
})

describe('wallet store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('reuses the idempotency key when the same amount is retried after a network error', async () => {
    const store = useWalletStore()
    mocked.createTopUp.mockRejectedValueOnce(new ApiError(0, 'network_error', 'down')).mockResolvedValueOnce(topup())
    await expect(store.createTopUp(2000)).rejects.toBeInstanceOf(ApiError)
    await store.createTopUp(2000)
    const [first, second] = mocked.createTopUp.mock.calls
    expect(first![0]).toBe(2000)
    expect(second![1]).toBe(first![1])
    expect(store.byId.t1?.reference).toBe('EMB001-TOPUP-000123')
  })

  it('uses a new key for a different amount or after a definite rejection', async () => {
    const store = useWalletStore()
    mocked.createTopUp
      .mockRejectedValueOnce(new ApiError(422, 'validation_failed', 'small', { amount: 'too_small' }))
      .mockResolvedValueOnce(topup())
      .mockResolvedValueOnce(topup({ id: 't2', amount: 5000 }))
    await expect(store.createTopUp(2000)).rejects.toBeInstanceOf(ApiError)
    await store.createTopUp(2000)
    await store.createTopUp(5000)
    const keys = mocked.createTopUp.mock.calls.map((c) => c[1])
    expect(new Set(keys).size).toBe(3)
  })

  it('"I paid" sends only filled, trimmed fields and never touches the balance', async () => {
    const store = useWalletStore()
    store.wallet = WALLET
    mocked.submitTopUp.mockResolvedValue(topup({ status: 'pending_verification', can_submit: false, can_cancel: false }))
    const res = await store.submitTopUp('t1', { payment_reference: '  987654  ', note: '   ' })
    expect(mocked.submitTopUp).toHaveBeenCalledWith('t1', { payment_reference: '987654' })
    expect(res.status).toBe('pending_verification')
    expect(store.wallet).toEqual(WALLET)
    expect(mocked.getWallet).not.toHaveBeenCalled()
  })

  it('keeps the list and the detail in step', async () => {
    const store = useWalletStore()
    mocked.listTopUps.mockResolvedValue({ items: [topup()], next_cursor: null })
    await store.loadTopUps(true)
    mocked.cancelTopUp.mockResolvedValue(topup({ status: 'expired', can_cancel: false, can_submit: false }))
    await store.cancelTopUp('t1')
    expect(store.topups.items[0]!.status).toBe('expired')
    expect(store.byId.t1!.status).toBe('expired')
  })

  it('topup.confirmed refreshes the wallet and the top-up', async () => {
    const store = useWalletStore()
    mocked.getTopUp.mockResolvedValueOnce(topup())
    await store.fetchTopUp('t1')
    mocked.getWallet.mockResolvedValue({ ...WALLET, balance: 3000, available: 3000 })
    mocked.getTopUp.mockResolvedValueOnce(topup({ status: 'confirmed', verified_amount: 2000 }))
    await store.onTopUpEvent('t1')
    expect(store.wallet?.balance).toBe(3000)
    expect(store.byId.t1!.status).toBe('confirmed')
  })

  it('pages the ledger with the cursor', async () => {
    const store = useWalletStore()
    const entry = (id: number) => ({
      id,
      entry_type: 'topup_credit' as const,
      balance_delta: 100,
      reserved_delta: 0,
      balance_after: 100 * id,
      reserved_after: 0,
      currency: 'KZT' as const,
      order_id: null,
      order_number: null,
      topup_id: null,
      topup_reference: null,
      description: null,
      created_at: '',
    })
    mocked.listTransactions.mockResolvedValueOnce({ items: [entry(3), entry(2)], next_cursor: '2' }).mockResolvedValueOnce({ items: [entry(1)], next_cursor: null })
    await store.loadLedger(true)
    await store.loadLedger(false)
    expect(mocked.listTransactions.mock.calls.map((c) => c[0])).toEqual([null, '2'])
    expect(store.ledger.items.map((e) => e.id)).toEqual([3, 2, 1])
    await store.loadLedger(false)
    expect(mocked.listTransactions).toHaveBeenCalledTimes(2)
  })

  it('a credited receipt refreshes the balance', async () => {
    const store = useWalletStore()
    mocked.uploadReceipt.mockResolvedValueOnce(
      topup({ status: 'confirmed', verified_amount: 2000, receipt: { kind: 'pdf', decision: 'auto_confirmed', reasons: [], receipt_serial: 'QR18149336524', amount: 2000, paid_at: null } }),
    )
    mocked.getWallet.mockResolvedValue({ ...WALLET, balance: 3000, available: 3000 })
    const t = await store.uploadReceipt('t1', new Blob(['%PDF-']), 'receipt_QR18149336524.pdf')
    expect(mocked.uploadReceipt.mock.calls[0]![2]).toBe('receipt_QR18149336524.pdf')
    expect(t.status).toBe('confirmed')
    expect(store.byId.t1!.receipt?.decision).toBe('auto_confirmed')
    await vi.waitFor(() => expect(store.wallet?.balance).toBe(3000))
  })

  it('a reused receipt re-reads the rejected top-up and rethrows', async () => {
    const store = useWalletStore()
    mocked.uploadReceipt.mockRejectedValueOnce(new ApiError(409, 'receipt_already_used', 'used', {}, { reason: 'duplicate_receipt' }))
    mocked.getTopUp.mockResolvedValueOnce(topup({ status: 'rejected', rejected_reason: 'duplicate_receipt' }))
    await expect(store.uploadReceipt('t1', new Blob(['%PDF-']), 'receipt.pdf')).rejects.toMatchObject({ code: 'receipt_already_used' })
    expect(store.byId.t1!.status).toBe('rejected')
    expect(store.byId.t1!.rejected_reason).toBe('duplicate_receipt')
  })
})
