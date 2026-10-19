// The online card's label, colour and switch come from one confirmed state,
// and the Kazakh labels cannot be misread (the owner read «Желідесіз» — "you
// are online" — as "offline", because -сіз also means "without").

import { describe, expect, it } from 'vitest'
import { i18n } from '../../i18n'
import { LOCALES } from '../../i18n/core'
import { dutyView } from './status'

describe('dutyView', () => {
  it('online: green tone, switch on', () => {
    expect(dutyView({ online: true, switchingTo: null })).toEqual({ tone: 'online', checked: true, title: 'home.online', hint: 'home.online_hint' })
  })

  it('offline: neutral tone, switch off', () => {
    expect(dutyView({ online: false, switchingTo: null })).toEqual({ tone: 'offline', checked: false, title: 'home.offline', hint: 'home.offline_hint' })
  })

  it('a request in flight is pending; the switch keeps the confirmed state', () => {
    expect(dutyView({ online: false, switchingTo: true })).toMatchObject({ tone: 'pending', checked: false, title: 'home.going_online' })
    expect(dutyView({ online: true, switchingTo: false })).toMatchObject({ tone: 'pending', checked: true, title: 'home.going_offline' })
  })

  it('repeating the current state is not pending', () => {
    expect(dutyView({ online: true, switchingTo: true }).tone).toBe('online')
  })

  it('every key it returns exists in kk, ru and en', () => {
    const keys = new Set<string>()
    for (const online of [true, false]) {
      for (const switchingTo of [null, true, false]) {
        const v = dutyView({ online, switchingTo })
        keys.add(v.title).add(v.hint)
      }
    }
    const missing = LOCALES.flatMap((l) => [...keys].filter((k) => !i18n.global.te(k, l)).map((k) => `${l}:${k}`))
    expect(missing).toEqual([])
  })

  it('uses unambiguous Kazakh labels', () => {
    const kk = (k: string) => i18n.global.t(k, {}, { locale: 'kk' })
    expect(kk('home.online')).toBe('Желіде')
    expect(kk('home.offline')).toBe('Желіде емес')
    for (const key of ['home.online', 'home.offline', 'home.going_online', 'duty.without_location']) {
      expect(kk(key)).not.toMatch(/Желідесіз|тыссыз/)
    }
  })
})
