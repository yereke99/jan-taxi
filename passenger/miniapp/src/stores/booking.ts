// Booking flow state shared by the route editor, the place search, the map
// picker and the confirmation sheet, so moving between them (and "back to
// route") never loses anything.
//
// * Every change of the category, the stops or the load (passenger count for
//   taxi, cargo size for deliveries, §21) re-quotes the route after a 400 ms
//   pause (POST /routes/preview); older requests are aborted and an outdated
//   answer can never replace a newer one (domain/latest).
// * A quote lists one option per vehicle class. Every booking starts with the
//   default option (the cheapest class able to carry the load). A class the
//   passenger taps belongs to the route it was tapped for: a new route (or a
//   new booking from the home screen) starts with the default again, and an
//   earlier pick never comes back; only "order again" repeats a past order's
//   class. Within the route the pick is kept as a preference: when a re-quote
//   for another load no longer offers it, the default is used until it is
//   offered again. The fare belongs to the chosen class and restarts at that
//   class's recommendation when it changes.
// * POST /orders carries an idempotency key that stays the same while the
//   payload is the same, so a retry after a lost response replays the
//   original order instead of creating a second one.
// * quote_expired / fare_not_allowed re-quote silently and ask the passenger
//   to confirm again; active_order_exists hands over the existing order.

import { defineStore } from 'pinia'
import { computed, ref, shallowRef, watch } from 'vue'
import { reverseGeocode } from '../api/geo'
import { createOrder } from '../api/orders'
import { previewRoute } from '../api/routes'
import type { Order, RoutePreviewRequest, RouteQuote, RouteQuoteOption, StopInput } from '../api/types'
import { detailsPayload, isDelivery, validateDetails, type DetailsField, type DetailsInput } from '../domain/details'
import { clampFare, fareAfterRequote, isAllowedFare } from '../domain/fare'
import { createLatest } from '../domain/latest'
import {
  addStop as addStopTo,
  canAddStop,
  clampMaxPoints,
  createStops,
  isComplete,
  moveStop as moveStopIn,
  outsideIndices,
  removeEmptyStops,
  removeStop as removeStopFrom,
  routeFingerprint,
  setPlace,
  stopsFromPlaces,
  toStopInputs,
  type PlaceValue,
  type RouteStop,
} from '../domain/route'
import { clampPassengers, DEFAULT_CARGO_SIZE, loadProblem, maxPassengers, MIN_PASSENGERS, pickOption } from '../domain/vehicleClass'
import { i18n } from '../i18n'
import type { CargoSize } from '../services/config'
import { isApiError, newIdempotencyKey } from '../services/http'
import type { DeviceLocation } from '../telegram'
import type { Category, PaymentMethod } from '../utils/status'
import { useSessionStore } from './session'

export const PREVIEW_DEBOUNCE_MS = 400
/** A quote this close to its expiry is renewed before it is used. */
export const QUOTE_REFRESH_MARGIN_MS = 20_000

export type PreviewStatus = 'idle' | 'loading' | 'ready' | 'error'

export type SubmitResult =
  | { kind: 'created'; order: Order }
  | { kind: 'active_exists'; orderId: string }
  /** A new quote replaced the old one; the passenger confirms again. */
  | { kind: 'requoted' }
  /** Form fields need attention (see fieldErrors). */
  | { kind: 'invalid' }
  /** The stops need changes (see stopErrors / previewError). */
  | { kind: 'route_invalid' }
  | { kind: 'busy' }
  | { kind: 'failed'; error: unknown }

export type FormField = DetailsField | 'fare' | 'payment_method'

interface PreviewInput {
  key: string
  /** routeKey when the input was taken. */
  route: string
  category: Category
  stops: StopInput[]
  passengerCount?: number
  cargoSize?: CargoSize
}

/** Load fields fixed by a booking started from an earlier order ("order again"). */
export interface LoadPrefill {
  vehicleClass?: string | null
  passengerCount?: number | null
  cargoSize?: CargoSize | null
}

function quoteExpiring(q: Pick<RouteQuote, 'expires_at'>, now = Date.now()): boolean {
  const t = Date.parse(q.expires_at)
  return Number.isFinite(t) && t - now < QUOTE_REFRESH_MARGIN_MS
}

