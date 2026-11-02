// Broadcast editor checks: text in every language (line breaks kept, 3000
// characters), a button label and a start parameter the audience's Mini App
// knows, and the edit body that clears a segment, a campaign or the button.

import { describe, expect, it } from 'vitest'
import { NIL_UUID } from '../../api/marketing'
import { cleanMessage, deliveryStats, emptyBroadcastForm, validateBroadcastForm, type BroadcastForm } from './broadcastModel'

function filled(extra: Partial<BroadcastForm> = {}): BroadcastForm {
  return {
    ...emptyBroadcastForm(),
    title: 'November promo',
    content: { kk: 'Сәлем!\nЖаңа купон', ru: 'Привет!\nНовый купон', en: 'Hello!\nNew coupon' },
    ...extra,
  }
}

describe('validateBroadcastForm', () => {
  it('requires the message in every language and keeps line breaks', () => {
    const r = validateBroadcastForm(filled({ content: { kk: 'Сәлем', ru: '  ', en: '' } }), false)
    expect(r.errors).toEqual({ 'content.ru': 'required', 'content.en': 'required' })
    expect(r.payload).toBeNull()

    const ok = validateBroadcastForm(filled({ content: { kk: 'Бір\r\n\r\n\r\n\r\nЕкі', ru: 'Раз\nДва', en: ' One\nTwo ' } }), false)
    expect(ok.errors).toEqual({})
    expect(ok.payload?.content).toEqual({ kk: 'Бір\n\nЕкі', ru: 'Раз\nДва', en: 'One\nTwo' })
  })

  it('limits the text to 3000 characters and the title to 120', () => {
    const r = validateBroadcastForm(filled({ title: 'x'.repeat(121), content: { kk: 'ә'.repeat(3001), ru: 'да', en: 'yes' } }), false)
    expect(r.errors).toEqual({ title: 'too_long', 'content.kk': 'too_long' })
    expect(validateBroadcastForm(filled({ title: '' }), false).errors).toEqual({ title: 'required' })
  })

  it('accepts only the start parameters of the chosen audience', () => {
    const label = { kk: 'Ашу', ru: 'Открыть', en: 'Open' }
    const passenger = validateBroadcastForm(filled({ withButton: true, buttonLabel: label, startParam: 'coupons' }), false)
    expect(passenger.errors).toEqual({})
    expect(passenger.payload?.button).toEqual({ label, start_param: 'coupons' })

    expect(validateBroadcastForm(filled({ audience: 'drivers', withButton: true, buttonLabel: label, startParam: 'coupons' }), false).errors).toEqual({
      'button.start_param': 'invalid',
    })
    expect(validateBroadcastForm(filled({ audience: 'drivers', withButton: true, buttonLabel: label, startParam: 'wallet' }), false).errors).toEqual({})
    expect(validateBroadcastForm(filled({ withButton: true, buttonLabel: { kk: 'Ашу', ru: '', en: 'x'.repeat(41) }, startParam: '' }), false).errors).toEqual({
      'button.label.ru': 'required',
      'button.label.en': 'too_long',
      'button.start_param': 'required',
    })
  })

  it('needs a segment when targeting one and checks inline conditions otherwise', () => {
    expect(validateBroadcastForm(filled({ targeting: 'segment' }), false).errors).toEqual({ segment_id: 'required' })
    const f = filled()
    f.filters.completed_rides_min = '5'
    f.filters.completed_rides_max = '2'
    expect(validateBroadcastForm(f, false).errors).toEqual({ 'filters.completed_rides_max': 'below_min' })
  })

  it('creates without clearing anything and edits with explicit clears', () => {
    const created = validateBroadcastForm(filled(), false).payload!
    expect(created).toEqual({
      title: 'November promo',
      audience: 'passengers',
      content: { kk: 'Сәлем!\nЖаңа купон', ru: 'Привет!\nНовый купон', en: 'Hello!\nNew coupon' },
      filters: {},
    })
    const edited = validateBroadcastForm(filled(), true).payload!
    expect(edited).toMatchObject({ segment_id: NIL_UUID, campaign_id: NIL_UUID, no_button: true, filters: {} })
    const withSegment = validateBroadcastForm(filled({ targeting: 'segment', segmentId: 's1', campaignId: 'c1' }), true).payload!
    expect(withSegment).toMatchObject({ segment_id: 's1', filters: {}, campaign_id: 'c1' })
  })
})

describe('message helpers', () => {
  it('cleans text like the API and splits test deliveries from the rest', () => {
    expect(cleanMessage('\tA\r\nB\n\n\n\nC  ')).toBe('A\nB\n\nC')
    expect(deliveryStats({ sent: 10, failed: 1, test_sent: 2 })).toEqual({ live: { sent: 10, failed: 1 }, test: { sent: 2 } })
  })
})
