// List filters and pagination kept in the URL query (?status=…&page=2), so a
// filtered view can be bookmarked, shared and restored with Back/Forward.
// Changing any filter returns to page 1.

import { computed, reactive, ref, watch, type ComputedRef, type Ref } from 'vue'
import { useRoute, useRouter, type LocationQuery, type LocationQueryRaw } from 'vue-router'
import { DEFAULT_PAGE_SIZE } from '../services/api'

export type FilterValues = Record<string, string>

function first(v: LocationQuery[string] | undefined): string | undefined {
  if (Array.isArray(v)) return v[0] ?? undefined
  return v ?? undefined
}

/** Reads filters (unknown keys ignored, defaults filled in) and the page from a route query. */
export function parseListQuery<F extends FilterValues>(query: LocationQuery, defaults: F): { filters: F; page: number } {
  const filters = { ...defaults }
  for (const key of Object.keys(defaults) as Array<keyof F & string>) {
    const v = first(query[key])
    if (typeof v === 'string') filters[key] = v.slice(0, 200) as F[typeof key]
  }
  const n = Number(first(query.page))
  const page = Number.isInteger(n) && n >= 1 && n <= 100_000 ? n : 1
  return { filters, page }
}

/** Route query for filters and page: default values and page 1 are omitted. */
export function buildListQuery<F extends FilterValues>(filters: F, page: number, defaults: F): Record<string, string> {
  const out: Record<string, string> = {}
  for (const key of Object.keys(defaults)) {
    const v = filters[key] ?? ''
    if (v !== '' && v !== defaults[key]) out[key] = v
    // A non-empty default that the operator cleared is kept explicitly.
    else if (v === '' && defaults[key] !== '') out[key] = ''
  }
  if (page > 1) out.page = String(page)
  return out
}

function sameFilters<F extends FilterValues>(a: F, b: F): boolean {
  return Object.keys(a).every((k) => a[k] === b[k])
}

export interface ListQuery<F extends FilterValues> {
  filters: F
  page: Ref<number>
  pageSize: number
  /** Changes whenever the request must be repeated (filters or page). */
  key: ComputedRef<string>
  reset: () => void
}

export function useListQuery<F extends FilterValues>(defaults: F, opts: { pageSize?: number } = {}): ListQuery<F> {
  const route = useRoute()
  const router = useRouter()
  // The list belongs to this route; ignore query changes while navigating away.
  const routeName = route.name
  const initial = parseListQuery(route.query, defaults)
  const filters = reactive({ ...initial.filters }) as F
  const page = ref(initial.page)
  const snapshot = (): F => ({ ...(filters as F) })

  // Filter edits by the operator go back to page 1; values that arrive from
  // the URL (Back/Forward, links) keep the page from the URL.
  watch(snapshot, (next) => {
    const fromUrl = parseListQuery(route.query, defaults)
    if (!sameFilters(next, fromUrl.filters)) page.value = 1
  })

  const key = computed(() => JSON.stringify(buildListQuery(snapshot(), page.value, defaults)))

  watch(key, () => {
    if (route.name !== routeName) return
    const own = buildListQuery(snapshot(), page.value, defaults)
    const parsed = parseListQuery(route.query, defaults)
    const current = buildListQuery(parsed.filters, parsed.page, defaults)
    if (JSON.stringify(own) === JSON.stringify(current)) return
    // Keep unrelated query keys (e.g. an open tab) untouched.
    const rest: LocationQueryRaw = {}
    for (const [k, v] of Object.entries(route.query)) if (!(k in defaults) && k !== 'page') rest[k] = v
    void router.replace({ query: { ...rest, ...own } })
  })

  watch(
    () => route.query,
    (q) => {
      if (route.name !== routeName) return
      const parsed = parseListQuery(q, defaults)
      if (!sameFilters(parsed.filters, snapshot())) Object.assign(filters, parsed.filters)
      if (parsed.page !== page.value) page.value = parsed.page
    },
  )

  function reset(): void {
    Object.assign(filters, defaults)
    page.value = 1
  }

  return { filters, page, pageSize: opts.pageSize ?? DEFAULT_PAGE_SIZE, key, reset }
}
