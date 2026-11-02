// Broadcast editor without Vue: form state, the same checks as
// marketing.saveBroadcast (text per language up to 3000 characters with line
// breaks kept, a button label up to 40 and a start parameter the audience's
// Mini App knows) and the request body for create or edit.

import type { LocalizedText } from '../../api/common'
import { NIL_UUID, START_PARAMS, type Audience, type Broadcast, type BroadcastInput } from '../../api/marketing'
import type { FieldErrors } from '../../forms/validation'
import { codePoints } from '../../utils/text'
import { emptyFiltersForm, filtersToForm, formToFilters, type FiltersForm } from './segmentModel'

export const BROADCAST_LIMITS = { title: 120, text: 3000, buttonLabel: 40 } as const

const LANGS = ['kk', 'ru', 'en'] as const

export interface BroadcastForm {
  title: string
  audience: Audience
  /** A saved segment, or filters of this broadcast only. */
  targeting: 'segment' | 'filters'
  segmentId: string
  filters: FiltersForm
  content: LocalizedText
  withButton: boolean
  buttonLabel: LocalizedText
  startParam: string
  campaignId: string
}

export function emptyBroadcastForm(): BroadcastForm {
  return {
    title: '',
    audience: 'passengers',
    targeting: 'filters',
    segmentId: '',
    filters: emptyFiltersForm(),
    content: { kk: '', ru: '', en: '' },
    withButton: false,
    buttonLabel: { kk: '', ru: '', en: '' },
    startParam: 'home',
    campaignId: '',
  }
}

export function broadcastToForm(b: Broadcast): BroadcastForm {
  return {
    title: b.title,
    audience: b.audience,
    targeting: b.segment_id ? 'segment' : 'filters',
    segmentId: b.segment_id ?? '',
    filters: filtersToForm(b.segment_id ? {} : b.filters),
    content: { ...b.content },
    withButton: b.button !== null,
    buttonLabel: b.button ? { ...b.button.label } : { kk: '', ru: '', en: '' },
    startParam: b.button?.start_param ?? 'home',
    campaignId: b.campaign_id ?? '',
  }
}

/** Message text as the API keeps it: CRLF → LF, at most two blank lines in a row, trimmed. */
export function cleanMessage(s: string): string {
  return s
    .replace(/\r\n?/g, '\n')
    .replace(/\t/g, ' ')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

/** Code points of the text the API will count. */
export function messageLength(s: string): number {
  return codePoints(cleanMessage(s))
}

function cleanLine(s: string): string {
  return s.replace(/\s+/g, ' ').trim()
}

/**
 * Field errors under the API's paths ("content.kk", "button.label.ru",
 * "button.start_param", "segment_id", "filters.…") and the request body.
 * An edit clears a segment or campaign with the nil UUID and removes a
 * button with no_button.
 */
export function validateBroadcastForm(form: BroadcastForm, editing: boolean): { errors: FieldErrors; payload: BroadcastInput | null } {
  const errors: FieldErrors = {}
  const title = cleanLine(form.title)
  if (!title) errors.title = 'required'
  else if (codePoints(title) > BROADCAST_LIMITS.title) errors.title = 'too_long'

  const content = { kk: cleanMessage(form.content.kk), ru: cleanMessage(form.content.ru), en: cleanMessage(form.content.en) }
  for (const l of LANGS) {
    if (!content[l]) errors[`content.${l}`] = 'required'
    else if (codePoints(content[l]) > BROADCAST_LIMITS.text) errors[`content.${l}`] = 'too_long'
  }

  const payload: BroadcastInput = { title, audience: form.audience, content }

  if (form.targeting === 'segment') {
    if (!form.segmentId) errors.segment_id = 'required'
    payload.segment_id = form.segmentId
    payload.filters = {}
  } else {
    const f = formToFilters(form.audience, form.filters)
    Object.assign(errors, f.errors)
    payload.filters = f.filters ?? {}
    if (editing) payload.segment_id = NIL_UUID
  }

  if (form.withButton) {
    const label = { kk: cleanLine(form.buttonLabel.kk), ru: cleanLine(form.buttonLabel.ru), en: cleanLine(form.buttonLabel.en) }
    for (const l of LANGS) {
      if (!label[l]) errors[`button.label.${l}`] = 'required'
      else if (codePoints(label[l]) > BROADCAST_LIMITS.buttonLabel) errors[`button.label.${l}`] = 'too_long'
    }
    if (!form.startParam) errors['button.start_param'] = 'required'
    else if (!START_PARAMS[form.audience].includes(form.startParam)) errors['button.start_param'] = 'invalid'
    payload.button = { label, start_param: form.startParam }
  } else if (editing) {
    payload.no_button = true
  }

  if (form.campaignId) payload.campaign_id = form.campaignId
  else if (editing) payload.campaign_id = NIL_UUID

  return { errors, payload: Object.keys(errors).length ? null : payload }
}

/** Delivery counts of a broadcast; test sends are kept apart (test_<status>). */
export function deliveryStats(stats: Record<string, number> | null | undefined): { live: Record<string, number>; test: Record<string, number> } {
  const live: Record<string, number> = {}
  const test: Record<string, number> = {}
  for (const [k, n] of Object.entries(stats ?? {})) {
    if (k.startsWith('test_')) test[k.slice(5)] = n
    else live[k] = n
  }
  return { live, test }
}
