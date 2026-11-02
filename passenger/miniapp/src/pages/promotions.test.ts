// Promotion screens (§23): the coupon countdown on the server's clock, the
// first-ride offer with the service its first order must be, the invitation
// locked, locked for good (no first-ride coupon to use or to earn) and open
// (share and copy), the bonus history and the profile's promotion rows with
// the marketing messages switch.

import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick, type Component } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { parseBonusPage, parsePromotions, parseReferral } from '../api/parse'
import { getPromotions, getReferral, listBonusHistory, setMarketingSubscription } from '../api/promotions'
import { toast } from '../components/ui'
import { i18n } from '../i18n'
import { ROUTE } from '../router/names'
import { ApiError } from '../services/http'
import { usePromotionsStore } from '../stores/promotions'
import { openTelegram } from '../telegram'
import { copyText } from '../utils/clipboard'
import BonusPage from './BonusPage.vue'
import CouponsPage from './CouponsPage.vue'
import InvitePage from './InvitePage.vue'
import ProfilePage from './ProfilePage.vue'

vi.mock('../api/promotions', () => ({
  getPromotions: vi.fn(),
  getReferral: vi.fn(),
  listBonusHistory: vi.fn(),
  setMarketingSubscription: vi.fn(),
}))
vi.mock('../services/realtime', () => ({
  useRealtime: () => ({ on: () => () => undefined, onResync: () => () => undefined }),
}))
vi.mock('../utils/clipboard', () => ({ copyText: vi.fn() }))
vi.mock('../telegram', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../telegram')>()),
  openTelegram: vi.fn(() => true),
}))

/** Money and dates use no-break spaces. */
const plain = (s: string) => s.replace(/\u00a0/g, ' ')

/** The device clock; the server runs ten minutes ahead of it. */
const DEVICE_NOW = Date.parse('2026-11-02T10:00:00Z')
const SERVER_NOW = DEVICE_NOW + 600_000

function promotionsAnswer(over: Record<string, unknown> = {}, couponExpiresInMs = 2 * 86_400_000) {
  return parsePromotions({
    server_time: new Date(SERVER_NOW).toISOString(),
    redemptions_enabled: true,
    coupons: [
      {
        id: 'c1',
        title: { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' },
        amount: 400,
        min_order_fare: 400,
        categories: ['taxi'],
        status: 'issued',
        issued_at: new Date(SERVER_NOW - 3_600_000).toISOString(),
        expires_at: new Date(SERVER_NOW + couponExpiresInMs).toISOString(),
      },
    ],
    bonus: { balance: 300, reserved: 0, available: 300 },
    bonus_categories: ['taxi'],
    referral: { state: 'unlocked', reward_amount: 300, rewarded: false },
    marketing_subscribed: true,
    ...over,
  })
}

const Blank = { template: '<div />' }

async function mountPage(component: Component, path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: ROUTE.home, component: Blank },
      { path: '/order/:id', name: ROUTE.order, component: Blank },
      { path: '/orders', name: ROUTE.orders, component: Blank },
      { path: '/support', name: ROUTE.support, component: Blank },
      { path: '/driver', name: ROUTE.becomeDriver, component: Blank },
      { path: '/language', name: ROUTE.language, component: Blank },
      { path: '/profile', name: ROUTE.profile, component: Blank },
      { path: '/coupons', name: ROUTE.coupons, component: Blank },
      { path: '/bonus', name: ROUTE.bonus, component: Blank },
      { path: '/invite', name: ROUTE.invite, component: Blank },
    ],
  })
  await router.push(path)
  await router.isReady()
  const w = mount(component, { global: { plugins: [i18n, router] } })
  await settle()
  return w
}

/** Lets mocked requests resolve and the DOM update (fake timers). */
async function settle(): Promise<void> {
  await vi.advanceTimersByTimeAsync(0)
  await nextTick()
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.useFakeTimers()
  vi.setSystemTime(DEVICE_NOW)
  vi.mocked(getPromotions).mockReset()
  vi.mocked(getReferral).mockReset()
  vi.mocked(listBonusHistory).mockReset()
  vi.mocked(setMarketingSubscription).mockReset()
  vi.mocked(copyText).mockReset()
  vi.mocked(openTelegram).mockClear()
})

