import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { i18n } from '../i18n'
import { NAV_ITEMS } from '../navigation'
import type { Permission } from '../permissions'
import { useAuthStore } from '../stores/auth'
import { guardDecision } from './guard'
import { DETAIL_COMPONENTS, PAGE_COMPONENTS } from './pages'

export { homeLocation, safeRedirect } from './guard'

declare module 'vue-router' {
  interface RouteMeta {
    /** Reachable without a session (login). */
    public?: boolean
    /** Permission required to open the route. */
    perm?: Permission
    /** i18n key of the page title. */
    titleKey?: string
    /** Sidebar item highlighted for a detail page. */
    navParent?: string
  }
}

const navRoutes: RouteRecordRaw[] = NAV_ITEMS.map((item) => ({
  path: item.path === '/' ? '' : item.path.slice(1),
  name: item.name,
  component: PAGE_COMPONENTS[item.name]!,
  meta: { perm: item.perm, titleKey: item.titleKey },
}))

const detailRoutes: RouteRecordRaw[] = [
  {
    path: 'orders/:id',
    name: 'order-detail',
    component: DETAIL_COMPONENTS['order-detail']!,
    props: true,
    meta: { perm: 'orders.view', titleKey: 'nav.order_detail', navParent: 'orders' },
  },
  {
    path: 'drivers/:id',
    name: 'driver-detail',
    component: DETAIL_COMPONENTS['driver-detail']!,
    props: true,
    meta: { perm: 'drivers.view', titleKey: 'nav.driver_detail', navParent: 'drivers' },
  },
  {
    path: 'topups/:id',
    name: 'topup-detail',
    component: DETAIL_COMPONENTS['topup-detail']!,
    props: true,
    meta: { perm: 'finance.view', titleKey: 'nav.topup_detail', navParent: 'topups' },
  },
]

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('../pages/LoginPage.vue'),
    meta: { public: true, titleKey: 'auth.sign_in' },
  },
  {
    path: '/',
    component: () => import('../layouts/AdminLayout.vue'),
    children: [
      ...navRoutes,
      ...detailRoutes,
      { path: 'forbidden', name: 'forbidden', component: () => import('../pages/ForbiddenPage.vue'), meta: { titleKey: 'pages.forbidden_title' } },
    ],
  },
  { path: '/:pathMatch(.*)*', name: 'not-found', component: () => import('../pages/NotFoundPage.vue'), meta: { public: true, titleKey: 'pages.not_found_title' } },
]

export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!auth.loaded) await auth.bootstrap()
  return guardDecision(to, auth)
})

router.afterEach((to) => {
  const { t } = i18n.global
  const title = to.meta.titleKey ? t(to.meta.titleKey) : ''
  document.title = title ? `${title} — Jan Taxi` : 'Jan Taxi'
})
