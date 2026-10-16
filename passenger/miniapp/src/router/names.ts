// Route names of the passenger app.

export const ROUTE = {
  home: 'home',
  book: 'book',
  bookSearch: 'book-search',
  bookPick: 'book-pick',
  confirm: 'confirm',
  order: 'order',
  orders: 'orders',
  orderDetail: 'order-detail',
  support: 'support',
  becomeDriver: 'become-driver',
  language: 'language',
  profile: 'profile',
} as const

export type RouteName = (typeof ROUTE)[keyof typeof ROUTE]
