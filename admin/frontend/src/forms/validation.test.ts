import { describe, expect, it } from 'vitest'
import { parseInteger, percentToBps } from './numbers'
import { validateAdjustment, validateCorrectionForm, validateTariffForm, validateTopUpConfirm, type TariffForm, type TopUpConfirmForm } from './validation'

describe('validateCorrectionForm', () => {
  it('requires at least one field', () => {
    expect(validateCorrectionForm({ items: [], comment: '' }).errors).toEqual({ items: 'too_few' })
  })

  it('requires a reason for every ticked field', () => {
    const r = validateCorrectionForm({ items: [{ field: 'vehicle_plate', reason: null, comment: '' }], comment: '' })
    expect(r.errors).toEqual({ 'items.0.reason': 'required' })
  })

  it('requires a comment when the reason is "other"', () => {
    const r = validateCorrectionForm({
      items: [
        { field: 'vehicle_plate', reason: 'plate_mismatch', comment: '' },
        { field: 'avatar', reason: 'other', comment: '   ' },
      ],
      comment: '',
    })
    expect(r.errors).toEqual({ 'items.1.comment': 'required' })
  })

  it('accepts "other" with a comment and builds the API items', () => {
    const r = validateCorrectionForm({
      items: [
        { field: 'vehicle_plate', reason: 'invalid_plate', comment: '' },
        { field: 'avatar', reason: 'other', comment: '  Бас киімсіз фото  қажет ' },
      ],
      comment: ' Рахмет ',
    })
    expect(r.errors).toEqual({})
    expect(r.items).toEqual([
      { field: 'vehicle_plate', reason: 'invalid_plate', comment: null },
      { field: 'avatar', reason: 'other', comment: 'Бас киімсіз фото қажет' },
    ])
    expect(r.comment).toBe('Рахмет')
  })

  it('limits comments by code points (§11)', () => {
    const long = 'ә'.repeat(201)
    const r = validateCorrectionForm({ items: [{ field: 'phone', reason: 'duplicate_phone', comment: long }], comment: 'ғ'.repeat(501) })
    expect(r.errors).toEqual({ 'items.0.comment': 'too_long', comment: 'too_long' })
  })
})

describe('validateTopUpConfirm', () => {
  const now = new Date(2026, 9, 16, 12, 0)
  const ok: TopUpConfirmForm = { verifiedAmount: '5 000', transactionId: ' qr 1234 5678 ', paidAt: '2026-10-16T11:45', payerInfo: '', note: '' }

  it('builds the payload with a normalised transaction number', () => {
    const r = validateTopUpConfirm(ok, now)
    expect(r.errors).toEqual({})
    expect(r.payload).toEqual({ verified_amount: 5000, provider_transaction_id: 'QR12345678', paid_at: new Date(2026, 9, 16, 11, 45).toISOString() })
  })

  it('requires the Kaspi transaction number', () => {
    const r = validateTopUpConfirm({ ...ok, transactionId: '   ' }, now)
    expect(r.errors.provider_transaction_id).toBe('required')
    expect(r.payload).toBeNull()
  })

  it('requires an amount above zero', () => {
    expect(validateTopUpConfirm({ ...ok, verifiedAmount: '0' }, now).errors.verified_amount).toBe('positive')
    expect(validateTopUpConfirm({ ...ok, verifiedAmount: '-100' }, now).errors.verified_amount).toBe('positive')
    expect(validateTopUpConfirm({ ...ok, verifiedAmount: '' }, now).errors.verified_amount).toBe('required')
    expect(validateTopUpConfirm({ ...ok, verifiedAmount: '12.5' }, now).errors.verified_amount).toBe('integer')
    expect(validateTopUpConfirm({ ...ok, verifiedAmount: '10000001' }, now).errors.verified_amount).toBe('too_large')
  })

  it('requires the payment time and rejects one in the future', () => {
    expect(validateTopUpConfirm({ ...ok, paidAt: '' }, now).errors.paid_at).toBe('required')
    expect(validateTopUpConfirm({ ...ok, paidAt: '2026-10-16T13:00' }, now).errors.paid_at).toBe('in_future')
    expect(validateTopUpConfirm({ ...ok, paidAt: '2026-10-16T12:05' }, now).errors.paid_at).toBeUndefined()
  })
})

describe('validateTariffForm', () => {
  const base: TariffForm = {
    city_id: 'c1',
    category: 'taxi',
    min_fare: '500',
    max_fare: '1500',
    fare_step: '50',
    base_fare: '300',
    per_km: '100',
    per_minute: '0',
    extra_stop_fee: '200',
    commission_percent: '10',
    extra_fees: [],
  }

  it('builds the API body with commission in basis points', () => {
    const r = validateTariffForm({ ...base, commission_percent: '12,5' })
    expect(r.errors).toEqual({})
    expect(r.payload).toMatchObject({ min_fare: 500, max_fare: 1500, fare_step: 50, base_fare: 300, per_km: 100, commission_bps: 1250, extra_fees: [] })
  })

  it('requires min and max to be multiples of the step', () => {
    const r = validateTariffForm({ ...base, min_fare: '520', max_fare: '1510' })
    expect(r.errors).toEqual({ min_fare: 'not_multiple_of_step', max_fare: 'not_multiple_of_step' })
  })

  it('rejects max below min and too many fare options', () => {
    expect(validateTariffForm({ ...base, max_fare: '450' }).errors).toEqual({ max_fare: 'less_than_min' })
    expect(validateTariffForm({ ...base, fare_step: '1', min_fare: '1', max_fare: '1000' }).errors).toEqual({ fare_step: 'too_small' })
  })

  it('checks required amounts and the commission range', () => {
    expect(validateTariffForm({ ...base, fare_step: '' }).errors.fare_step).toBe('required')
    expect(validateTariffForm({ ...base, commission_percent: '51' }).errors.commission_bps).toBe('too_large')
    expect(validateTariffForm({ ...base, per_km: '-1' }).errors.per_km).toBe('too_small')
  })

  it('validates extra fees', () => {
    const r = validateTariffForm({
      ...base,
      extra_fees: [
        { code: 'luggage', nameKk: 'Жүк', nameRu: 'Багаж', nameEn: 'Luggage', amount: '200' },
        { code: 'luggage', nameKk: '', nameRu: 'Детское кресло', nameEn: 'Child seat', amount: '0' },
        { code: 'Bad Code', nameKk: 'Ит', nameRu: 'Собака', nameEn: 'Dog', amount: '100' },
      ],
    })
    expect(r.errors).toEqual({
      'extra_fees.1.code': 'duplicate',
      'extra_fees.1.name.kk': 'required',
      'extra_fees.1.amount': 'too_small',
      'extra_fees.2.code': 'invalid',
    })
  })
})

describe('validateAdjustment', () => {
  it('signs the amount by direction and requires a reason', () => {
    expect(validateAdjustment({ direction: 'debit', amount: '1 500', reason: 'Қате толтыру' })).toMatchObject({ errors: {}, amount: -1500 })
    expect(validateAdjustment({ direction: 'credit', amount: '0', reason: '' }).errors).toEqual({ amount: 'positive', reason: 'required' })
  })
})

describe('number parsing', () => {
  it('parses grouped integers and percents', () => {
    expect(parseInteger('1 500')).toBe(1500)
    expect(parseInteger('1 500')).toBe(1500)
    expect(parseInteger('1.5')).toBeNull()
    expect(percentToBps('10')).toBe(1000)
    expect(percentToBps('0.01')).toBe(1)
  })
})
