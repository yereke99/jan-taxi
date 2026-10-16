// Prohibited delivery items: the API sends codes (settings
// delivery.prohibited_items) and the app shows them in the UI language.
// Codes without a translation are summarised by one "other items" line.

export const KNOWN_PROHIBITED_ITEMS = [
  'weapons',
  'explosives',
  'drugs',
  'alcohol_tobacco',
  'money_valuables',
  'animals',
  'hazardous',
  'illegal_goods',
] as const

export type ProhibitedItem = (typeof KNOWN_PROHIBITED_ITEMS)[number]

/** Message keys to show for the configured codes, in their configured order. */
export function prohibitedItemKeys(codes: readonly string[]): string[] {
  const known = new Set<string>(KNOWN_PROHIBITED_ITEMS)
  const keys: string[] = []
  let unknown = false
  for (const c of codes) {
    if (known.has(c)) {
      const key = `prohibited.${c}`
      if (!keys.includes(key)) keys.push(key)
    } else unknown = true
  }
  if (unknown) keys.push('prohibited.other')
  return keys
}
