import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { i18n } from '../i18n'
import DataTable from './DataTable.vue'
import Pagination from './Pagination.vue'
import type { Column } from './types'

interface Row {
  id: string
  code: string
  fare: number
}

const rows: Row[] = [
  { id: 'a', code: 'EMB002', fare: 1500 },
  { id: 'b', code: 'EMB010', fare: 800 },
  { id: 'c', code: 'EMB001', fare: 2000 },
]

const columns: Column<Row>[] = [
  { key: 'code', label: 'Code', sortable: true },
  { key: 'fare', label: 'Fare', sortable: true, align: 'right', format: (v) => `${String(v)} ₸` },
]

const global = { plugins: [i18n] }

function cellTexts(wrapper: ReturnType<typeof mount>, col: number): string[] {
  return wrapper.findAll('tbody tr').map((tr) => tr.findAll('td')[col]!.text())
}

describe('DataTable', () => {
  it('renders formatted cells and sorts client-side by column value', async () => {
    const wrapper = mount(DataTable<Row>, {
      props: { columns, rows, rowKey: 'id', sortMode: 'client', sort: { key: 'fare', dir: 'asc' } },
      global,
    })
    expect(cellTexts(wrapper, 1)).toEqual(['800 ₸', '1500 ₸', '2000 ₸'])
    await wrapper.setProps({ sort: { key: 'code', dir: 'desc' } })
    expect(cellTexts(wrapper, 0)).toEqual(['EMB010', 'EMB002', 'EMB001'])
    expect(wrapper.findAll('th')[0]!.attributes('aria-sort')).toBe('descending')
  })

  it('cycles sort asc → desc → none through update:sort', async () => {
    const wrapper = mount(DataTable<Row>, { props: { columns, rows, rowKey: 'id', sort: null }, global })
    await wrapper.find('th button').trigger('click')
    expect(wrapper.emitted('update:sort')?.[0]).toEqual([{ key: 'code', dir: 'asc' }])
    await wrapper.setProps({ sort: { key: 'code', dir: 'asc' } })
    await wrapper.find('th button').trigger('click')
    expect(wrapper.emitted('update:sort')?.[1]).toEqual([{ key: 'code', dir: 'desc' }])
    await wrapper.setProps({ sort: { key: 'code', dir: 'desc' } })
    await wrapper.find('th button').trigger('click')
    expect(wrapper.emitted('update:sort')?.[2]).toEqual([null])
  })

  it('emits row-click for clickable rows and shows the empty state', async () => {
    const wrapper = mount(DataTable<Row>, { props: { columns, rows, rowKey: 'id', clickable: true }, global })
    await wrapper.findAll('tbody tr')[1]!.trigger('click')
    expect(wrapper.emitted('row-click')?.[0]?.[0]).toEqual(rows[1])

    await wrapper.setProps({ rows: [], emptyText: 'Nothing here' })
    expect(wrapper.find('.table-state').text()).toBe('Nothing here')
  })
})

describe('Pagination', () => {
  it('shows the range and emits page changes within bounds', async () => {
    const wrapper = mount(Pagination, { props: { page: 1, pageSize: 25, total: 123 }, global })
    expect(wrapper.find('.pager__range').text()).toContain('1–25')
    const buttons = wrapper.findAll('button')
    expect(buttons[0]!.attributes('disabled')).toBeDefined()
    await buttons.at(-1)!.trigger('click')
    expect(wrapper.emitted('update:page')?.[0]).toEqual([2])
    expect(wrapper.text()).toContain('5')
  })
})
