// Texts about an order's promotion (§23.6): the name of what took money off
// the fare, the compact breakdown under the price the passenger pays
// ("1 500 ₸ · Купон −400 ₸"), the name of that price while the order runs and
// once the ride is done, and the note that a cancelled order gave its coupon
// or bonus back.

import { useI18n } from 'vue-i18n'
import type { OrderPromoAmounts, OrderPromotionStatus, PromotionKind } from '../api/types'
import { hasPromo } from '../domain/promotions'
import { normalizeLocale } from '../i18n/core'
import { formatMoney } from '../utils/format'

/** Order amounts with the promotion's kind when the view knows it (a list row does not). */
export type PromoBreakdownSource = OrderPromoAmounts & { fare: number; status?: string; promotion?: { kind: PromotionKind } | null }

/** An order with the state of its promotion (the order detail views). */
export interface PromoStateSource {
  status: string
  promotion: { kind: PromotionKind; status: OrderPromotionStatus } | null
}

export function usePromoText() {
  const { t, locale } = useI18n()

  /** «Купон», «Бонус», or «Жеңілдік» when the kind is unknown. */
  function kindLabel(kind: PromotionKind | null | undefined): string {
    return kind === 'coupon' ? t('promo.coupon') : kind === 'bonus' ? t('promo.bonus') : t('promo.discount')
  }

  /** "1 500 ₸ · Купон −400 ₸": the fare and what the promotion took off; '' without one. */
  function breakdown(o: PromoBreakdownSource): string {
    if (!hasPromo(o)) return ''
    const lang = normalizeLocale(locale.value)
    return t('promo.breakdown', {
      fare: formatMoney(o.fare, lang),
      kind: kindLabel(o.promotion?.kind),
      discount: formatMoney(-o.promo_discount, lang),
    })
  }

  /** «Төленді» once the ride is done (completed, or disputed after it), «Төлейсіз» while it runs. */
  function payLabel(status: string): string {
    return status === 'completed' || status === 'disputed' ? t('promo.paid') : t('promo.you_pay')
  }

  /** A cancelled order gave its coupon or bonus back: what the passenger is told; '' otherwise. */
  function returned(o: PromoStateSource): string {
    const p = o.promotion
    if (o.status !== 'cancelled' || p?.status !== 'released') return ''
    return p.kind === 'coupon' ? t('promo.returned_coupon') : t('promo.returned_bonus')
  }

  return { kindLabel, breakdown, payLabel, returned }
}
