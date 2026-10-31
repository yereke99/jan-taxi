// After «Тіркелуді аяқтау» the API has already approved the driver (the
// automatic checks run in the same request), so the app takes them straight
// to work: a fresh GPS fix is sent (useEnsureLocation) and then the driver
// goes online (PATCH /driver/availability), while the Orders tab opens.
//
// Orders opens at once rather than after the GPS fix: the realtime
// moderation.approved hint would otherwise move the driver to Home first, and
// the online card on Orders shows the switch going through. Nothing here
// blocks: without location, or when going online is refused (balance,
// outside Kazakhstan), the driver is on Orders, where the usual location and
// online prompts take over.

import { nextTick } from 'vue'
import { useRouter } from 'vue-router'
import type { DriverRegistration } from '../../api/types'
import { ROUTE } from '../../router/names'
import { useDutyStore } from '../../stores/duty'
import { useSessionStore } from '../../stores/session'
import { useEnsureLocation } from '../location/useEnsureLocation'

export function useStartWork(): (r: DriverRegistration) => Promise<void> {
  const router = useRouter()
  const session = useSessionStore()
  const duty = useDutyStore()
  const gps = useEnsureLocation()

  async function goOnline(): Promise<void> {
    if (await gps.ensure()) await duty.setOnline(true)
  }

  return async (r) => {
    if (r.status !== 'approved' || !r.id) {
      // Not approved after all (an older API): the session moves the driver to the right screen.
      await session.refreshMe()
      return
    }
    session.adoptDriver({ id: r.id, code: r.code ?? '', status: 'approved', is_online: false, city: r.city })
    const online = goOnline().catch(() => undefined)
    // The runtime's own redirect for the new status (Home) runs first; Orders is the last word.
    await nextTick()
    await router.replace({ name: ROUTE.orders })
    await online
    // Read back what the API holds now (started after the switch, so it cannot undo it).
    await session.refreshMe()
  }
}