/** Stop index → reason from a preview/create error (outside area, duplicates). */
export function stopErrorsFrom(err: unknown): Record<number, string> {
  const out: Record<number, string> = {}
  if (!isApiError(err)) return out
  if (err.code === 'outside_service_area') {
    const i = Number(err.details.index)
    if (Number.isInteger(i) && i >= 0) out[i] = 'outside_service_area'
    return out
  }
  if (err.code === 'validation_failed') {
    for (const [field, reason] of Object.entries(err.fields)) {
      const m = /^stops\.(\d+)(?:\.|$)/.exec(field)
      if (m) out[Number(m[1])] = reason
    }
  }
  return out
}

export const useBookingStore = defineStore('booking', () => {
  const session = useSessionStore()

  // --- Route ---------------------------------------------------------------
  const category = ref<Category>('taxi')
  const stops = ref<RouteStop[]>(createStops())
  /** Accuracy of the GPS fix used for the pickup, while the pickup came from GPS. */
  const pickupAccuracy = ref<number | null>(null)

  const maxPoints = computed(() => clampMaxPoints(session.maxRoutePoints))
  const complete = computed(() => isComplete(stops.value))
  const canAdd = computed(() => canAddStop(stops.value, maxPoints.value))
  const routeKey = computed(() => `${category.value}|${routeFingerprint(stops.value)}`)

  // --- Load and vehicle class (§21) ------------------------------------------
  const passengerCount = ref(MIN_PASSENGERS)
  const cargoSize = ref<CargoSize>(DEFAULT_CARGO_SIZE)
  /** The class the passenger tapped and the route it was tapped for. */
  const classChoice = shallowRef<{ code: string; route: string } | null>(null)
  /** The class the passenger picked for this route; null = the default option. */
  const vehicleClass = computed(() => (classChoice.value && classChoice.value.route === routeKey.value ? classChoice.value.code : null))
  const passengerLimit = computed(() => maxPassengers(session.config))
  const loadKey = computed(() => (isDelivery(category.value) ? `cargo:${cargoSize.value}` : `passengers:${passengerCount.value}`))
  /** Everything a quote depends on: route, category and load. */
  const quoteInputKey = computed(() => `${routeKey.value}|${loadKey.value}`)

  // --- Quote ---------------------------------------------------------------
  const quote = shallowRef<RouteQuote | null>(null)
  const quoteKey = ref('')
  /** routeKey of the quote shown (the load may have changed since). */
  const quoteRouteKey = ref('')
  const previewStatus = ref<PreviewStatus>('idle')
  const previewError = shallowRef<unknown>(null)
  /** API problems per stop index ('outside_service_area', 'duplicate', …). */
  const stopErrors = ref<Record<number, string>>({})

  /** Stops the passenger must change before the route can be quoted. */
  const blockedStops = computed(() => {
    const set = new Set<number>(outsideIndices(stops.value))
    for (const k of Object.keys(stopErrors.value)) set.add(Number(k))
    return [...set].sort((a, b) => a - b)
  })
  /** The quote matches the current stops, category and load. */
  const quoteCurrent = computed(() => quote.value !== null && quoteKey.value === quoteInputKey.value)
  /** The quote is for the current stops and category; only the load may differ (re-quote pending). */
  const routeCurrent = computed(() => quote.value !== null && quoteRouteKey.value === routeKey.value)
  /** The class option the order will be placed with. */
  const selectedOption = computed<RouteQuoteOption | null>(() => pickOption(quote.value, vehicleClass.value))
  /**
   * The preview failed only because no class carries the load (too many
   * passengers, cargo too large); the confirmation screen is where the load
   * is changed, so the passenger may go on to it.
   */
  const loadBlocked = computed(
    () => previewStatus.value === 'error' && blockedStops.value.length === 0 && loadProblem(previewError.value) !== null,
  )
  const canContinue = computed(
    () => (quoteCurrent.value && previewStatus.value === 'ready' && blockedStops.value.length === 0) || loadBlocked.value,
  )

  // --- Details -------------------------------------------------------------
  const comment = ref('')
  const cargoDescription = ref('')
  const recipientPhone = ref('')
  const prohibitedItemsAck = ref(false)
  const fare = ref<number | null>(null)
  const paymentMethod = ref<PaymentMethod>('cash')
  const fieldErrors = ref<Partial<Record<FormField, string>>>({})
  const submitting = ref(false)
  /** Set after a silent re-quote: the passenger must review and confirm again. */
  const reconfirm = ref(false)
  let attempt: { signature: string; key: string } | null = null
  /** The class the current fare was chosen for. */
  let fareClass: string | null = null

  const detailsInput = computed<DetailsInput>(() => ({
    category: category.value,
    comment: comment.value,
    cargoDescription: cargoDescription.value,
    recipientPhone: recipientPhone.value,
    prohibitedItemsAck: prohibitedItemsAck.value,
  }))

  // --- Preview -------------------------------------------------------------
  /** Keeps the fare when the class stays the same and the new range allows it. */
  function syncFare(opt: RouteQuoteOption | null): void {
    if (!opt) return
    fare.value = fareClass === opt.vehicle_class ? fareAfterRequote(fare.value, opt.fare) : clampFare(opt.fare, null)
    fareClass = opt.vehicle_class
  }

  function applyQuote(q: RouteQuote, input: PreviewInput): void {
    quote.value = q
    quoteKey.value = input.key
    quoteRouteKey.value = input.route
    previewStatus.value = 'ready'
    previewError.value = null
    stopErrors.value = {}
    syncFare(selectedOption.value)
    const methods = q.payment_methods
    if (methods.length > 0 && !methods.includes(paymentMethod.value)) paymentMethod.value = methods[0]!
  }

  function applyPreviewError(err: unknown): void {
    previewStatus.value = 'error'
    previewError.value = err
    stopErrors.value = stopErrorsFrom(err)
  }

  const runner = createLatest<PreviewInput, RouteQuote>({
    delayMs: PREVIEW_DEBOUNCE_MS,
    run: (input, signal) => previewRoute(previewRequest(input), signal),
    onStart: () => {
      previewStatus.value = 'loading'
    },
    onResult: (q, input) => {
      if (input.key === quoteInputKey.value) applyQuote(q, input)
    },
    onError: (err, input) => {
      if (input.key === quoteInputKey.value) applyPreviewError(err)
    },
  })

  function previewRequest(input: PreviewInput): RoutePreviewRequest {
    return {
      category: input.category,
      stops: input.stops,
      ...(input.passengerCount !== undefined ? { passenger_count: input.passengerCount } : {}),
      ...(input.cargoSize !== undefined ? { cargo_size: input.cargoSize } : {}),
    }
  }

  function previewInput(): PreviewInput | null {
    const inputs = toStopInputs(stops.value, i18n.global.t('common.point_on_map'))
    if (!inputs || outsideIndices(stops.value).length > 0) return null
    const load = isDelivery(category.value) ? { cargoSize: cargoSize.value } : { passengerCount: passengerCount.value }
    return { key: quoteInputKey.value, route: routeKey.value, category: category.value, stops: inputs, ...load }
  }

  /** Quotes the current route after the debounce (or now); clears it while incomplete. */
  function schedulePreview(immediate = false): void {
    const input = previewInput()
    if (!input) {
      runner.cancel()
      quote.value = null
      quoteKey.value = ''
      quoteRouteKey.value = ''
      previewStatus.value = 'idle'
      previewError.value = null
      return
    }
    if (quote.value && quoteKey.value === input.key && !quoteExpiring(selectedOption.value ?? quote.value)) {
      runner.cancel()
      previewStatus.value = 'ready'
      return
    }
    previewStatus.value = 'loading'
    runner.schedule(input, { immediate })
  }

  /** Re-quotes now and waits for the answer; false when the route cannot be quoted. */
  async function requote(): Promise<boolean> {
    runner.cancel()
    const input = previewInput()
    if (!input) return false
    previewStatus.value = 'loading'
    try {
      const q = await previewRoute(previewRequest(input))
      if (input.key !== quoteInputKey.value) return false
      applyQuote(q, input)
      return true
    } catch (err) {
      if (input.key === quoteInputKey.value) applyPreviewError(err)
      return false
    }
  }

  /** Renews a quote that is about to expire (confirmation screen). */
  function ensureFreshQuote(): void {
    if (quote.value && quoteCurrent.value && quoteExpiring(selectedOption.value ?? quote.value)) {
      quoteKey.value = ''
      schedulePreview(true)
    }
  }

  watch(quoteInputKey, () => schedulePreview())
  // A new route is a new booking: an earlier pick never comes back, even if
  // the passenger returns to the same addresses later.
  watch(routeKey, (key) => {
    if (classChoice.value && classChoice.value.route !== key) classChoice.value = null
  })

  // --- Load and class editing ------------------------------------------------
  function setPassengerCount(n: number): void {
    passengerCount.value = clampPassengers(n, passengerLimit.value)
  }

  function setCargoSize(size: CargoSize): void {
    cargoSize.value = size
  }

  /** Picks one of the quote's class options; the fare restarts at its recommendation. */
  function setVehicleClass(code: string): void {
    if (!quote.value?.options.some((o) => o.vehicle_class === code)) return
    classChoice.value = { code, route: routeKey.value }
    syncFare(selectedOption.value)
  }

  /**
   * A new booking starts (home screen): the class and the fare go back to the
   * default option; the stops stay so the passenger can reuse them.
   */
  function startNewBooking(): void {
    classChoice.value = null
    fareClass = null
    fare.value = null
    syncFare(selectedOption.value)
    reconfirm.value = false
  }

  // --- Route editing -------------------------------------------------------
  function setCategory(next: Category): void {
    category.value = next
  }

  function setStopPlace(index: number, place: PlaceValue | null): void {
    stops.value = setPlace(stops.value, index, place)
    if (index === 0 && place?.source !== 'gps') pickupAccuracy.value = null
    if (stopErrors.value[index] !== undefined) {
      const next = { ...stopErrors.value }
      delete next[index]
      stopErrors.value = next
    }
  }

  function addStop(): boolean {
    const next = addStopTo(stops.value, maxPoints.value)
    if (next === stops.value) return false
    stops.value = next
    return true
  }

  function removeStop(index: number): void {
    stops.value = removeStopFrom(stops.value, index)
    stopErrors.value = {}
  }

  /** Removes intermediate stops left empty (added, then the search was closed). */
  function pruneEmptyStops(): void {
    const next = removeEmptyStops(stops.value)
    if (next === stops.value) return
    stops.value = next
    stopErrors.value = {}
  }

  function moveStop(from: number, to: number): void {
    stops.value = moveStopIn(stops.value, from, to)
    stopErrors.value = {}
  }

  /** Fills the pickup from a GPS fix (reverse geocoded; coordinates when that fails). */
  async function setPickupFromLocation(loc: DeviceLocation, signal?: AbortSignal): Promise<void> {
    let place: PlaceValue
    try {
      const r = await reverseGeocode(loc, { lang: session.language, signal })
      place = { lat: r.lat, lon: r.lon, address: r.title, subtitle: r.title ? r.subtitle : null, source: 'gps', inServiceArea: r.in_service_area }
    } catch (err) {
      if (signal?.aborted) return
      if (isApiError(err) && !err.isNetworkError && err.code !== 'too_many_requests' && err.code !== 'internal') throw err
      // Unnamed point: the UI shows "Point on the map", never coordinates.
      place = { lat: loc.lat, lon: loc.lon, address: '', subtitle: null, source: 'gps', inServiceArea: null }
    }
    setStopPlace(0, place)
    pickupAccuracy.value = loc.accuracy_m
  }

  /** Starts a new booking with the given category and places ("order again", recent place). */
  function prefill(next: Category, places: readonly PlaceValue[], load: LoadPrefill = {}): void {
    resetDraft()
    category.value = next
    stops.value = stopsFromPlaces(places, maxPoints.value)
    if (load.passengerCount) setPassengerCount(load.passengerCount)
    if (load.cargoSize) cargoSize.value = load.cargoSize
    // "Order again" is the one case where an earlier class is repeated.
    if (load.vehicleClass) classChoice.value = { code: load.vehicleClass, route: routeKey.value }
  }

  // --- Details editing -----------------------------------------------------
  function clearFieldError(field: FormField): void {
    if (fieldErrors.value[field] === undefined) return
    const next = { ...fieldErrors.value }
    delete next[field]
    fieldErrors.value = next
  }

  watch(comment, () => clearFieldError('comment'))
  watch(cargoDescription, () => clearFieldError('cargo_description'))
  watch(recipientPhone, () => clearFieldError('recipient_phone'))
  watch(prohibitedItemsAck, () => clearFieldError('prohibited_items_ack'))
  watch(fare, () => clearFieldError('fare'))
  watch(category, () => {
    fieldErrors.value = {}
  })

  /** Validates the details locally; true when the form can be submitted. */
  function validate(): boolean {
    const errors: Partial<Record<FormField, string>> = validateDetails(detailsInput.value)
    const opt = selectedOption.value
    if (opt && !isAllowedFare(opt.fare, fare.value)) errors.fare = 'invalid'
    fieldErrors.value = errors
    return Object.keys(errors).length === 0
  }

  function idempotencyKeyFor(signature: string): string {
    if (!attempt || attempt.signature !== signature) attempt = { signature, key: newIdempotencyKey() }
    return attempt.key
  }

  function applyFieldErrors(fields: Readonly<Record<string, string>>): boolean {
    const known: FormField[] = ['comment', 'cargo_description', 'recipient_phone', 'prohibited_items_ack', 'fare', 'payment_method']
    const next: Partial<Record<FormField, string>> = {}
    for (const f of known) if (fields[f]) next[f] = fields[f]
    fieldErrors.value = next
    return Object.keys(next).length > 0
  }

  async function handleSubmitError(err: unknown): Promise<SubmitResult> {
    if (!isApiError(err)) return { kind: 'failed', error: err }
    switch (err.code) {
      case 'quote_expired':
      case 'not_found':
      case 'fare_not_allowed':
        if (await requote()) {
          reconfirm.value = true
          return { kind: 'requoted' }
        }
        return { kind: 'route_invalid' }
      case 'conflict': {
        const reason = err.details.reason
        const activeId = err.details.active_order_id
        if (reason === 'active_order_exists' && typeof activeId === 'string') {
          attempt = null
          return { kind: 'active_exists', orderId: activeId }
        }
        // idempotency_key_reused: the next attempt gets a fresh key.
        attempt = null
        return { kind: 'failed', error: err }
      }
      case 'validation_failed': {
        const stopsBad = stopErrorsFrom(err)
        if (Object.keys(stopsBad).length > 0) {
          stopErrors.value = stopsBad
          return { kind: 'route_invalid' }
        }
        if (err.fields.quote_id && (await requote())) {
          reconfirm.value = true
          return { kind: 'requoted' }
        }
        return applyFieldErrors(err.fields) ? { kind: 'invalid' } : { kind: 'failed', error: err }
      }
      case 'outside_service_area':
        stopErrors.value = stopErrorsFrom(err)
        return { kind: 'route_invalid' }
      default:
        return { kind: 'failed', error: err }
    }
  }

  /** Validates and sends POST /orders. */
  async function submit(): Promise<SubmitResult> {
    if (submitting.value) return { kind: 'busy' }
    if (!validate()) return { kind: 'invalid' }
    const q = selectedOption.value
    if (!q || !quoteCurrent.value || blockedStops.value.length > 0) return { kind: 'route_invalid' }
    submitting.value = true
    try {
      if (quoteExpiring(q)) {
        if (await requote()) {
          reconfirm.value = true
          return { kind: 'requoted' }
        }
        return { kind: 'route_invalid' }
      }
      const body = {
        quote_id: q.quote_id,
        fare: fare.value!,
        payment_method: paymentMethod.value,
        ...detailsPayload(detailsInput.value),
      }
      const idempotencyKey = idempotencyKeyFor(JSON.stringify(body))
      reconfirm.value = false
      const order = await createOrder({ ...body, idempotency_key: idempotencyKey })
      resetDraft()
      return { kind: 'created', order }
    } catch (err) {
      return await handleSubmitError(err)
    } finally {
      submitting.value = false
    }
  }

  /** Clears the route, load and details (after an order was created). Category and payment stay. */
  function resetDraft(): void {
    runner.cancel()
    stops.value = createStops()
    pickupAccuracy.value = null
    quote.value = null
    quoteKey.value = ''
    quoteRouteKey.value = ''
    previewStatus.value = 'idle'
    previewError.value = null
    stopErrors.value = {}
    comment.value = ''
    cargoDescription.value = ''
    recipientPhone.value = ''
    prohibitedItemsAck.value = false
    fare.value = null
    fareClass = null
    passengerCount.value = MIN_PASSENGERS
    cargoSize.value = DEFAULT_CARGO_SIZE
    classChoice.value = null
    fieldErrors.value = {}
    reconfirm.value = false
    attempt = null
  }

  return {
    // route
    category,
    stops,
    pickupAccuracy,
    maxPoints,
    complete,
    canAdd,
    routeKey,
    // load and class
    passengerCount,
    cargoSize,
    vehicleClass,
    passengerLimit,
    selectedOption,
    // quote
    quote,
    previewStatus,
    previewError,
    stopErrors,
    blockedStops,
    quoteCurrent,
    routeCurrent,
    loadBlocked,
    canContinue,
    // details
    comment,
    cargoDescription,
    recipientPhone,
    prohibitedItemsAck,
    fare,
    paymentMethod,
    fieldErrors,
    submitting,
    reconfirm,
    // actions
    setCategory,
    setPassengerCount,
    setCargoSize,
    setVehicleClass,
    startNewBooking,
    setStopPlace,
    addStop,
    removeStop,
    pruneEmptyStops,
    moveStop,
    setPickupFromLocation,
    prefill,
    schedulePreview,
    requote,
    ensureFreshQuote,
    validate,
    submit,
    resetDraft,
  }
})
