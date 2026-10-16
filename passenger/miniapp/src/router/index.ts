import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { ROUTE } from './names'

declare module 'vue-router' {
  interface RouteMeta {
    /** i18n key of the screen title (navigation bar, document title). */
    titleKey?: string
  }
}

const home = { name: ROUTE.home }
const book = { name: ROUTE.book }

// Map-first screens are children of MapShell, so the one map instance stays
// alive while the bottom sheet changes (§14).
const routes: RouteRecordRaw[] = [
  {
    path: '/',
    component: () => import('../pages/map/MapShell.vue'),
    children: [
      { path: '', name: ROUTE.home, component: () => import('../pages/map/HomeSheet.vue'), meta: { back: false } },
      { path: 'ride', name: ROUTE.book, component: () => import('../pages/map/RouteEditorSheet.vue'), meta: { backTo: home } },
      {
        path: 'ride/search/:index(\\d)',
        name: ROUTE.bookSearch,
        component: () => import('../pages/map/PlaceSearchSheet.vue'),
        meta: { backTo: book },
      },
      {
        path: 'ride/pick/:index(\\d)',
        name: ROUTE.bookPick,
        component: () => import('../pages/map/PickOnMapSheet.vue'),
        meta: { backTo: book },
      },
      { path: 'ride/confirm', name: ROUTE.confirm, component: () => import('../pages/map/ConfirmSheet.vue'), meta: { backTo: book } },
      {
        path: 'order/:id',
        name: ROUTE.order,
        component: () => import('../pages/map/OrderSheet.vue'),
        // A live order never goes "back" into the booking form that created it.
        meta: { backTo: home, backReplace: true },
      },
    ],
  },
  { path: '/orders', name: ROUTE.orders, component: () => import('../pages/OrdersPage.vue'), meta: { backTo: home } },
  {
    path: '/orders/:id',
    name: ROUTE.orderDetail,
    component: () => import('../pages/OrderDetailPage.vue'),
    meta: { backTo: { name: ROUTE.orders } },
  },
  { path: '/support', name: ROUTE.support, component: () => import('../pages/SupportPage.vue'), meta: { backTo: home } },
  { path: '/driver', name: ROUTE.becomeDriver, component: () => import('../pages/BecomeDriverPage.vue'), meta: { backTo: home } },
  { path: '/language', name: ROUTE.language, component: () => import('../pages/LanguagePage.vue'), meta: { backTo: home } },
  { path: '/profile', name: ROUTE.profile, component: () => import('../pages/ProfilePage.vue'), meta: { backTo: home } },
  { path: '/:pathMatch(.*)*', redirect: home },
]

export const router = createRouter({
  // BASE_URL is /passenger/ (vite base), matching the Caddy mount (§1).
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
  scrollBehavior: () => ({ top: 0 }),
})
