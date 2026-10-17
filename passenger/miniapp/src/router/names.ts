// Route names of the passenger app.

export const ROUTE = {
  home: 'home',
  book: 'book',
  bookSearch: 'book-search',
  bookPick: 'book-pick',
  confirm: 'confirm',
  order: 'order',
  trip: 'trip',
  orders: 'orders',
  orderDetail: 'order-detail',
  support: 'support',
  becomeDriver: 'become-driver',
  language: 'language',
  profile: 'profile',
} as const

export type RouteName = (typeof ROUTE)[keyof typeof ROUTE]

/**
 * Query value of a place search / map pick opened straight from home: when a
 * place is chosen the step is replaced by the route editor (so "back" from
 * the editor returns home); "back" without a choice returns home.
 */
export const BOOKING_FROM_HOME = 'home'
