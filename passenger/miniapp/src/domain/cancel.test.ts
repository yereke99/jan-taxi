import { describe, expect, it } from 'vitest'
import { CANCEL_REASON_CODES } from '../api/types'
import { i18n } from '../i18n'
import { CANCEL_COMMENT_MAX, cancelPayload, cancelReasonOptions, validateCancel } from './cancel'

const fallback = (code: string) => `label:${code}`

describe('passenger cancellation (§22.3)', () => {
  it('offers the five reasons with the app labels when the config has none', () => {
    const list = cancelReasonOptions([], 'kk', fallback)
    expect(list.map((r) => r.code)).toEqual([...CANCEL_REASON_CODES])
    expect(list.find((r) => r.code === 'other')?.commentRequired).toBe(true)
    expect(list.filter((r) => r.commentRequired)).toHaveLength(1)
    expect(list[0]!.label).toBe('label:long_wait')
  })

  it("uses the config's order and labels and drops codes the API does not accept", () => {
    const list = cancelReasonOptions(
      [
        { code: 'changed_plans', label: { kk: 'Жоспарым өзгерді', ru: 'Планы изменились', en: 'My plans changed' }, comment_required: false },
        { code: 'teleported', label: { kk: 'x', ru: 'x', en: 'x' }, comment_required: false },
        { code: 'other', label: { kk: 'Басқа себеп', ru: 'Другое', en: 'Other' }, comment_required: false },
      ],
      'ru',
      fallback,
    )
    expect(list).toEqual([
      { code: 'changed_plans', label: 'Планы изменились', commentRequired: false },
      // "other" always needs a comment.
      { code: 'other', label: 'Другое', commentRequired: true },
    ])
  })

  it('requires a reason, a comment for "other" and at most 120 code points', () => {
    const [longWait, , , , other] = cancelReasonOptions([], 'kk', fallback)
    expect(validateCancel(null, '')).toEqual({ reason_code: 'required' })
    expect(validateCancel(longWait!, '')).toEqual({})
    expect(validateCancel(other!, '   ')).toEqual({ comment: 'required' })
    expect(validateCancel(other!, 'Басқа көлік таптым')).toEqual({})
    expect(validateCancel(longWait!, 'ә'.repeat(CANCEL_COMMENT_MAX))).toEqual({})
    expect(validateCancel(longWait!, 'ә'.repeat(CANCEL_COMMENT_MAX + 1))).toEqual({ comment: 'too_long' })
  })

  it('sends the cleaned comment only when there is one', () => {
    const [longWait, , , , other] = cancelReasonOptions([], 'kk', fallback)
    expect(cancelPayload(longWait!, '   ', 'searching')).toEqual({ reason_code: 'long_wait', expected_status: 'searching' })
    expect(cancelPayload(other!, '  Басқа\n көлік  ', 'accepted')).toEqual({
      reason_code: 'other',
      comment: 'Басқа көлік',
      expected_status: 'accepted',
    })
  })

  it('names the status on screen, so a driver who took the order meanwhile is not charged for a search cancel', () => {
    const [longWait] = cancelReasonOptions([], 'kk', fallback)
    expect(cancelPayload(longWait!, '', 'searching').expected_status).toBe('searching')
    expect(cancelPayload(longWait!, '', 'driver_arriving').expected_status).toBe('driver_arriving')
  })

  it('has a label for every reason in every language', () => {
    for (const locale of ['kk', 'ru', 'en'] as const) {
      for (const code of CANCEL_REASON_CODES) expect(i18n.global.te(`cancel.reason.${code}`, locale)).toBe(true)
    }
  })
})
