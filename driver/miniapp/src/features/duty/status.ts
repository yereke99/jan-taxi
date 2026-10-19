// What the online card shows. The switch, the title and its colour all come
// from the availability the API last confirmed (never an optimistic flip); a
// request in flight is shown as its own neutral state instead.

export type DutyTone = 'online' | 'offline' | 'pending'

export interface DutyView {
  tone: DutyTone
  /** Switch position: always the confirmed state. */
  checked: boolean
  /** i18n keys. */
  title: string
  hint: string
}

export interface DutyState {
  /** Confirmed by the API (PATCH /driver/availability, GET /me, driver.status_changed). */
  online: boolean
  /** Target of the request in flight, or null. */
  switchingTo: boolean | null
}

export function dutyView(s: DutyState): DutyView {
  const hint = s.online ? 'home.online_hint' : 'home.offline_hint'
  if (s.switchingTo !== null && s.switchingTo !== s.online) {
    return { tone: 'pending', checked: s.online, title: s.switchingTo ? 'home.going_online' : 'home.going_offline', hint }
  }
  return s.online
    ? { tone: 'online', checked: true, title: 'home.online', hint }
    : { tone: 'offline', checked: false, title: 'home.offline', hint }
}
