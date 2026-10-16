import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useSessionStore } from '../stores/session'
import { canAccess, entryRoute } from './access'
import { ROUTE } from './names'

declare module 'vue-router' {
  interface RouteMeta {
    /** i18n key of the screen title (navigation bar, document title). */
    titleKey?: string
  }
}

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: ROUTE.home,
    component: () => import('../pages/HomePage.vue'),
    meta: { back: false, access: 'approved', tab: 'home' },
  },
  {
    path: '/register',
    name: ROUTE.register,
    component: () => import('../pages/registration/RegistrationPage.vue'),
    // The BackButton walks back through the steps (?step=) once there is history.
    meta: { access: 'registration', hideTabBar: true },
  },
  {
    path: '/status',
    name: ROUTE.status,
    component: () => import('../pages/StatusPage.vue'),
    meta: { back: false, access: 'status', hideTabBar: true },
  },
  {
    path: '/orders',
    name: ROUTE.orders,
    component: () => import('../pages/OrdersPage.vue'),
    meta: { back: false, access: 'approved', tab: 'orders' },
  },
  {
    path: '/orders/:id',
    name: ROUTE.order,
    component: () => import('../pages/OrderDetailPage.vue'),
    meta: { access: 'approved', hideTabBar: true, backTo: { name: ROUTE.orders } },
  },
  {
    path: '/trip',
    name: ROUTE.trip,
    component: () => import('../pages/TripPage.vue'),
    meta: { access: 'approved', hideTabBar: true, backTo: { name: ROUTE.home } },
  },
  {
    path: '/history',
    name: ROUTE.history,
    component: () => import('../pages/HistoryPage.vue'),
    meta: { access: 'approved', backTo: { name: ROUTE.profile } },
  },
  {
    path: '/wallet',
    name: ROUTE.wallet,
    component: () => import('../pages/wallet/WalletPage.vue'),
    meta: { back: false, access: 'registered', tab: 'wallet' },
  },
  {
    path: '/wallet/topup',
    name: ROUTE.topup,
    component: () => import('../pages/wallet/TopUpAmountPage.vue'),
    meta: { access: 'registered', hideTabBar: true, backTo: { name: ROUTE.wallet } },
  },
  {
    path: '/wallet/topups/:id',
    name: ROUTE.topupDetail,
    component: () => import('../pages/wallet/TopUpDetailPage.vue'),
    meta: { access: 'registered', hideTabBar: true, backTo: { name: ROUTE.wallet } },
  },
  {
    path: '/profile',
    name: ROUTE.profile,
    component: () => import('../pages/ProfilePage.vue'),
    meta: { back: false, access: 'registered', tab: 'profile' },
  },
  {
    path: '/language',
    name: ROUTE.language,
    component: () => import('../pages/LanguagePage.vue'),
    meta: { access: 'any', backTo: { name: ROUTE.home } },
  },
  {
    path: '/support',
    name: ROUTE.support,
    component: () => import('../pages/SupportPage.vue'),
    meta: { access: 'any', backTo: { name: ROUTE.home } },
  },
  { path: '/:pathMatch(.*)*', redirect: { name: ROUTE.home } },
]

export const router = createRouter({
  // BASE_URL is /driver/ (vite base), matching the Caddy mount (§1).
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})

// Entry routing by driver status: screens a driver may not use (yet) send them
// to registration, the status screen or home. Until the session is ready App.vue
// shows the loading/error state, so navigation is not redirected.
router.beforeEach(async (to) => {
  const session = useSessionStore()
  await session.whenSettled()
  if (!session.isReady) return true
  const state = session.driverState
  if (canAccess(to.meta.access, state)) return true
  const target = entryRoute(state)
  return to.name === target ? true : { name: target, replace: true }
})
