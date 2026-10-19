import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { i18n } from '../../i18n'
import CancelReasonsCard from './CancelReasonsCard.vue'

beforeEach(() => {
  i18n.global.locale.value = 'en'
})

afterEach(() => {
  i18n.global.locale.value = 'kk'
})

describe('CancelReasonsCard', () => {
  it('lists the passenger reasons with counts and shares', () => {
    const w = mount(CancelReasonsCard, {
      props: { counts: { long_wait: 6, driver_issue: 2, vehicle_issue: 0, changed_plans: 1, other: 1, unspecified: 0 } },
      global: { plugins: [i18n] },
    })
    const rows = w.findAll('.reasons__row').map((r) => [r.find('.reasons__label').text(), r.find('.reasons__n').text(), r.find('.reasons__share').text()])
    expect(rows).toEqual([
      ['Waited too long', '6', '60%'],
      ["Didn't like the driver", '2', '20%'],
      ["Didn't like the car", '0', '0%'],
      ['My plans changed', '1', '10%'],
      ['Other', '1', '10%'],
    ])
    expect(w.text()).toContain('Cancelled by passengers: 10')
  })

  it('shows older cancellations without a reason only when there are some', () => {
    const w = mount(CancelReasonsCard, {
      props: { counts: { long_wait: 0, driver_issue: 0, vehicle_issue: 0, changed_plans: 0, other: 0, unspecified: 3 } },
      global: { plugins: [i18n] },
    })
    expect(w.text()).toContain('No reason given')
  })

  it('says so when nobody cancelled', () => {
    const w = mount(CancelReasonsCard, {
      props: { counts: { long_wait: 0, driver_issue: 0, vehicle_issue: 0, changed_plans: 0, other: 0, unspecified: 0 } },
      global: { plugins: [i18n] },
    })
    expect(w.text()).toContain('No passenger cancellations in this period.')
  })
})
