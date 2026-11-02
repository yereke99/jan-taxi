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
  marketing: () => import('../pages/marketing/MarketingOverviewPage.vue'),
  campaigns: () => import('../pages/marketing/CampaignsPage.vue'),
  coupons: () => import('../pages/marketing/CouponsPage.vue'),
  referrals: () => import('../pages/marketing/ReferralsPage.vue'),
  segments: () => import('../pages/marketing/SegmentsPage.vue'),
  broadcasts: () => import('../pages/marketing/BroadcastsPage.vue'),
  'marketing-reports': () => import('../pages/marketing/MarketingReportsPage.vue'),
  'marketing-settings': () => import('../pages/marketing/MarketingSettingsPage.vue'),
  cities: () => import('../pages/config/CitiesPage.vue'),
  'commission-areas': () => import('../pages/config/CommissionAreasPage.vue'),
  places: () => import('../pages/config/PlacesPage.vue'),
  'vehicle-classes': () => import('../pages/config/VehicleClassesPage.vue'),
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
  'campaign-detail': () => import('../pages/marketing/CampaignDetailPage.vue'),
  'broadcast-detail': () => import('../pages/marketing/BroadcastDetailPage.vue'),
  'marketing-user': () => import('../pages/marketing/MarketingUserPage.vue'),
}
