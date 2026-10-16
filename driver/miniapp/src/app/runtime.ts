// Driver app runtime wiring: realtime hints → store refreshes, re-sync after
// reconnects and foreground returns, location reporting while online or on a
// trip, and re-routing when the moderation status changes.

import { watch } from 'vue'
import type { Router } from 'vue-router'
import { toast } from '../components/ui'
import { i18n } from '../i18n'
import { canAccess, entryRoute } from '../router/access'
import { ROUTE } from '../router/names'
import type { RealtimeClient, RealtimeEvent } from '../services/realtime'
import { useDutyStore } from '../stores/duty'
import { useLocationStore } from '../stores/location'
import { useOrdersStore } from '../stores/orders'
import { useProfileStore } from '../stores/profile'
import { useRegistrationStore } from '../stores/registration'
import { useSessionStore } from '../stores/session'
import { useTripStore } from '../stores/trip'
import { useWalletStore } from '../stores/wallet'

function idOf(ev: RealtimeEvent, key: string): string | null {
  const v = ev.data[key]
  return typeof v === 'string' && v ? v : null
}

/** Coalesces bursts of hints into one call. */
function debounce(fn: () => void, ms: number): () => void {
  let t: ReturnType<typeof setTimeout> | undefined
  return () => {
    clearTimeout(t)
    t = setTimeout(fn, ms)
  }
}

export function installDriverRuntime(realtime: RealtimeClient, router: Router): () => void {
  const session = useSessionStore()
  const orders = useOrdersStore()
  const trip = useTripStore()
  const wallet = useWalletStore()
  const registration = useRegistrationStore()
  const profile = useProfileStore()
  const duty = useDutyStore()
  const location = useLocationStore()
  const t = (key: string): string => i18n.global.t(key)

  const refreshOrders = debounce(() => {
    if (session.isApproved) void orders.refresh()
  }, 300)
  const refreshTrip = debounce(() => {
    if (session.isApproved) void trip.refresh()
  }, 200)

  function onOrderEvent(ev: RealtimeEvent): void {
    const id = idOf(ev, 'order_id')
    switch (ev.type) {
      case 'order.available':
        refreshOrders()
        return
      case 'order.taken':
        if (id) orders.remove(id)
        return
      case 'order.cancelled':
        if (id) orders.remove(id)
        if (!id || trip.active?.id === id) refreshTrip()
        return
      case 'order.accepted':
        if (id) orders.remove(id)
        refreshTrip()
        return
      default:
        if (!id || !trip.active || trip.active.id === id) refreshTrip()
    }
  }

  async function onModeration(): Promise<void> {
    await session.refreshMe()
    if (registration.registration) await registration.refresh()
    if (profile.profile) await profile.refresh()
  }

  function resyncAll(): void {
    void session.refreshMe()
    if (session.isApproved) {
      void trip.refresh()
      if (orders.loaded) void orders.refresh()
    }
    if (wallet.wallet || wallet.ledger.loaded || wallet.topups.loaded) void wallet.resync()
    if (registration.registration) void registration.refresh()
  }
  const resyncSoon = debounce(resyncAll, 250)

  const offs: Array<() => void> = [
    ...['order.available', 'order.taken', 'order.cancelled', 'order.accepted', 'order.driver_arriving', 'order.started', 'order.stop_completed', 'order.completed', 'order.disputed'].map(
      (type) => realtime.on(type, onOrderEvent),
    ),
    realtime.on('wallet.updated', () => void wallet.resync()),
    realtime.on('topup.updated', (ev) => void wallet.onTopUpEvent(idOf(ev, 'topup_id'))),
    realtime.on('topup.confirmed', (ev) => {
      toast.success(t('wallet.topup_confirmed_toast'))
      void wallet.onTopUpEvent(idOf(ev, 'topup_id'))
    }),
    ...['moderation.submitted', 'moderation.changes_requested', 'moderation.approved', 'moderation.rejected', 'driver.status_changed'].map((type) =>
      realtime.on(type, () => void onModeration()),
    ),
    realtime.on('driver.phone_verified', () => void registration.onPhoneVerified()),
    // Reconnects, the hub's {"type":"resync"} frame and foreground returns.
    realtime.onResync(resyncSoon),
  ]

  // Report the position while online or driving an order.
  offs.push(
    watch(
      () => duty.isOnline || trip.hasActive,
      (on) => location.setReporting(on),
      { immediate: true },
    ),
  )

  // Open orders are computed live, so going online shows the ones already waiting.
  offs.push(
    watch(
      () => duty.isOnline,
      (on) => {
        if (on) refreshOrders()
      },
    ),
  )

  // A moderation decision (or suspension) moves the driver to the right screen.
  offs.push(
    watch(
      () => session.driverState,
      (state, prev) => {
        if (!session.isReady || state === prev) return
        const route = router.currentRoute.value
        if (!canAccess(route.meta.access, state)) void router.replace({ name: entryRoute(state) })
        if (state === 'approved' && prev !== 'approved') void trip.refresh()
      },
    ),
  )

  // An order assigned while the driver looks at another screen (operator
  // assignment). The first load at start-up is not news.
  let primed = false
  offs.push(
    watch(
      () => trip.active?.id ?? null,
      (id, prev) => {
        const name = router.currentRoute.value.name
        if (!primed || !id || id === prev || name === ROUTE.trip || name === ROUTE.order) return
        toast.show({
          message: t('order.assigned_toast'),
          tone: 'success',
          duration: 8000,
          action: { label: t('orders.open_trip'), run: () => void router.push({ name: ROUTE.trip }) },
        })
      },
    ),
  )

  // A trip that ended elsewhere (passenger or operator cancelled) while the trip
  // screen is not open: say so once instead of keeping a stale summary.
  offs.push(
    watch(
      () => trip.finished,
      (o) => {
        if (!o || router.currentRoute.value.name === ROUTE.trip) return
        if (o.status === 'cancelled') {
          const by = o.cancelled_by ? ` ${t(`trip.cancelled_by.${o.cancelled_by}`)}` : ''
          toast.show({ message: `${t('trip.cancelled_title')}.${by}`, duration: 8000 })
        }
        trip.clearFinished()
      },
    ),
  )

  void (session.isApproved ? trip.refresh() : Promise.resolve()).finally(() => (primed = true))

  return () => {
    for (const off of offs) off()
  }
}
