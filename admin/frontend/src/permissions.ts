// Admin roles and permissions (ARCHITECTURE §4.2). The server enforces them on
// every route; the UI uses them only to hide what the operator cannot open.

export const PERMISSIONS = [
  'dashboard.view',
  'orders.view',
  'orders.manage',
  'drivers.view',
  'drivers.moderate',
  'drivers.manage',
  'documents.view',
  'finance.view',
  'finance.manage',
  'config.manage',
  'admins.manage',
  'audit.view',
  'reports.export',
  // Marketing (§23.10): view campaigns and reports; edit drafts and segments;
  // approve activations, budgets and the emergency switches; send broadcasts.
  'marketing.view',
  'marketing.edit',
  'marketing.approve',
  'marketing.broadcast',
] as const
export type Permission = (typeof PERMISSIONS)[number]

export const ROLES = ['super_admin', 'operator', 'driver_moderator', 'finance_manager', 'auditor', 'marketing_manager'] as const
export type Role = (typeof ROLES)[number]

const BASE: Permission[] = ['dashboard.view', 'orders.view', 'drivers.view']

/** Mirror of the role table; used only when the API omits `permissions`. */
export const ROLE_PERMISSIONS: Record<Role, readonly Permission[]> = {
  super_admin: PERMISSIONS,
  operator: [...BASE, 'orders.manage', 'drivers.manage', 'audit.view', 'reports.export', 'marketing.view'],
  driver_moderator: [...BASE, 'drivers.moderate', 'drivers.manage', 'documents.view'],
  finance_manager: [...BASE, 'finance.view', 'finance.manage', 'audit.view', 'reports.export', 'marketing.view', 'marketing.approve'],
  auditor: [...BASE, 'finance.view', 'audit.view', 'reports.export', 'marketing.view'],
  // Prepares campaigns, segments and broadcasts; switching money on needs marketing.approve.
  marketing_manager: ['dashboard.view', 'marketing.view', 'marketing.edit', 'marketing.broadcast'],
}

export function isPermission(v: unknown): v is Permission {
  return typeof v === 'string' && (PERMISSIONS as readonly string[]).includes(v)
}

export function isRole(v: unknown): v is Role {
  return typeof v === 'string' && (ROLES as readonly string[]).includes(v)
}
