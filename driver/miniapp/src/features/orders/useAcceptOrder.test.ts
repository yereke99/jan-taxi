// One-tap acceptance (cards and order screen): success opens the trip and
// drops the card, an order someone else took is dropped with a toast, a lost
// response is checked against the active trip, and only one acceptance runs
// at a time.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import * as api from '../../api/orders'
import { normalizeDetail } from '../../api/orders'
import { toast } from '../../components/ui'
import { i18n } from '../../i18n'
import { ROUTE } from '../../router/names'
import { ApiError } from '../../services/http'
import { useOrdersStore } from '../../stores/orders'
import { useAcceptOrder, type AcceptOrder } from './useAcceptOrder'

vi.mock('../../api/orders', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../api/orders')>()
  return { ...real, acceptOrder: vi.fn(), getActive: vi.fn(), getOrder: vi.fn(), listAvailable: vi.fn() }
})
vi.mock('../../api/wallet', () => ({
  getWallet: vi.fn().mockResolvedValue({ balance: 500, reserved: 0, available: 500, currency: 'KZT', min_online_balance: 0, updated_at: '' }),
}))

const mocked = vi.mocked(api)
const ID = '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d34'

function detail(status: string) {
  return normalizeDetail({
    id: ID, number: 1024, status, is_mine: status !== 'searching', category: 'taxi', vehicle_class: 'sedan',
    vehicle_class_name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' }, passenger_count: 1, cargo_size: null, cargo_volume_l: 0,
    fare: 400, commission_amount: 50, commission_bps: 0, payment_method: 'cash', distance_m: 3200, duration_s: 480,
    distance_to_pickup_m: 20, stops: [], stop_count: 2, route: null, passenger: null, events: [], can_accept: status === 'searching',
    locality: null, tariff_region: null,
  })
}

async function setup() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/orders', name: ROUTE.orders, component: { template: '<div />' } },
      { path: '/trip', name: ROUTE.trip, component: { template: '<div />' } },
      { path: '/home', name: ROUTE.home, component: { template: '<div />' } },
      { path: '/wallet/topup', name: ROUTE.topup, component: { template: '<div />' } },
    ],
  })
  await router.push('/orders')
  await router.isReady()
  let acceptor!: AcceptOrder
  const Host = defineComponent({
    setup() {
      acceptor = useAcceptOrder()
      return () => h('div')
    },
  })
  mount(Host, { global: { plugins: [i18n, router] } })
  return { router, acceptor: () => acceptor }
}

describe('useAcceptOrder', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    i18n.global.locale.value = 'kk'
    vi.clearAllMocks()
  })

  it('takes the order: the trip opens and the card goes', async () => {
    mocked.acceptOrder.mockResolvedValue(detail('accepted'))
    const { router, acceptor } = await setup()
    const orders = useOrdersStore()
    const remove = vi.spyOn(orders, 'remove')
    await expect(acceptor().accept(ID)).resolves.toBe(true)
    await flushPromises()
    expect(mocked.acceptOrder).toHaveBeenCalledTimes(1)
    expect(remove).toHaveBeenCalledWith(ID)
    expect(router.currentRoute.value.name).toBe(ROUTE.trip)
  })

  it('another driver was faster: the card goes with a calm toast', async () => {
    mocked.acceptOrder.mockRejectedValue(new ApiError(409, 'order_already_taken', 'taken'))
    const { router, acceptor } = await setup()
    const orders = useOrdersStore()
    const remove = vi.spyOn(orders, 'remove')
    const show = vi.spyOn(toast, 'show')
    const onGone = vi.fn()
    await expect(acceptor().accept(ID, { onGone })).resolves.toBe(false)
    expect(remove).toHaveBeenCalledWith(ID)
    expect(onGone).toHaveBeenCalled()
    expect(show).toHaveBeenCalledWith('Тапсырысты басқа жүргізуші қабылдады')
    expect(router.currentRoute.value.name).toBe(ROUTE.orders)
  })

  it('a lost response that did take the order counts as taken by this driver', async () => {
    mocked.acceptOrder.mockRejectedValue(new ApiError(0, 'network_error', 'network'))
    mocked.getActive.mockResolvedValue(detail('accepted'))
    const { router, acceptor } = await setup()
    await expect(acceptor().accept(ID)).resolves.toBe(true)
    await flushPromises()
    expect(mocked.getActive).toHaveBeenCalled()
    expect(router.currentRoute.value.name).toBe(ROUTE.trip)
  })

  it('runs one acceptance at a time', async () => {
    let finish!: (v: ReturnType<typeof detail>) => void
    mocked.acceptOrder.mockReturnValue(new Promise((resolve) => (finish = resolve)))
    const { acceptor } = await setup()
    const first = acceptor().accept(ID)
    expect(acceptor().busy.value).toBe(true)
    expect(acceptor().acceptingId.value).toBe(ID)
    await expect(acceptor().accept('11111111-2222-4333-8444-555555555555')).resolves.toBe(false)
    finish(detail('accepted'))
    await expect(first).resolves.toBe(true)
    expect(mocked.acceptOrder).toHaveBeenCalledTimes(1)
    expect(acceptor().acceptingId.value).toBeNull()
  })

  it('a 402 goes to the page hook instead of the default toast', async () => {
    mocked.acceptOrder.mockRejectedValue(
      new ApiError(402, 'insufficient_balance', 'low', {}, { required: 50, available: 20, currency: 'KZT', topup_min_amount: 500 }),
    )
    const { acceptor } = await setup()
    const onInsufficient = vi.fn()
    await expect(acceptor().accept(ID, { onInsufficient })).resolves.toBe(false)
    expect(onInsufficient).toHaveBeenCalledWith({ kind: 'insufficient_balance', required: 50, available: 20, suggestedTopUp: 500 })
  })
})
