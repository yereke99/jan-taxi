// Sidebar sections and the admin routes they open. Every entry is guarded by
// its permission (route meta.perm) and hidden from operators who lack it.

import {
  BadgePercent,
  Bell,
  BookOpen,
  Car,
  ChartColumn,
  Gift,
  LayoutDashboard,
  ListFilter,
  ListOrdered,
  Map,
  MapPin,
  Megaphone,
  Scale,
  ScrollText,
  Send,
  Settings,
  ShieldAlert,
  ShieldCheck,
  Tag,
  TicketPercent,
  Truck,
  UserPlus,
  Users,
  Wallet,
} from '@lucide/vue'
import type { Component } from 'vue'
import type { Permission } from './permissions'

export type NavSection = 'operations' | 'finance' | 'marketing' | 'config' | 'system'

export interface NavItem {
  /** Route name. */
  name: string
  path: string
  /** i18n key of the label and page title. */
  titleKey: string
  perm: Permission
  icon: Component
  section: NavSection
}

export const NAV_SECTIONS: readonly NavSection[] = ['operations', 'finance', 'marketing', 'config', 'system']

export const NAV_ITEMS: readonly NavItem[] = [
  { name: 'dashboard', path: '/', titleKey: 'nav.dashboard', perm: 'dashboard.view', icon: LayoutDashboard, section: 'operations' },
  { name: 'orders', path: '/orders', titleKey: 'nav.orders', perm: 'orders.view', icon: ListOrdered, section: 'operations' },
  { name: 'disputes', path: '/disputes', titleKey: 'nav.disputes', perm: 'orders.view', icon: Scale, section: 'operations' },
  { name: 'drivers', path: '/drivers', titleKey: 'nav.drivers', perm: 'drivers.view', icon: Car, section: 'operations' },
  { name: 'moderation', path: '/moderation', titleKey: 'nav.moderation', perm: 'drivers.moderate', icon: ShieldCheck, section: 'operations' },
  { name: 'topups', path: '/topups', titleKey: 'nav.topups', perm: 'finance.view', icon: Wallet, section: 'finance' },
  { name: 'ledger', path: '/ledger', titleKey: 'nav.ledger', perm: 'finance.view', icon: BookOpen, section: 'finance' },
  { name: 'marketing', path: '/marketing', titleKey: 'nav.marketing', perm: 'marketing.view', icon: Megaphone, section: 'marketing' },
  { name: 'campaigns', path: '/campaigns', titleKey: 'nav.campaigns', perm: 'marketing.view', icon: Gift, section: 'marketing' },
  { name: 'coupons', path: '/coupons', titleKey: 'nav.coupons', perm: 'marketing.view', icon: TicketPercent, section: 'marketing' },
  { name: 'referrals', path: '/referrals', titleKey: 'nav.referrals', perm: 'marketing.view', icon: UserPlus, section: 'marketing' },
  { name: 'segments', path: '/segments', titleKey: 'nav.segments', perm: 'marketing.view', icon: ListFilter, section: 'marketing' },
  { name: 'broadcasts', path: '/broadcasts', titleKey: 'nav.broadcasts', perm: 'marketing.view', icon: Send, section: 'marketing' },
  { name: 'marketing-reports', path: '/marketing-reports', titleKey: 'nav.marketing_reports', perm: 'marketing.view', icon: ChartColumn, section: 'marketing' },
  { name: 'marketing-settings', path: '/marketing-settings', titleKey: 'nav.marketing_settings', perm: 'marketing.view', icon: ShieldAlert, section: 'marketing' },
  { name: 'cities', path: '/cities', titleKey: 'nav.cities', perm: 'config.manage', icon: Map, section: 'config' },
  { name: 'commission-areas', path: '/commission-areas', titleKey: 'nav.commission_areas', perm: 'config.manage', icon: BadgePercent, section: 'config' },
  { name: 'places', path: '/places', titleKey: 'nav.places', perm: 'config.manage', icon: MapPin, section: 'config' },
  { name: 'vehicle-classes', path: '/vehicle-classes', titleKey: 'nav.vehicle_classes', perm: 'config.manage', icon: Truck, section: 'config' },
  { name: 'tariffs', path: '/tariffs', titleKey: 'nav.tariffs', perm: 'dashboard.view', icon: Tag, section: 'config' },
  { name: 'settings', path: '/settings', titleKey: 'nav.settings', perm: 'config.manage', icon: Settings, section: 'config' },
  { name: 'notifications', path: '/notifications', titleKey: 'nav.notifications', perm: 'orders.manage', icon: Bell, section: 'system' },
  { name: 'admins', path: '/admins', titleKey: 'nav.admins', perm: 'admins.manage', icon: Users, section: 'system' },
  { name: 'audit', path: '/audit', titleKey: 'nav.audit', perm: 'audit.view', icon: ScrollText, section: 'system' },
]

/** First screen the operator may open (after login, or from "back home"). */
export function firstAllowedRoute(can: (p: Permission) => boolean): string | null {
  return NAV_ITEMS.find((i) => can(i.perm))?.name ?? null
}
