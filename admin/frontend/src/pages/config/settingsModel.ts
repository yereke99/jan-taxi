// Editable form state for GET/PATCH /admin/settings: each setting type maps
// to an input representation and back to the JSON value the API expects.

import type { LocalizedText } from '../../api/common'
import type { SettingItem } from '../../api/config'
import { parseInteger } from '../../forms/numbers'

export type Draft = string | boolean | LocalizedText | string[]

/** Groups in display order; a key belongs to the group with its longest matching prefix. */
export const SETTING_GROUPS: ReadonlyArray<{ id: string; prefixes: string[] }> = [
  // Welcome bonus and commission (§22.7) have their own block at the top.
  { id: 'rules', prefixes: ['driver.welcome_bonus', 'commission.mode', 'commission.fixed_'] },
  { id: 'kaspi', prefixes: ['kaspi.'] },
  { id: 'company', prefixes: ['company.'] },
  { id: 'topup', prefixes: ['topup.'] },
  { id: 'topup_auto', prefixes: ['topup.auto_', 'topup.receipt_'] },
  { id: 'dispatch', prefixes: ['dispatch.'] },
  { id: 'driver', prefixes: ['driver.'] },
  { id: 'orders', prefixes: ['routing.', 'commission.', 'orders.'] },
  { id: 'delivery', prefixes: ['delivery.'] },
  { id: 'support', prefixes: ['support.'] },
  { id: 'navigation', prefixes: ['navigation.'] },
]

export function groupOf(key: string): string {
  let best = { id: 'other', len: 0 }
  for (const g of SETTING_GROUPS) {
    for (const p of g.prefixes) if (key.startsWith(p) && p.length > best.len) best = { id: g.id, len: p.length }
  }
  return best.id
}

/** Settings of the «Bonus and commission» block (§22.1, §22.2, §22.7). */
export const RULE_KEYS = {
  welcomeBonus: 'driver.welcome_bonus',
  mode: 'commission.mode',
  fixedAmount: 'commission.fixed_amount',
  fixedMin: 'commission.fixed_min',
  fixedMax: 'commission.fixed_max',
} as const

/** Default welcome bonus offered when the switch is turned back on (§22.1). */
export const DEFAULT_WELCOME_BONUS = 500

/**
 * The welcome bonus is off only when its amount is 0. An empty or unfinished
 * amount is still "on": the admin is typing a new value, so the field stays
 * on screen (and save reports it if it is left invalid).
 */
export function welcomeBonusOn(draft: string): boolean {
  return parseInteger(draft) !== 0
}

/**
 * Effective bounds of the fixed commission: the item's constraints (the API
 * reports the configured bounds there), else the read-only bound settings,
 * else 50..100 ₸.
 */
export function commissionBounds(items: SettingItem[]): { min: number; max: number } {
  const byKey = (k: string): SettingItem | undefined => items.find((i) => i.key === k)
  const c = byKey(RULE_KEYS.fixedAmount)?.constraints
  const num = (v: unknown, d: number): number => (typeof v === 'number' && Number.isFinite(v) ? v : d)
  return {
    min: num(c?.min, num(byKey(RULE_KEYS.fixedMin)?.value, 50)),
    max: num(c?.max, num(byKey(RULE_KEYS.fixedMax)?.value, 100)),
  }
}

/** Client copy of the API range rule for int settings: out_of_range when outside constraints. */
export function intOutOfRange(item: SettingItem, value: unknown): boolean {
  if (item.type !== 'int' || typeof value !== 'number') return false
  const { min, max } = item.constraints
  return (min !== undefined && value < min) || (max !== undefined && value > max)
}

/** Dispatch radius settings (ARCHITECTURE §7). */
export const DISPATCH_KEYS = {
  base: 'dispatch.radius_base_m',
  step: 'dispatch.radius_step_m',
  stepS: 'dispatch.radius_step_s',
  max: 'dispatch.radius_max_m',
} as const

export interface RadiusStep {
  /** Seconds since the order was created. */
  afterS: number
  radiusM: number
}

/**
 * Effective search radius over time:
 * min(max, base + step * floor(age / stepS)). Lists the radius at the start
 * and after each step until it reaches the maximum (at most `limit` rows);
 * empty when the values are unusable.
 */
export function radiusSchedule(base: number, step: number, stepS: number, max: number, limit = 6): RadiusStep[] {
  if (![base, step, stepS, max].every(Number.isFinite) || base <= 0 || stepS <= 0 || step < 0 || max < base) return []
  const out: RadiusStep[] = [{ afterS: 0, radiusM: Math.min(base, max) }]
  for (let i = 1; out.length < limit && step > 0 && out[out.length - 1]!.radiusM < max; i++) {
    out.push({ afterS: i * stepS, radiusM: Math.min(max, base + step * i) })
  }
  return out
}

function asText(v: unknown): LocalizedText {
  const o = v && typeof v === 'object' ? (v as Record<string, unknown>) : {}
  const s = (x: unknown): string => (typeof x === 'string' ? x : '')
  return { kk: s(o.kk), ru: s(o.ru), en: s(o.en) }
}

/** Input representation of a stored value. */
export function toDraft(item: SettingItem): Draft {
  const v = item.value
  switch (item.type) {
    case 'bool':
      return v === true
    case 'int':
      return typeof v === 'number' ? String(v) : ''
    case 'localized_text':
      return asText(v)
    case 'int_list':
      return Array.isArray(v) ? v.map(String).join(', ') : ''
    case 'code_list':
    case 'string_list':
      return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
    default:
      return typeof v === 'string' ? v : ''
  }
}

/** JSON value for the API; `invalid` when the input cannot be converted. */
export function fromDraft(item: SettingItem, draft: Draft): { value: unknown } | { invalid: string } {
  switch (item.type) {
    case 'bool':
      return { value: draft === true }
    case 'int': {
      const n = parseInteger(String(draft))
      return n === null ? { invalid: 'integer' } : { value: n }
    }
    case 'localized_text': {
      const t = draft as LocalizedText
      return { value: { kk: t.kk.trim(), ru: t.ru.trim(), en: t.en.trim() } }
    }
    case 'int_list': {
      const parts = String(draft)
        .split(/[,;\s]+/)
        .map((s) => s.trim())
        .filter(Boolean)
      const nums = parts.map((p) => parseInteger(p))
      return nums.some((n) => n === null) ? { invalid: 'integer' } : { value: nums }
    }
    case 'code_list':
    case 'string_list':
      return { value: (draft as string[]).map((s) => s.trim()).filter(Boolean) }
    default:
      return { value: String(draft).trim() }
  }
}

/** Stable comparison of two JSON values (object key order ignored). */
export function sameJSON(a: unknown, b: unknown): boolean {
  const norm = (v: unknown): unknown => {
    if (Array.isArray(v)) return v.map(norm)
    if (v && typeof v === 'object') return Object.fromEntries(Object.entries(v as Record<string, unknown>).sort(([x], [y]) => x.localeCompare(y)).map(([k, x]) => [k, norm(x)]))
    return v
  }
  return JSON.stringify(norm(a)) === JSON.stringify(norm(b))
}
