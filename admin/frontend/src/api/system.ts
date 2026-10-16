// Dashboard metrics, CSV reports, notifications, administrators and the audit log.

import { api, downloadFile } from '../services/api'
import type { Permission, Role } from '../permissions'
import type { AdminRef, Language, Page } from './common'
import { dayEnd, dayStart, opt } from './common'

/** Totals of one period (reports.Summary). */
export interface PeriodSummary {
  from: string
  to: string
  orders_created: number
  orders_completed: number
  orders_cancelled: number
  orders_disputed: number
  gmv: number
  commission_charged: number
  commission_refunded: number
  commission_net: number
  topups_confirmed_count: number
  topups_confirmed_amount: number
}

export interface DailyRow {
  /** Local calendar date YYYY-MM-DD in the dashboard time zone. */
  date: string
  orders_created: number
  orders_completed: number
  gmv: number
  commission: number
  topups_confirmed_count: number
  topups_confirmed_amount: number
}

/** GET /admin/dashboard (admin/backend/internal/reports/dashboard.go). */
export interface DashboardMetrics {
  from: string
  to: string
  city_id: string | null
  timezone: string
  orders: {
    created: number
    completed: number
    cancelled: number
    cancelled_by_passenger: number
    cancelled_by_driver: number
    cancelled_by_admin: number
    no_driver_found: number
    disputed: number
    searching_now: number
    active_now: number
    by_category: { taxi: number; parcel: number; other: number }
  }
  revenue: { gmv: number; commission_charged: number; commission_refunded: number; commission_net: number; currency: string }
  drivers: {
    total: number
    approved: number
    online_now: number
    offline_now: number
    pending_review: number
    pending_applications: number
    changes_requested: number
    suspended: number
    registered_in_period: number
  }
  topups: {
    pending_verification_count: number
    pending_verification_amount: number
    confirmed_count: number
    confirmed_amount: number
    refunded_count: number
    refunded_amount: number
  }
  wallets: { total_balance: number; total_reserved: number }
  performance: { avg_accept_time_s: number | null; avg_trip_duration_s: number | null; completion_rate_bps: number | null }
  notifications: { pending: number; dead: number }
  cities: { total: number; active: number }
  today: PeriodSummary
  daily: DailyRow[]
}

/** Template keys (ARCHITECTURE §8), used for the outbox filter. */
export const NOTIFICATION_TEMPLATE_KEYS = [
  'order_created_passenger',
  'order_available_driver',
  'order_accepted_passenger',
  'order_accepted_driver',
  'order_driver_arriving',
  'order_started',
  'order_stop_completed',
  'order_completed_passenger',
  'order_completed_driver',
  'order_cancelled_passenger',
  'order_cancelled_driver',
  'order_taken_driver',
  'order_no_driver',
  'moderation_submitted',
  'moderation_approved',
  'moderation_changes_requested',
  'moderation_rejected',
  'driver_suspended',
  'driver_restored',
  'topup_created',
  'topup_confirmed',
  'topup_rejected',
  'wallet_low_balance',
  'dispute_opened',
  'dispute_resolved',
  'phone_verified',
] as const

export type OutboxStatus = 'pending' | 'sending' | 'sent' | 'dead' | 'cancelled'

export interface AttemptInfo {
  attempt: number
  outcome: string
  http_status: number | null
  error: string | null
  retry_after_s: number | null
  finished_at: string
}

export interface OutboxItem {
  id: string
  bot: 'passenger' | 'driver'
  user_id: string | null
  user_name: string
  username: string | null
  chat_id: number
  template: string
  params: Record<string, unknown> | null
  dedupe_key: string
  status: OutboxStatus
  attempts: number
  max_attempts: number
  next_attempt_at: string
  locked_until: string | null
  last_error: string | null
  telegram_message_id: number | null
  created_at: string
  sent_at: string | null
  attempts_summary: { total: number; sent: number; retry: number; fatal: number; last: AttemptInfo | null }
}

export interface TemplateParam {
  name: string
  kind: string
  optional: boolean
  description: string
}

