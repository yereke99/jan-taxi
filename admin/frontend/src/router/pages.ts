// Page component per route (lazy chunks). Section pages are keyed by the
// navigation item name; detail pages by their own route name.

import type { Component } from 'vue'

type Loader = () => Promise<Component | { default: Component }>

export const PAGE_COMPONENTS: Record<string, Loader> = {
  dashboard: () => import('../pages/dashboard/DashboardPage.vue'),
  orders: () => import('../pages/orders/OrdersPage.vue'),
  disputes: () => import('../pages/orders/DisputesPage.vue'),
  drivers: () => import('../pages/drivers/DriversPage.vue'),
  moderation: () => import('../pages/drivers/ModerationPage.vue'),
  topups: () => import('../pages/finance/TopUpsPage.vue'),
  ledger: () => import('../pages/finance/LedgerPage.vue'),
  cities: () => import('../pages/config/CitiesPage.vue'),
  places: () => import('../pages/config/PlacesPage.vue'),
  tariffs: () => import('../pages/config/TariffsPage.vue'),
  settings: () => import('../pages/config/SettingsPage.vue'),
  notifications: () => import('../pages/system/NotificationsPage.vue'),
  admins: () => import('../pages/system/AdminsPage.vue'),
  audit: () => import('../pages/system/AuditPage.vue'),
}

export const DETAIL_COMPONENTS: Record<string, Loader> = {
  'order-detail': () => import('../pages/orders/OrderDetailPage.vue'),
  'driver-detail': () => import('../pages/drivers/DriverDetailPage.vue'),
  'topup-detail': () => import('../pages/finance/TopUpDetailPage.vue'),
}
