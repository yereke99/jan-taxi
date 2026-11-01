// The stop list shows the passenger's own words exactly as written and marks
// every such stop «Жолаушы көрсеткен орын»: a manual stop and a pickup A the
// passenger typed over its GPS or pin point.

import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it } from 'vitest'
import { normalizeDetail } from '../../api/orders'
import { i18n } from '../../i18n'
import StopList from './StopList.vue'

const LABEL = 'Жолаушы көрсеткен орын'
const COORDS = '48.826400, 58.144200'

function stops(pickup: Record<string, unknown> = {}) {
  return normalizeDetail({
    id: 'o1',
    stops: [
      { id: 's1', seq: 0, kind: 'pickup', mode: 'geocoded', lat: 48.8264, lon: 58.1442, address: 'Мешіттің жанында, көк қақпа', address_typed: true, address_details: '2-кіреберіс', completed_at: null, ...pickup },
      { id: 's2', seq: 1, kind: 'intermediate', mode: 'geocoded', lat: 48.8249, lon: 58.1437, address: 'Ембі базары', address_typed: false, address_details: null, completed_at: null },
      { id: 's3', seq: 2, kind: 'destination', mode: 'manual', lat: null, lon: null, address: 'Әженің үйі', address_typed: false, address_details: null, completed_at: null },
    ],
  }).stops
}

function mountList(list = stops()) {
  return mount(StopList, { props: { stops: list }, global: { plugins: [i18n] } })
}

beforeEach(() => {
  i18n.global.locale.value = 'kk'
})

describe('StopList', () => {
  it('marks every stop the passenger wrote, a typed pickup A included', () => {
    const items = mountList().findAll('.stops__item')
    expect(items.map((li) => li.find('.stops__addr').text())).toEqual(['Мешіттің жанында, көк қақпа', 'Ембі базары', 'Әженің үйі'])
    expect(items.map((li) => li.text().includes(LABEL))).toEqual([true, false, true])
    // The label comes right after the address, before the passenger's details.
    expect(items[0]!.findAll('.stops__details').map((d) => d.text())).toEqual([LABEL, '2-кіреберіс'])
  })

  it("shows the passenger's words verbatim, other unnamed points as a point on the map", () => {
    const typed = mountList(stops({ address: COORDS })).findAll('.stops__addr')
    expect(typed[0]!.text()).toBe(COORDS)
    const geocoded = mountList(stops({ address: COORDS, address_typed: false })).findAll('.stops__item')
    expect(geocoded[0]!.find('.stops__addr').text()).toBe('Картадағы нүкте')
    expect(geocoded[0]!.text()).not.toContain(LABEL)
  })
})
