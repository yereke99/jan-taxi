// Shared with the other Mini App — keep both copies identical (make verify-shared).

import type { Component } from 'vue'

export type { Tone } from '../../utils/status'

export type ButtonVariant = 'primary' | 'secondary' | 'plain' | 'destructive'
export type ButtonSize = 'large' | 'medium' | 'small'

export interface SegmentedOption<V extends string = string> {
  value: V
  label: string
  disabled?: boolean
}

export interface ActionSheetAction {
  id: string
  label: string
  destructive?: boolean
  disabled?: boolean
}

/**
 * Named sheet positions ('content' = natural content height); a number is an
 * absolute height in px measured from the viewport bottom.
 */
export type SheetSnap = 'peek' | 'half' | 'full' | 'content' | number

/** Item of the floating glass tab bar (JTabBar). */
export interface TabBarItem<V extends string = string> {
  value: V
  label: string
  /** Icon component (e.g. from @lucide/vue), rendered at 24px. */
  icon: Component
  /** Small dot/count on the icon; 0 or undefined hides it. */
  badge?: number | boolean
}
