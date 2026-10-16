// An admin list screen: filters and page in the URL (useListQuery), the
// request with cancellation (useResource), reloaded whenever either changes.

import { computed, watch } from 'vue'
import type { Page } from '../services/api'
import { useListQuery, type FilterValues } from './useListQuery'
import { useResource } from './useResource'

export function usePagedList<F extends FilterValues, T>(
  defaults: F,
  load: (filters: F, page: number, pageSize: number, signal: AbortSignal) => Promise<Page<T>>,
  opts: { pageSize?: number } = {},
) {
  const query = useListQuery(defaults, opts)
  const res = useResource((signal) => load({ ...(query.filters as F) }, query.page.value, query.pageSize, signal))

  watch(query.key, () => void res.reload(), { immediate: true })

  const rows = computed<T[]>(() => res.data.value?.items ?? [])
  const total = computed(() => res.data.value?.total ?? 0)

  // After rows disappear (e.g. resolved items leave a filtered list) step back
  // to the last page that still has rows.
  watch(total, (n) => {
    const last = Math.max(1, Math.ceil(n / query.pageSize))
    if (res.data.value && query.page.value > last) query.page.value = last
  })

  return {
    filters: query.filters,
    page: query.page,
    pageSize: query.pageSize,
    key: query.key,
    reset: query.reset,
    rows,
    total,
    loading: res.loading,
    error: res.error,
    reload: res.reload,
  }
}
