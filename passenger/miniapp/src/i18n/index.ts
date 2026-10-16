import { createI18nInstance } from './core'
import { en } from './messages/en'
import { kk } from './messages/kk'
import { ru } from './messages/ru'

/** The passenger app's i18n instance (core + passenger messages). */
export const i18n = createI18nInstance({ kk, ru, en })