afterEach(() => {
  toast.clear()
  vi.useRealTimers()
})

describe('coupons', () => {
  const units = (w: ReturnType<typeof mount>) => w.findAll('.coupon__digits').map((d) => d.text())

  it('counts down on the server clock and turns expired exactly at expires_at', async () => {
    // Five seconds left on the server's clock; ten minutes on the device's.
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({}, 5000))
    const w = await mountPage(CouponsPage, '/coupons')
    expect(units(w)).toEqual(['00', '00', '00', '05'])
    expect(w.find('.coupon__status').text()).toBe('Белсенді')
    expect(w.find('.coupon__timer').attributes('aria-label')).toBe('Мерзімінің бітуіне қалды: 0 күн 0 сағат 0 минут 5 секунд')

    await vi.advanceTimersByTimeAsync(4000)
    expect(units(w)).toEqual(['00', '00', '00', '01'])
    await vi.advanceTimersByTimeAsync(999)
    expect(w.find('.coupon__status').text()).toBe('Белсенді')
    await vi.advanceTimersByTimeAsync(1)
    expect(w.find('.coupon__status').text()).toBe('Мерзімі өтті')
    expect(w.find('.coupon__timer').exists()).toBe(false)
    expect(w.find('.coupon__rules').exists()).toBe(false)
    expect(w.find('.coupon__meta').text()).toMatch(/мерзімі өтті$/)
    w.unmount()
  })

  it('shows days, hours, minutes and seconds with the terms of an active coupon', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({}, 86_400_000 + 2 * 3_600_000 + 3 * 60_000 + 4000))
    const w = await mountPage(CouponsPage, '/coupons')
    expect(plain(w.find('.coupon__amount').text())).toBe('400 ₸')
    expect(w.find('.coupon__title').text()).toBe('Алғашқы сапар')
    expect(units(w)).toEqual(['01', '02', '03', '04'])
    expect(w.findAll('.coupon__unit-label').map((l) => l.text())).toEqual(['күн', 'сағат', 'минут', 'секунд'])
    expect(w.findAll('.coupon__rules li').map((li) => plain(li.text()))).toEqual([
      'Келесі тапсырысыңызға автоматты түрде қолданылады',
      'Тек такси тапсырысына жарамды',
      'Тапсырыс құны кемінде 400 ₸ болуы керек',
      'Бір сапарға ғана қолданылады',
      'Тек сіздің аккаунтыңызға тиесілі, басқа адамға берілмейді',
      'Қолма-қол ақшаға айырбасталмайды',
    ])
    w.unmount()
  })

  it('offers the first-ride coupon before it is earned and links a reserved coupon to its order', async () => {
    vi.mocked(getPromotions).mockResolvedValue(
      promotionsAnswer({
        coupons: [],
        offers: [{ kind: 'first_ride_coupon', title: { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' }, amount: 400, validity_hours: 72, categories: ['taxi'] }],
      }),
    )
    const w = await mountPage(CouponsPage, '/coupons')
    expect(w.findAll('.offer__text > span').map((s) => plain(s.text()))).toEqual([
      'Алғашқы сапар',
      'Алғашқы такси сапарыңыз аяқталған соң 400 ₸ купон аласыз.',
      // A first delivery earns nothing (§23.4): said before the passenger sends a parcel first.
      'Алғашқы тапсырысыңызды басқа қызметке берсеңіз, купон берілмейді.',
      'Купонның жарамдылық мерзімі: 72 сағ.',
    ])
    expect(w.find('.jt-empty').exists()).toBe(false)
    w.unmount()

    const orderId = '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d34'
    vi.mocked(getPromotions).mockResolvedValue(
      promotionsAnswer({
        coupons: [{ id: 'c1', amount: 400, min_order_fare: 400, categories: ['taxi'], status: 'reserved', expires_at: new Date(SERVER_NOW + 3_600_000).toISOString(), reserved_order_id: orderId }],
      }),
    )
    setActivePinia(createPinia())
    const reserved = await mountPage(CouponsPage, '/coupons')
    expect(reserved.find('.coupon__status').text()).toBe('Тапсырысқа қолданылды')
    expect(reserved.find('.coupon__timer').exists()).toBe(false)
    expect(reserved.text()).toContain('Тапсырысты ашу')
    reserved.unmount()
  })

  it('names the services whose first order earns the coupon, and none when every service does', async () => {
    const offer = (categories: string[]) => ({ kind: 'first_ride_coupon', title: null, amount: 400, validity_hours: null, categories })
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ coupons: [], offers: [offer(['taxi', 'parcel'])] }))
    const some = await mountPage(CouponsPage, '/coupons')
    expect(some.findAll('.offer__text > span').map((s) => plain(s.text()))).toEqual([
      'Алғашқы сапар',
      'Алғашқы сапарыңыз аяқталған соң 400 ₸ купон аласыз.',
      'Купон алғашқы тапсырысыңыз мына қызметтердің біріне болса ғана беріледі: Такси, Сәлемдеме.',
    ])
    some.unmount()

    setActivePinia(createPinia())
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ coupons: [], offers: [offer(['taxi', 'parcel', 'other'])] }))
    const every = await mountPage(CouponsPage, '/coupons')
    expect(every.findAll('.offer__text > span').map((s) => plain(s.text()))).toEqual(['Алғашқы сапар', 'Алғашқы сапарыңыз аяқталған соң 400 ₸ купон аласыз.'])
    every.unmount()
  })

  it('says a coupon the server refused for an order cannot be used now', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer())
    const w = await mountPage(CouponsPage, '/coupons')
    expect(w.text()).not.toContain('Бұл купонды қазір қолдану мүмкін емес.')
    const store = usePromotionsStore()
    store.refuseCoupon(store.data!.coupons[0]!)
    await nextTick()
    expect(w.find('.coupon__status').text()).toBe('Белсенді')
    expect(w.text()).toContain('Бұл купонды қазір қолдану мүмкін емес.')
    w.unmount()
  })

  it('labels the Russian countdown with abbreviations that read with any number', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({}, 86_400_000 + 3_600_000))
    i18n.global.locale.value = 'ru'
    try {
      const w = await mountPage(CouponsPage, '/coupons')
      expect(w.findAll('.coupon__unit-label').map((l) => l.text())).toEqual(['дн.', 'ч', 'мин', 'сек'])
      expect(units(w)).toEqual(['01', '01', '00', '00'])
      w.unmount()
    } finally {
      i18n.global.locale.value = 'kk'
    }
  })

  it('says an active coupon waits while coupons are switched off', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ redemptions_enabled: false }))
    const w = await mountPage(CouponsPage, '/coupons')
    expect(w.find('.coupon__status').text()).toBe('Белсенді')
    expect(w.text()).toContain('Купондарды қолдану уақытша тоқтатылды.')
    w.unmount()
  })

  it('says there are no promotions when the API has none', async () => {
    vi.mocked(getPromotions).mockRejectedValue(new ApiError(404, 'not_found', 'no route'))
    const w = await mountPage(CouponsPage, '/coupons')
    expect(w.find('.jt-empty__title').text()).toBe('Акциялар әзірге жоқ')
    w.unmount()
  })
})

