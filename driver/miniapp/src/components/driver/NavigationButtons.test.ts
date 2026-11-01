// 2GIS hand-off on the trip: while a remaining stop is manual (the passenger's
// words, no map point) «Маршрут есептелмеген» stays visible, also when 2GIS
// has nothing to open because the driver heads to that stop; the 2GIS buttons
// appear only when there is a known point to drive to.

import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import type { Stop } from '../../api/types'
import { i18n } from '../../i18n'
import NavigationButtons from './NavigationButtons.vue'

const NOTE = 'Маршрут есептелмеген'

function stop(seq: number, kind: Stop['kind'], over: Partial<Stop> = {}): Stop {
  return {
    id: `s${seq}`,
    seq,
    kind,
    mode: 'geocoded',
    lat: 48.829,
    lon: 58.1385,
    address: `Address ${seq}`,
    address_typed: false,
    address_details: null,
    completed_at: null,
    ...over,
  }
}

const pickup = stop(0, 'pickup')
const destination = stop(1, 'destination', { lat: 48.8319, lon: 58.1585 })
const manualDestination = stop(1, 'destination', { mode: 'manual', lat: null, lon: null, address: 'Әженің үйі' })

function mountButtons(remaining: Stop[]) {
  return mount(NavigationButtons, { props: { remaining, position: null }, global: { plugins: [i18n] } })
}

beforeEach(() => {
  setActivePinia(createPinia())
  i18n.global.locale.value = 'kk'
})

describe('NavigationButtons', () => {
  it('heading to a manual stop: the note stays, without 2GIS buttons', () => {
    const w = mountButtons([manualDestination])
    expect(w.find('.nav').exists()).toBe(true)
    expect(w.findAll('.nav__note').map((n) => n.text())).toEqual([NOTE])
    expect(w.findAll('button')).toHaveLength(0)
  })

  it('a manual stop later on: the note and 2GIS to the known stops before it', () => {
    const w = mountButtons([pickup, manualDestination])
    expect(w.findAll('.nav__note').map((n) => n.text())).toContain(NOTE)
    expect(w.find('.nav__main').text()).toBe('2GIS арқылы ашу')
  })

  it('a route with map points only: 2GIS without the note', () => {
    const w = mountButtons([pickup, destination])
    expect(w.find('.nav__main').exists()).toBe(true)
    expect(w.text()).not.toContain(NOTE)
  })

  it('nothing left to drive to: nothing is shown', () => {
    expect(mountButtons([]).find('.nav').exists()).toBe(false)
  })
})
