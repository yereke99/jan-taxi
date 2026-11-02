// Passenger promotion endpoints (§23.11): coupons, the bonus balance and its
// history, the invitation («Досыңды шақыр») and the marketing messages
// switch. The server decides and computes every discount; the app only shows
// what these answers say.

import { http } from '../services/http'
import { parseBonusPage, parsePromotions, parseReferral, rec } from './parse'
import type { BonusEntry, CursorPage, Promotions, ReferralView } from './types'

export const BONUS_PAGE_SIZE = 20

export async function getPromotions(signal?: AbortSignal): Promise<Promotions> {
  return parsePromotions(await http.get<unknown>('/promotions', { signal }))
}

export async function getReferral(signal?: AbortSignal): Promise<ReferralView> {
  return parseReferral(await http.get<unknown>('/promotions/referral', { signal }))
}

export async function listBonusHistory(opts: { before?: string | null; limit?: number; signal?: AbortSignal } = {}): Promise<CursorPage<BonusEntry>> {
  const raw = await http.get<unknown>('/promotions/bonus/history', {
    query: { limit: opts.limit ?? BONUS_PAGE_SIZE, before: opts.before ?? undefined },
    signal: opts.signal,
  })
  return parseBonusPage(raw)
}

/** Turns marketing messages on or off (order messages are not affected); resolves the saved choice. */
export async function setMarketingSubscription(subscribed: boolean): Promise<boolean> {
  const r = rec(await http.put<unknown>('/promotions/subscription', { subscribed }))
  return typeof r.marketing_subscribed === 'boolean' ? r.marketing_subscribed : subscribed
}