describe('invite a friend', () => {
  const LINK = 'https://t.me/jantaxi_bot?start=ref_AB2C3D4E'

  function referral(over: Record<string, unknown> = {}) {
    return parseReferral({
      state: 'unlocked',
      reward_amount: 300,
      rewarded: false,
      title: { kk: 'Досыңды шақыр', ru: 'Пригласи друга', en: 'Invite a friend' },
      description: null,
      code: 'AB2C3D4E',
      link: LINK,
      app_link: 'https://t.me/jantaxi_bot?startapp=ref_AB2C3D4E',
      earned: 0,
      friends: [
        { name: 'Дана Қ.', status: 'attributed', attributed_at: '2026-11-01T10:00:00Z' },
        { name: 'Арман Б.', status: 'capped', attributed_at: '2026-10-30T10:00:00Z' },
      ],
      ...over,
    })
  }

  beforeEach(() => vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer()))

  it('explains why inviting is locked: by default after the first-ride coupon is used', async () => {
    vi.mocked(getReferral).mockResolvedValue(referral({ state: 'locked', code: undefined, link: undefined, app_link: undefined, friends: [] }))
    const w = await mountPage(InvitePage, '/invite')
    expect(w.find('.invite-card__title').text()).toBe('Шақыру әзірге қолжетімсіз')
    expect(w.find('.invite-card__text').text()).toBe('Алғашқы сапар купонын пайдаланғаннан кейін досыңызды шақыра аласыз.')
    expect(w.find('.invite-code').exists()).toBe(false)
    expect(w.find('.invite-share').exists()).toBe(false)
    expect(plain(w.find('.jt-row__value').text())).toBe('300 ₸')
    expect(w.find('.jt-list-group__footer').text()).toContain('бір рет қана беріледі')
    w.unmount()
  })

  it('names the lock reason the server gives', async () => {
    vi.mocked(getReferral).mockResolvedValue(referral({ state: 'locked', lock_reason: 'first_ride_required', code: undefined, friends: [] }))
    const w = await mountPage(InvitePage, '/invite')
    expect(w.find('.invite-card__text').text()).toBe('Алғашқы сапарыңызды аяқтағаннан кейін досыңызды шақыра аласыз.')
    w.unmount()
  })

  const LOCKED = { state: 'locked', lock_reason: 'coupon_not_redeemed', reward_amount: 300, rewarded: false }

  it('says the promotion is for new passengers when inviting can never open: no coupon to use or to earn', async () => {
    // A passenger who rode before the launch: locked under the default policy, without a coupon or an offer.
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ coupons: [], referral: LOCKED }))
    vi.mocked(getReferral).mockResolvedValue(referral({ state: 'locked', lock_reason: 'coupon_not_redeemed', code: undefined, link: undefined, app_link: undefined, friends: [] }))
    const w = await mountPage(InvitePage, '/invite')
    expect(w.find('.jt-empty__title').text()).toBe('Акция жаңа жолаушыларға арналған')
    expect(w.find('.jt-empty__description').text()).toBe(
      'Досыңызды шақыру Алғашқы сапар купонын пайдаланған жолаушыларға ғана ашылады. Бұл купонды жаңа жолаушылар алғашқы сапарынан кейін алады. Сіздің аккаунтыңыз үшін бұл акция қолжетімсіз.',
    )
    expect(w.find('.invite-card').exists()).toBe(false)
    expect(w.text()).not.toContain('300')
    w.unmount()

    // The same once the coupon expired unused.
    setActivePinia(createPinia())
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ coupons: [{ id: 'c1', amount: 400, min_order_fare: 400, categories: ['taxi'], status: 'expired', expires_at: new Date(SERVER_NOW - 60_000).toISOString() }], referral: LOCKED }))
    const expired = await mountPage(InvitePage, '/invite')
    expect(expired.find('.jt-empty__title').text()).toBe('Акция жаңа жолаушыларға арналған')
    expired.unmount()
  })

  it('keeps the lock while the first-ride coupon can still be earned or is on an order', async () => {
    vi.mocked(getReferral).mockResolvedValue(referral({ state: 'locked', lock_reason: 'coupon_not_redeemed', code: undefined, link: undefined, app_link: undefined, friends: [] }))
    vi.mocked(getPromotions).mockResolvedValue(
      promotionsAnswer({ coupons: [], offers: [{ kind: 'first_ride_coupon', amount: 400, validity_hours: 72, categories: ['taxi'] }], referral: LOCKED }),
    )
    const earn = await mountPage(InvitePage, '/invite')
    expect(earn.find('.invite-card__title').text()).toBe('Шақыру әзірге қолжетімсіз')
    earn.unmount()

    setActivePinia(createPinia())
    const orderId = '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d34'
    vi.mocked(getPromotions).mockResolvedValue(
      promotionsAnswer({
        coupons: [{ id: 'c1', amount: 400, min_order_fare: 400, categories: ['taxi'], status: 'reserved', expires_at: new Date(SERVER_NOW + 3_600_000).toISOString(), reserved_order_id: orderId }],
        referral: LOCKED,
      }),
    )
    const onOrder = await mountPage(InvitePage, '/invite')
    expect(onOrder.find('.invite-card__title').text()).toBe('Шақыру әзірге қолжетімсіз')
    onOrder.unmount()
  })

  it('shares the invitation link through Telegram, copies it and lists the friends', async () => {
    vi.mocked(getReferral).mockResolvedValue(referral())
    vi.mocked(copyText).mockResolvedValue(true)
    const w = await mountPage(InvitePage, '/invite')
    expect(plain(w.find('.invite-card__text').text())).toBe(
      'Досыңызды Jan Taxi-ге шақырыңыз. Ол алғашқы сапарын аяқтаған соң, бонус балансыңызға 300 ₸ түседі.',
    )
    expect(w.find('.jt-list-group__footer').text()).toBe(
      'Бонус шақырған достарыңыздың санына қарамастан бір рет қана беріледі. Бонусты тек шақырған адам алады.',
    )
    expect(w.find('.invite-code__value').text()).toBe('AB2C3D4E')

    await w.find('.invite-share').trigger('click')
    const text = 'Jan Taxi-ге қосыл! Такси мен жеткізуді Telegram ішінде оңай шақыруға болады.'
    expect(openTelegram).toHaveBeenCalledWith(`https://t.me/share/url?url=${encodeURIComponent(LINK)}&text=${encodeURIComponent(text)}`)

    await w.find('.invite-copy').trigger('click')
    await settle()
    expect(copyText).toHaveBeenCalledWith(LINK)
    expect(toast.items.map((x) => x.message)).toContain('Сілтеме көшірілді')

    const friends = w.findAll('.jt-row').filter((r) => r.find('.jt-row__subtitle').exists())
    expect(friends.map((r) => r.find('.jt-row__title').text())).toEqual(['Дана Қ.', 'Арман Б.'])
    expect(friends[0]!.find('.jt-row__subtitle').text()).toContain('Досыңыздың алғашқы сапары күтілуде')
    expect(friends[1]!.find('.jt-row__subtitle').text()).toContain('Есепке алынды, бонус бұрын берілген')
    w.unmount()
  })

  it('says the campaign is not running when the state is unavailable', async () => {
    vi.mocked(getReferral).mockResolvedValue(referral({ state: 'unavailable', code: undefined, friends: [] }))
    const w = await mountPage(InvitePage, '/invite')
    expect(w.find('.jt-empty__title').text()).toBe('Акция қазір өтпейді')
    w.unmount()
  })
})

