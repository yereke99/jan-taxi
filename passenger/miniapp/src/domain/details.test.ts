import { describe, expect, it } from 'vitest'
import { detailsPayload, validateDetails, type DetailsInput } from './details'

const base: DetailsInput = { category: 'taxi', comment: '', cargoDescription: '', recipientPhone: '', prohibitedItemsAck: false }

// 120 Kazakh letters with the specific glyphs (ә ғ қ ң ө ұ ү һ і), counted as code points.
const KK_120 = 'әғқңөұүһі'.repeat(13) + 'әғқ'

describe('comment counter (same rule as the backend)', () => {
  it('accepts 120 Kazakh code points and blocks 121', () => {
    expect([...KK_120].length).toBe(120)
    expect(validateDetails({ ...base, comment: KK_120 })).toEqual({})
    expect(validateDetails({ ...base, comment: `${KK_120}ә` })).toEqual({ comment: 'too_long' })
  })

  it('counts after NFC and cleaning (decomposed letters, spaces, newlines)', () => {
    // "й" typed as "и" + U+0306 is two code points before NFC and one after.
    const decomposed = '\u0438\u0306'.repeat(120)
    expect(validateDetails({ ...base, comment: decomposed })).toEqual({})
    expect(validateDetails({ ...base, comment: `  ${KK_120}\n\n  ` })).toEqual({})
  })

  it('applies the limit to the cargo description too', () => {
    const parcel: DetailsInput = { ...base, category: 'other', prohibitedItemsAck: true, cargoDescription: `${KK_120}і` }
    expect(validateDetails(parcel)).toEqual({ cargo_description: 'too_long' })
  })
})

describe('category rules', () => {
  it('taxi needs nothing and sends no delivery fields', () => {
    expect(validateDetails(base)).toEqual({})
    expect(detailsPayload({ ...base, comment: '  екінші   подъезд ' })).toEqual({ comment: 'екінші подъезд' })
  })

  it('parcel requires a description, a KZ phone and the acknowledgment', () => {
    const p: DetailsInput = { ...base, category: 'parcel' }
    expect(validateDetails(p)).toEqual({
      cargo_description: 'required',
      recipient_phone: 'required',
      prohibited_items_ack: 'must_be_true',
    })
    expect(validateDetails({ ...p, cargoDescription: 'Құжаттар', recipientPhone: '+7 123', prohibitedItemsAck: true })).toEqual({
      recipient_phone: 'invalid_phone',
    })
    const ok: DetailsInput = { ...p, cargoDescription: 'Құжаттар', recipientPhone: '8 701 234 56 78', prohibitedItemsAck: true }
    expect(validateDetails(ok)).toEqual({})
    expect(detailsPayload(ok)).toEqual({ cargo_description: 'Құжаттар', recipient_phone: '+77012345678', prohibited_items_ack: true })
  })

  it('other requires a description and the acknowledgment, never a phone', () => {
    const o: DetailsInput = { ...base, category: 'other', cargoDescription: 'Екі жәшік', prohibitedItemsAck: true, recipientPhone: '+77012345678' }
    expect(validateDetails(o)).toEqual({})
    expect(detailsPayload(o)).toEqual({ cargo_description: 'Екі жәшік', prohibited_items_ack: true })
  })
})
