import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import type { Page } from '../services/api'
import { buildListQuery, parseListQuery } from './useListQuery'
import { usePagedList } from './usePagedList'

const DEFAULTS = { status: '', city_id: '', q: '' }

describe('parseListQuery / buildListQuery', () => {
  it('reads known filters and a valid page, ignoring the rest', () => {
    expect(parseListQuery({ status: 'searching', page: '3', other: 'x' }, DEFAULTS)).toEqual({ filters: { status: 'searching', city_id: '', q: '' }, page: 3 })
    expect(parseListQuery({ page: '-2' }, DEFAULTS).page).toBe(1)
    expect(parseListQuery({ page: 'abc' }, DEFAULTS).page).toBe(1)
  })

  it('omits defaults and page 1, keeps a cleared non-empty default', () => {
    expect(buildListQuery({ status: '', city_id: 'c1', q: '' }, 1, DEFAULTS)).toEqual({ city_id: 'c1' })
    expect(buildListQuery({ status: 'open' }, 2, { status: 'open' })).toEqual({ page: '2' })
    expect(buildListQuery({ status: '' }, 1, { status: 'open' })).toEqual({ status: '' })
  })
})

interface Row {
  id: string
}

function setup() {
  const load = vi.fn(async (f: typeof DEFAULTS, page: number, size: number): Promise<Page<Row>> => ({
    items: [{ id: `${f.status}-${page}` }],
    page,
    page_size: size,
    total: 120,
  }))
  let list!: ReturnType<typeof usePagedList<typeof DEFAULTS, Row>>
  const Screen = defineComponent({
    setup() {
      list = usePagedList(DEFAULTS, load)
      return () => h('div')
    },
  })
  const router: Router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/orders', name: 'orders', component: Screen },
      { path: '/other', name: 'other', component: { render: () => h('p') } },
    ],
  })
  return {
    router,
    load,
    App: Screen,
    get list() {
      return list
    },
  }
}

async function settle(): Promise<void> {
  await flushPromises()
  await nextTick()
  await flushPromises()
}

describe('usePagedList URL sync', () => {
  it('restores filters and page from the URL and requests them', async () => {
    const s = setup()
    await s.router.push('/orders?status=searching&page=2')
    mount(s.App, { global: { plugins: [s.router] } })
    await settle()
    expect(s.list.filters.status).toBe('searching')
    expect(s.list.page.value).toBe(2)
    expect(s.load).toHaveBeenLastCalledWith({ status: 'searching', city_id: '', q: '' }, 2, 25, expect.any(AbortSignal))
    expect(s.list.rows.value).toEqual([{ id: 'searching-2' }])
  })

  it('writes filter changes to the URL and returns to page 1', async () => {
    const s = setup()
    await s.router.push('/orders?page=3&tab=keep')
    mount(s.App, { global: { plugins: [s.router] } })
    await settle()
    expect(s.list.page.value).toBe(3)

    s.list.filters.status = 'completed'
    await settle()
    expect(s.list.page.value).toBe(1)
    expect(s.router.currentRoute.value.query).toEqual({ tab: 'keep', status: 'completed' })
    expect(s.load).toHaveBeenLastCalledWith({ status: 'completed', city_id: '', q: '' }, 1, 25, expect.any(AbortSignal))

    s.list.page.value = 2
    await settle()
    expect(s.router.currentRoute.value.query).toEqual({ tab: 'keep', status: 'completed', page: '2' })
  })

  it('follows Back/Forward navigation to another filter', async () => {
    const s = setup()
    await s.router.push('/orders')
    mount(s.App, { global: { plugins: [s.router] } })
    await settle()
    await s.router.push('/orders?status=cancelled&page=4')
    await settle()
    expect(s.list.filters.status).toBe('cancelled')
    expect(s.list.page.value).toBe(4)
    expect(s.load).toHaveBeenLastCalledWith({ status: 'cancelled', city_id: '', q: '' }, 4, 25, expect.any(AbortSignal))
  })

  it('reset clears filters and the page', async () => {
    const s = setup()
    await s.router.push('/orders?status=searching&q=1042&page=2')
    mount(s.App, { global: { plugins: [s.router] } })
    await settle()
    s.list.reset()
    await settle()
    expect(s.router.currentRoute.value.query).toEqual({})
    expect(s.list.filters).toEqual({ status: '', city_id: '', q: '' })
  })
})