describe('bonus balance', () => {
  it('shows the balance and the history with translated types and signed amounts', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ bonus: { balance: 300, reserved: 100, available: 200 } }))
    vi.mocked(listBonusHistory).mockResolvedValueOnce(
      parseBonusPage({
        items: [
          { id: 12, type: 'grant', amount: 300, order_number: null, created_at: '2026-11-01T10:00:00Z' },
          { id: 11, type: 'spend', amount: -200, order_number: 1024, created_at: '2026-10-30T10:00:00Z' },
        ],
        next_cursor: '11',
      }),
    )
    const w = await mountPage(BonusPage, '/bonus')
    expect(plain(w.find('.bonus-card__value').text())).toBe('300 ₸')
    const rows = w.findAll('.jt-row')
    expect(rows.slice(0, 2).map((r) => [r.find('.jt-row__title').text(), plain(r.find('.jt-row__value').text())])).toEqual([
      ['Қолжетімді', '200 ₸'],
      ['Тапсырысқа брондалған', '100 ₸'],
    ])
    const amounts = w.findAll('.bonus-amount')
    expect(amounts.map((a) => plain(a.text()))).toEqual(['+300 ₸', '−200 ₸'])
    expect(amounts[0]!.classes()).toContain('is-credit')
    expect(amounts[1]!.classes()).not.toContain('is-credit')
    expect(w.text()).toContain('Бонус есептелді')
    expect(w.text()).toContain('Тапсырысқа жұмсалды')
    expect(w.text()).toContain('Тапсырыс №1024')

    vi.mocked(listBonusHistory).mockResolvedValueOnce(parseBonusPage({ items: [{ id: 10, type: 'adjustment_credit', amount: 200, created_at: '2026-10-29T10:00:00Z' }], next_cursor: null }))
    await w.find('.bonus-more button').trigger('click')
    await settle()
    expect(listBonusHistory).toHaveBeenLastCalledWith({ before: '11' })
    expect(w.findAll('.bonus-amount')).toHaveLength(3)
    expect(w.text()).toContain('Түзету: қосылды')
    expect(w.find('.bonus-more').exists()).toBe(false)
    w.unmount()
  })
})

