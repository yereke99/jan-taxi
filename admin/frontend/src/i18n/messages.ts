// Composes the per-area message files into one tree per locale. Each area file
// holds kk, ru and en side by side; Kazakh defines the schema.

import { base } from './areas/base'
import { dashboard } from './areas/dashboard'
import { drivers } from './areas/drivers'
import { finance } from './areas/finance'
import { orders } from './areas/orders'
import { settingsArea } from './areas/settings'
import { system } from './areas/system'
import { configArea } from './areas/config'

function compose(locale: 'kk' | 'ru' | 'en') {
  return {
    ...base[locale],
    dashboard: dashboard[locale],
    orders: orders[locale],
    drivers: drivers[locale],
    finance: finance[locale],
    config: configArea[locale],
    settings: settingsArea[locale],
    system: system[locale],
  }
}

export const kk = compose('kk')
export const ru = compose('ru')
export const en = compose('en')

export type AdminMessages = typeof kk
