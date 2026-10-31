// One-tap acceptance shared by the order cards (Orders tab) and the order
// screen. Both go through the trip store to POST /driver/orders/{id}/accept —
// the same atomic server operation as the driver bot's accept button — so the
// first driver wins and a repeated tap is a harmless replay. A missing or stale
// location is fixed in place (one fresh GPS fix, one retry); a lost response
// is checked against the active trip before it is reported as a failure. Only
// one acceptance runs at a time in the whole app.

import { computed, ref, type ComputedRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { toast } from '../../components/ui'
import { useDriverErrorText } from '../../composables/useDriverErrorText'
import { ROUTE } from '../../router/names'
import { isApiError } from '../../services/http'
import { useDutyStore } from '../../stores/duty'
import { useOrdersStore } from '../../stores/orders'
import { useSessionStore } from '../../stores/session'
import { useTripStore } from '../../stores/trip'
import { useWalletStore } from '../../stores/wallet'
import { useEnsureLocation } from '../location/useEnsureLocation'
import { classifyAcceptError, type AcceptFailure } from './acceptOutcome'

/** The order being accepted right now, app-wide (null when none). */
const acceptingId = ref<string | null>(null)

export type InsufficientBalance = Extract<AcceptFailure, { kind: 'insufficient_balance' }>

export interface AcceptHooks {
  /** 402: replaces the default top-up toast (the order screen shows its own shortfall). */
  onInsufficient?: (f: InsufficientBalance) => void
  /** The order can no longer be taken (taken, out of reach, closed). */
  onGone?: () => void
}

export interface AcceptOrder {
  /** Id of the order being accepted, or null. */
  acceptingId: Readonly<typeof acceptingId>
  /** True while any acceptance (or another trip action) is running. */
  busy: ComputedRef<boolean>
  /** Accepts orderId; resolves true when the order is now the driver's trip. */
  accept: (orderId: string, hooks?: AcceptHooks) => Promise<boolean>
}

export function useAcceptOrder(): AcceptOrder {
  const { t } = useI18n()
  const errorText = useDriverErrorText()
  const router = useRouter()
  const trip = useTripStore()
  const orders = useOrdersStore()
  const wallet = useWalletStore()
  const duty = useDutyStore()
  const session = useSessionStore()
  const gps = useEnsureLocation()

  const busy = computed(() => acceptingId.value !== null || trip.busy)

  /** Without a recent location the API answers location_required: send a fresh fix and try once more. */
  async function attempt(orderId: string): Promise<void> {
    try {
      await trip.accept(orderId)
    } catch (err) {
      if (classifyAcceptError(err).kind !== 'location_required' || !(await gps.ensure())) throw err
      await trip.accept(orderId)
    }
  }

  function succeeded(orderId: string): void {
    orders.remove(orderId)
    toast.success(t('order.accepted_toast'))
    // The commission was charged with the acceptance.
    void wallet.refreshWallet()
    void router.replace({ name: ROUTE.trip })
  }

  function locationText(): string {
    switch (gps.state.value) {
      case 'denied':
        return t('orders.location_denied')
      case 'failed':
        return t('orders.location_failed')
      case 'outside':
        return t('duty.outside_text')
      default:
        return t('order.location_toast')
    }
  }

  async function goOnline(): Promise<void> {
    const res = await duty.setOnline(true)
    if (!res.ok) void router.replace({ name: ROUTE.home })
  }

  function failed(orderId: string, err: unknown, hooks: AcceptHooks): void {
    const f = classifyAcceptError(err)
    const gone = (message: string): void => {
      // Another driver was faster, or the order closed: normal in dispatch, not an error.
      toast.show(message)
      orders.remove(orderId)
      hooks.onGone?.()
    }
    switch (f.kind) {
      case 'taken':
        gone(t('order.taken'))
        return
      case 'unavailable':
        gone(t('order.unavailable_toast'))
        return
      case 'closed':
        gone(t('order.closed_toast'))
        return
      case 'insufficient_balance':
        void wallet.refreshWallet()
        if (hooks.onInsufficient) {
          hooks.onInsufficient(f)
          return
        }
        toast.show({
          message: t('order.insufficient_toast'),
          tone: 'error',
          duration: 6000,
          action: {
            label: t('orders.top_up'),
            run: () => void router.push({ name: ROUTE.topup, query: f.suggestedTopUp ? { amount: String(f.suggestedTopUp) } : {} }),
          },
        })
        return
      case 'has_active_order':
        void trip.refresh().then(() => router.replace({ name: ROUTE.trip }))
        return
      case 'offline':
        toast.show({
          message: t('order.offline_toast'),
          tone: 'error',
          action: { label: t('orders.go_online'), run: () => void goOnline() },
          duration: 6000,
        })
        return
      case 'location_required':
        toast.show({
          message: locationText(),
          tone: 'error',
          action:
            gps.state.value === 'outside'
              ? undefined
              : { label: gps.state.value === 'denied' ? t('common.open_settings') : t('orders.enable_location'), run: () => void gps.fix() },
          duration: 8000,
        })
        return
      case 'not_approved':
        // The status changed (suspended, profile missing): re-reading /me re-routes the driver.
        if (!session.handleAccessError(err)) {
          toast.error(errorText(err))
          void session.refreshMe()
        }
        return
      default:
        toast.error(errorText(f.error))
    }
  }

  async function accept(orderId: string, hooks: AcceptHooks = {}): Promise<boolean> {
    if (busy.value) return false
    acceptingId.value = orderId
    try {
      await attempt(orderId)
      succeeded(orderId)
      return true
    } catch (err) {
      // No answer (network drop, timeout): the acceptance may still have
      // committed. The active trip tells; a retry would only be a replay.
      if (isApiError(err) && err.status === 0) {
        await trip.refresh()
        if (trip.active?.id === orderId) {
          succeeded(orderId)
          return true
        }
        toast.show({
          message: errorText(err),
          tone: 'error',
          action: { label: t('common.retry'), run: () => void accept(orderId, hooks) },
          duration: 6000,
        })
        return false
      }
      failed(orderId, err, hooks)
      return false
    } finally {
      acceptingId.value = null
    }
  }

  return { acceptingId, busy, accept }
}
