// Shared prop types for the admin components (kept outside .vue files so pages
// can import them).

import type { Tone } from '../utils/status'

export type { Tone, StatusKind } from '../utils/status'

export type SortDirection = 'asc' | 'desc'

export interface SortState {
  key: string
  dir: SortDirection
}

export interface Column<Row> {
  /** Unique key; also the default property read from the row and the cell slot name (cell-<key>). */
  key: string
  label: string
  sortable?: boolean
  align?: 'left' | 'center' | 'right'
  /** CSS width, e.g. '120px' or '20%'. */
  width?: string
  /** Value used for display (via format) and client-side sorting. */
  value?: (row: Row) => unknown
  /** Text shown in the cell when no slot overrides it. */
  format?: (value: unknown, row: Row) => string
  /** Monospace, tabular digits (ids, money, codes). */
  mono?: boolean
}

export interface SelectOption<V extends string | number = string> {
  value: V
  label: string
  disabled?: boolean
}

export interface TabItem<V extends string = string> {
  value: V
  label: string
  count?: number
}

export interface KeyValueItem {
  key?: string
  label: string
  value?: string | number | null
  mono?: boolean
}

export type ButtonVariant = 'primary' | 'secondary' | 'plain' | 'destructive' | 'danger'
export type ButtonSize = 'sm' | 'md' | 'lg'

export interface BadgeProps {
  tone: Tone
  label: string
}

/** Private image shown in the viewer (url is an API file URL, loaded as a blob). */
export interface ViewerImage {
  url: string
  label: string
}

/** One series of a ColumnChart; `light` is the wider background column. */
export interface ColumnSeries {
  key: string
  label: string
  values: number[]
  shade: 'strong' | 'light'
}

/** One entry of <Timeline>. */
export interface TimelineItem {
  key: string | number
  time: string
  title: string
  detail?: string | null
  meta?: string | null
  tone?: 'neutral' | 'success' | 'danger' | 'dark'
}
