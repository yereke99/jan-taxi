import { http } from '../services/http'
import type { LocalizedText } from './types'
import { num, rec, str, strOrNull } from './normalize'

export interface DriverOffer {
  id: string
  title: LocalizedText | null
  description: LocalizedText | null
  amount: number
  rides: number
  completed: number
  categories: string[]
  ends_at: string | null
  rewarded: boolean
}

export function parseDriverOffers(raw: unknown): DriverOffer[] {
  const items = rec(raw).offers
  if (!Array.isArray(items)) return []
  const localized = (v: unknown): LocalizedText | null => {
    const r = rec(v)
    return Object.keys(r).length ? { kk: str(r.kk), ru: str(r.ru), en: str(r.en) } : null
  }
  return items.flatMap((item) => {
    const r = rec(item)
    const amount = num(r.amount)
    const rides = num(r.rides)
    if (!str(r.id) || amount <= 0 || !Number.isInteger(amount) || rides < 1 || !Number.isInteger(rides)) return []
    return [{ id: str(r.id), title: localized(r.title), description: localized(r.description), amount, rides,
      completed: Math.max(0, Math.floor(num(r.completed))), categories: Array.isArray(r.categories) ? r.categories.filter((c): c is string => typeof c === 'string') : [],
      ends_at: strOrNull(r.ends_at), rewarded: r.rewarded === true }]
  })
}

export async function getDriverOffers(signal?: AbortSignal): Promise<DriverOffer[]> {
  return parseDriverOffers(await http.get<unknown>('/driver/promotions', { signal }))
}
