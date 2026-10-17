import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AdminReceipt, AdminTopUpDetail } from '../../api/finance'
import { toLocalDateTimeInput } from '../../forms/validation'
import { i18n } from '../../i18n'
import { api } from '../../services/api'
import ReceiptCard from './ReceiptCard.vue'
import TopUpVerifyForm from './TopUpVerifyForm.vue'

const receipt: AdminReceipt = {
  id: 'r1',
  provider: 'kaspi_pdf',
  file_id: 'f1',
  file_url: '/api/v1/admin/files/f1',
  file_sha256: 'a'.repeat(64),
  receipt_serial: 'QR90000000102',
  qr_payload_hash: null,
  amount: 500,
  paid_at: '2026-10-17T17:52:00Z',
  recipient_name: 'ИП "JAN-TAXI"',
  recipient_bin: '000809500289',
  payer_masked: 'Т*** Т.',
  payment_method: 'с Kaspi Gold',
  producer: 'WeasyPrint 70.0',
  parse_status: 'parsed',
  flags: ['qr_not_found', 'moddate_present'],
  decision: 'manual_review',
  reasons: ['amount_mismatch', 'tamper_suspected'],
  created_at: '2026-10-17T17:53:00Z',
}

function topUp(over: Partial<AdminTopUpDetail> = {}): AdminTopUpDetail {
  return {
    id: 't1',
    number: 3,
    reference: 'EMB014-TOPUP-000003',
    status: 'pending_verification',
    provider: 'kaspi_manual',
    amount: 700,
    verified_amount: null,
    currency: 'KZT',
    driver: { id: 'd1', code: 'EMB014', first_name: 'Тест', last_name: 'Тестов', phone: null },
    payment_url: null,
    driver_payment_reference: null,
    driver_note: null,
    has_receipt: true,
    receipt_url: '/api/v1/admin/topups/t1/receipt',
    driver_reported_paid_at: null,
    provider_transaction_id: null,
    paid_at: null,
    confirmed_by: null,
    confirmed_automatically: false,
    confirmed_at: null,
    rejected_by: null,
    rejected_at: null,
    rejected_reason: null,
    refunded_by: null,
    refunded_at: null,
    refund_reason: null,
    expires_at: '2026-10-19T17:52:00Z',
    created_at: '2026-10-17T17:52:00Z',
    updated_at: '2026-10-17T17:53:00Z',
    events: [],
    payment_transaction: null,
    duplicate_warnings: [],
    receipt,
    receipts: [receipt],
    confirm_prefill: { verified_amount: 500, provider_transaction_id: 'QR90000000102', paid_at: '2026-10-17T17:52:00Z', payer_info: 'Т*** Т.' },
    ...over,
  }
}

const global = { plugins: [i18n], stubs: { RouterLink: true } }

beforeEach(() => {
  i18n.global.locale.value = 'en'
  URL.createObjectURL = vi.fn(() => 'blob:receipt')
  URL.revokeObjectURL = vi.fn()
})

afterEach(() => {
  i18n.global.locale.value = 'kk'
})

describe('ReceiptCard', () => {
  it('shows the parsed receipt, why it was not credited, the file checks and the PDF inline', async () => {
    const blob = vi.spyOn(api, 'blob').mockResolvedValue({ blob: new Blob(['%PDF-1.7'], { type: 'application/pdf' }), contentType: 'application/pdf', filename: null })
    const wrapper = mount(ReceiptCard, { props: { topup: topUp() }, global })
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('QR90000000102')
    expect(text).toContain('Left for admin review')
    expect(text).toContain('differs from the requested')
    expect(text).toContain('The amount differs from the requested amount')
    expect(text).toContain('The file may have been edited')
    expect(text).toContain('Modification date present (ModDate)')
    expect(text).toContain('No QR code (normal for Kaspi receipts)')
    expect(wrapper.findAll('.receipt__item--danger')).toHaveLength(1)
    expect(wrapper.findAll('.receipt__item--info')).toHaveLength(1)

    // Fetched with the session (API client), shown from a blob: URL.
    expect(blob).toHaveBeenCalledWith('/admin/topups/t1/receipt', expect.anything())
    expect(wrapper.find('iframe').attributes('src')).toMatch(/^blob:receipt#/)
  })

  it('explains an image receipt instead of parsed fields', async () => {
    vi.spyOn(api, 'blob').mockResolvedValue({ blob: new Blob(['x'], { type: 'image/jpeg' }), contentType: 'image/jpeg', filename: null })
    const wrapper = mount(ReceiptCard, { props: { topup: topUp({ receipt: null, receipts: [], confirm_prefill: null }) }, global })
    await flushPromises()
    expect(wrapper.text()).toContain('The driver sent an image')
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.find('.doc__image img').exists()).toBe(true)
  })
})

describe('TopUpVerifyForm', () => {
  it('starts from the receipt values (confirm_prefill)', () => {
    const wrapper = mount(TopUpVerifyForm, { props: { topup: topUp() }, global })
    const values = wrapper.findAll('input').map((i) => (i.element as HTMLInputElement).value)
    expect(values).toEqual(['500', 'QR90000000102', toLocalDateTimeInput('2026-10-17T17:52:00Z'), 'Т*** Т.'])
    expect(wrapper.text()).toContain('Fields are filled from the receipt')
  })

  it('starts from the requested amount without a receipt', () => {
    const wrapper = mount(TopUpVerifyForm, { props: { topup: topUp({ receipt: null, receipts: [], confirm_prefill: null }) }, global })
    const values = wrapper.findAll('input').map((i) => (i.element as HTMLInputElement).value)
    expect(values).toEqual(['700', '', '', ''])
  })
})

describe('toLocalDateTimeInput', () => {
  it('formats an instant as a local datetime-local value', () => {
    const d = new Date(2026, 9, 17, 22, 52)
    expect(toLocalDateTimeInput(d.toISOString())).toBe('2026-10-17T22:52')
    expect(toLocalDateTimeInput('nope')).toBe('')
    expect(toLocalDateTimeInput(null)).toBe('')
  })
})
