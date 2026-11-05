import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { getDriverOffers, type DriverOffer } from '../api/promotions'
import DriverPromoGift from '../components/driver/DriverPromoGift.vue'
import { i18n } from '../i18n'
import { ROUTE } from '../router/names'

vi.mock('../api/promotions', () => ({ getDriverOffers: vi.fn() }))
vi.mock('../telegram', () => ({ useTelegramFullscreen: () => ({ state: { isActive: true } }) }))
vi.mock('../services/realtime', () => ({ useRealtime: () => ({ on: () => undefined, onResync: () => undefined }) }))

const offer: DriverOffer = { id: 'campaign', title: null, description: null, amount: 500, rides: 10, completed: 3, categories: ['taxi'], ends_at: null, rewarded: false }
beforeEach(() => { setActivePinia(createPinia()); vi.mocked(getDriverOffers).mockReset() })

async function gift() {
  const router = createRouter({ history: createMemoryHistory(), routes: Object.values(ROUTE).map((name) => ({ path: name === ROUTE.home ? '/' : `/${name}`, name, component: { template: '<div />' } })) })
  await router.push('/')
  await router.isReady()
  const wrapper = mount(DriverPromoGift, { global: { plugins: [i18n, router] } })
  await flushPromises()
  return { wrapper, router }
}

describe('driver map gift', () => {
  it('opens the driver promotion for a real available offer', async () => {
    vi.mocked(getDriverOffers).mockResolvedValue([offer])
    const { wrapper, router } = await gift()
    expect(wrapper.find('.jt-promo-gift').exists()).toBe(true)
    await wrapper.find('.jt-promo-gift').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe(ROUTE.promotions)
    wrapper.unmount()
  })
  it.each([{ offers: [] as DriverOffer[] }, { offers: [{ ...offer, rewarded: true }] }])('hides when there is no unearned offer', async ({ offers }) => {
    vi.mocked(getDriverOffers).mockResolvedValue(offers)
    const { wrapper } = await gift()
    expect(wrapper.find('button').exists()).toBe(false)
    wrapper.unmount()
  })
})