export interface NotificationTemplate {
  key: string
  language: Language
  body: string
  is_custom: boolean
  default_body: string
  placeholders: string[]
  updated_at: string | null
  updated_by: AdminRef | null
  /** Stored override, even when it no longer validates. */
  override_body: string | null
  bots: Array<'passenger' | 'driver'>
  params: TemplateParam[]
  button: { label: string; start_param: string } | null
}

export interface AdminUser {
  id: string
  email: string
  full_name: string
  role: Role
  permissions: Permission[]
  is_active: boolean
  last_login_at: string | null
  locked_until: string | null
  created_at: string
  updated_at: string
}

/** Audit row as the Go handler returns it (admin fields are flattened). */
export interface AuditRecord {
  id: number
  admin_id: string | null
  admin_email: string | null
  admin_name: string | null
  action: string
  entity_type: string
  entity_id: string | null
  before: unknown
  after: unknown
  ip: string | null
  user_agent: string | null
  created_at: string
}

export interface AuditFilters {
  admin_id?: string
  entity_type?: string
  entity_id?: string
  action?: string
  from?: string
  to?: string
}

export const dashboardApi = {
  /** from/to are local dates (YYYY-MM-DD) in the dashboard time zone; "to" is inclusive. */
  get(q: { from?: string; to?: string; city_id?: string }, signal?: AbortSignal): Promise<DashboardMetrics> {
    return api.get('/admin/dashboard', { query: { from: opt(q.from), to: opt(q.to), city_id: opt(q.city_id) }, signal })
  },
}

export type ReportKind = 'orders' | 'ledger' | 'topups'

export const reportsApi = {
  /** Streams the CSV export for the current filters to a browser download. */
  download(kind: ReportKind, query: Record<string, string | undefined>): Promise<string> {
    return downloadFile(`/admin/reports/${kind}.csv`, query, `jantaxi-${kind}.csv`)
  },
}

export const notificationsApi = {
  list(f: { status?: string; bot?: string; template?: string }, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<OutboxItem>> {
    return api.get('/admin/notifications', {
      query: { status: opt(f.status), bot: opt(f.bot), template: opt(f.template), page, page_size: pageSize },
      signal,
    })
  },
  retry(id: string): Promise<OutboxItem> {
    return api.post(`/admin/notifications/${encodeURIComponent(id)}/retry`)
  },
  templates(signal?: AbortSignal): Promise<{ items: NotificationTemplate[]; max_body_length?: number }> {
    return api.get('/admin/notification-templates', { signal })
  },
  saveTemplate(key: string, lang: Language, body: string): Promise<NotificationTemplate> {
    return api.put(`/admin/notification-templates/${encodeURIComponent(key)}/${lang}`, { body })
  },
  revertTemplate(key: string, lang: Language): Promise<NotificationTemplate> {
    return api.delete(`/admin/notification-templates/${encodeURIComponent(key)}/${lang}`)
  },
}

export const adminsApi = {
  list(page: number, pageSize: number, signal?: AbortSignal): Promise<Page<AdminUser>> {
    return api.get('/admin/admins', { query: { page, page_size: pageSize }, signal })
  },
  create(body: { email: string; full_name: string; role: Role; password: string }): Promise<AdminUser> {
    return api.post('/admin/admins', body)
  },
  update(id: string, body: { full_name?: string; role?: Role; is_active?: boolean; password?: string }): Promise<AdminUser> {
    return api.patch(`/admin/admins/${encodeURIComponent(id)}`, body)
  },
  changeOwnPassword(current: string, next: string): Promise<void> {
    return api.post('/admin/auth/password', { current_password: current, new_password: next })
  },
}

export const auditApi = {
  list(f: AuditFilters, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<AuditRecord>> {
    return api.get('/admin/audit-logs', {
      query: {
        admin_id: opt(f.admin_id),
        entity_type: opt(f.entity_type?.trim()),
        entity_id: opt(f.entity_id?.trim()),
        action: opt(f.action?.trim()),
        // Local calendar days → instants, so the range follows the operator's clock.
        from: dayStart(f.from),
        to: dayEnd(f.to),
        page,
        page_size: pageSize,
      },
      signal,
    })
  },
}