describe('profile', () => {
  const rowTitles = (w: ReturnType<typeof mount>) => w.findAll('.jt-row__title').map((t) => t.text())

  it('lists the coupon, the bonus balance and the invitation, and saves the marketing messages switch', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer())
    vi.mocked(setMarketingSubscription).mockResolvedValue(false)
    const w = await mountPage(ProfilePage, '/profile')
    expect(rowTitles(w)).toEqual(expect.arrayContaining(['Менің купоным', 'Бонус балансы', 'Досыңды шақыр', 'Жарнамалық хабарламалар']))
    const coupons = w.findAll('.jt-row').find((r) => r.text().startsWith('Менің купоным'))!
    expect(plain(coupons.find('.jt-row__value').text())).toBe('400 ₸')
    expect(coupons.attributes('href')).toBe('/coupons')

    const toggle = w.find('[role="switch"]')
    expect(toggle.attributes('aria-checked')).toBe('true')
    await toggle.trigger('click')
    await settle()
    expect(setMarketingSubscription).toHaveBeenCalledWith(false)
    expect(w.find('[role="switch"]').attributes('aria-checked')).toBe('false')
    w.unmount()
  })

  it('hides the invitation while no campaign runs and every promotion row without promotions', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ referral: { state: 'unavailable' }, coupons: [] }))
    const w = await mountPage(ProfilePage, '/profile')
    expect(rowTitles(w)).toContain('Менің купоным')
    expect(rowTitles(w)).not.toContain('Досыңды шақыр')
    expect(w.findAll('.jt-row').find((r) => r.text().startsWith('Менің купоным'))!.find('.jt-row__value').exists()).toBe(false)
    w.unmount()

    setActivePinia(createPinia())
    vi.mocked(getPromotions).mockRejectedValue(new ApiError(404, 'not_found', 'no route'))
    const without = await mountPage(ProfilePage, '/profile')
    expect(rowTitles(without)).not.toContain('Менің купоным')
    expect(without.find('[role="switch"]').exists()).toBe(false)
    without.unmount()
  })

  it('hides the invitation from a passenger it can never open for, and keeps it while the coupon waits', async () => {
    const locked = { state: 'locked', lock_reason: 'coupon_not_redeemed', reward_amount: 300 }
    // Rode before the launch: no coupon, no first-ride offer.
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ coupons: [], referral: locked }))
    const legacy = await mountPage(ProfilePage, '/profile')
    expect(rowTitles(legacy)).toEqual(expect.arrayContaining(['Менің купоным', 'Бонус балансы']))
    expect(rowTitles(legacy)).not.toContain('Досыңды шақыр')
    legacy.unmount()

    setActivePinia(createPinia())
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ referral: locked }))
    const waiting = await mountPage(ProfilePage, '/profile')
    expect(rowTitles(waiting)).toContain('Досыңды шақыр')
    waiting.unmount()
  })
})
