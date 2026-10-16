// Editable form state for GET/PATCH /admin/settings: each setting type maps
// to an input representation and back to the JSON value the API expects.

import type { LocalizedText } from '../../api/common'
import type { SettingItem } from '../../api/config'
import { parseInteger } from '../../forms/numbers'

export type Draft = string | boolean | LocalizedText | string[]

export const SETTING_GROUPS: ReadonlyArray<{ id: string; prefixes: string[] }> = [
  { id: 'kaspi', prefixes: ['kaspi.'] },
  { id: 'topup', prefixes: ['topup.'] },
  { id: 'driver', prefixes: ['driver.'] },
  { id: 'orders', prefixes: ['routing.', 'commission.'] },
  { id: 'delivery', prefixes: ['delivery.'] },
  { id: 'support', prefixes: ['support.'] },
  { id: 'navigation', prefixes: ['navigation.'] },
]

export function groupOf(key: string): string {
  return SETTING_GROUPS.find((g) => g.prefixes.some((p) => key.startsWith(p)))?.id ?? 'other'
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
