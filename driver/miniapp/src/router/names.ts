// Route names of the driver app (start parameters map onto these, §18).

export const ROUTE = {
  home: 'home',
  register: 'register',
  status: 'status',
  orders: 'orders',
  order: 'order',
  trip: 'trip',
  history: 'history',
  wallet: 'wallet',
  topup: 'topup',
  topupDetail: 'topup-detail',
  profile: 'profile',
  language: 'language',
  support: 'support',
} as const

export type RouteName = (typeof ROUTE)[keyof typeof ROUTE]
