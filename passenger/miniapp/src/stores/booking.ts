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
// * A taxi has no fare input: the order goes out at the chosen class's price
//   (the price on its row, also for routes with a manual destination). Only
//   parcel and other deliveries let the passenger choose the fare.
// * Pickup A can be described in the passenger's own words (setPickupText);
//   A still keeps the pin or GPS coordinates that dispatch measures from. The
//   text stays while only A's point moves (setPickupPoint: the home pin, the
//   map picker, a dragged marker); a place chosen for A replaces it. The
//   default map centre named after a failed GPS attempt is not a point for
//   such a text (pickupFallback).
// * An order asks for the passenger's promotion (§23.6) as the server would
//   apply it: a usable coupon of the order's service by itself (a delivery
//   too, when the coupon covers it, at the fare the passenger chose), else
//   the bonus balance (the services in bonus_categories: taxi) behind the
//   «Бонусты қолдану» switch (on by default); never both. The fare stays what
//   the passenger confirms (a taxi's class price); the discount and the cash
//   price come from the promotions store's data, the server recomputes them.
// * POST /orders carries an idempotency key that stays the same while the
//   payload (promotion included) is the same, so a retry after a lost
//   response replays the original order instead of creating a second one.
// * quote_expired / fare_not_allowed re-quote silently and ask the passenger
//   to confirm again; active_order_exists hands over the existing order;
//   promotion_changed re-reads the promotions and asks to confirm the new
//   price (never re-sent by itself). A promotion the server refused although
//   the fresh data still offers it is left out: a coupon (its campaign's
//   coupons frozen) until GET /promotions changes it (promotions store), the
//   bonus for this booking.

import { defineStore } from 'pinia'
import { computed, ref, shallowRef, watch } from 'vue'
import { reverseGeocode } from '../api/geo'
import { createOrder } from '../api/orders'
import { previewRoute } from '../api/routes'
import type { Order, OrderCreateRequest, PromotionKind, RoutePreviewRequest, RouteQuote, RouteQuoteOption, StopInput } from '../api/types'
import { detailsPayload, isDelivery, validateDetails, type DetailsField, type DetailsInput } from '../domain/details'
import { clampFare, fareAfterRequote, isAllowedFare } from '../domain/fare'
import { createLatest } from '../domain/latest'
import { choosePromotion, couponBelowMinimum, type PromotionOffer } from '../domain/promotions'
import {
  addStop as addStopTo,
  canAddStop,
  clampMaxPoints,
  createStops,
  isComplete,
  MAX_ADDRESS_LENGTH,
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
import { cleanText, codePointLength } from '../utils/text'
import { usePromotionsStore } from './promotions'
import { useSessionStore } from './session'
import { hasCoordinates, roundCoord, isValidLatLon } from '../utils/geo'

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
  /** The server would apply another promotion than the one shown; the passenger confirms the new price. */
  | { kind: 'promotion_changed' }
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

/** Reason the API gives when manual stops were switched off (preview: stops, POST /orders: quote_id). */
export const MANUAL_DISABLED = 'manual_destination_disabled'

/** The API refused the route because manual stops are switched off (§5). */
export function manualRefused(err: unknown): boolean {
  return isApiError(err) && err.code === 'validation_failed' && (err.fields.stops === MANUAL_DISABLED || err.fields.quote_id === MANUAL_DISABLED)
}

/**
 * Stop index → reason from a preview/create error (outside area, duplicates).
 * When manual stops were switched off, every manual stop of `stops` (the
 * route that was sent) gets MANUAL_DISABLED.
 */
export function stopErrorsFrom(err: unknown, stops: readonly RouteStop[] = []): Record<number, string> {
  const out: Record<number, string> = {}
  if (!isApiError(err)) return out
  if (err.code === 'outside_service_area') {
    const i = Number(err.details.index)
    if (Number.isInteger(i) && i >= 0) out[i] = 'outside_service_area'
    return out
  }
  if (err.code === 'validation_failed') {
    if (manualRefused(err)) {
      stops.forEach((s, i) => {
        if (s.place?.source === 'manual') out[i] = MANUAL_DISABLED
      })
    }
    for (const [field, reason] of Object.entries(err.fields)) {
      const m = /^stops\.(\d+)(?:\.|$)/.exec(field)
      if (m) out[Number(m[1])] = reason
    }
  }
  return out
}

export const useBookingStore = defineStore('booking', () => {
  const session = useSessionStore()
  const promotions = usePromotionsStore()

  // --- Route ---------------------------------------------------------------
  const category = ref<Category>('taxi')
  const stops = ref<RouteStop[]>(createStops())
  /** Accuracy of the GPS fix used for the pickup, while the pickup came from GPS. */
  const pickupAccuracy = ref<number | null>(null)
  /**
   * A is only the default map centre the home screen named after the GPS
   * attempt failed (no device position, the map never moved by the passenger,
   * no place chosen): not a point drivers can be sent to for a typed pickup.
   */
  const pickupFallback = ref(false)
  /**
   * The passenger moved the map under the home pin themselves (a drag; a
   * zoom keeps the point), so the pin names their own choice, never the
   * default map centre, for the rest of this booking.
   */
  const pinPlaced = ref(false)
  let pickupRevision = 0

  const maxPoints = computed(() => clampMaxPoints(session.maxRoutePoints))
  const complete = computed(() => isComplete(stops.value))
  const manualDestination = computed(()=>stops.value.some((s,i)=>i>0 && s.place?.source==='manual'))
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
  /** Only deliveries have a fare input; a taxi always costs the chosen class's price. */
  const fareEditable = computed(() => isDelivery(category.value))
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

  // --- Promotion (§23.6) -----------------------------------------------------
  /** «Бонусты қолдану» on the confirmation sheet; on for every new booking. */
  const useBonus = ref(true)
  /** Kinds the server refused for this booking although the fresh promotions still offer them (the bonus). */
  const refusedPromotions = ref<PromotionKind[]>([])
  /** Set after 409 promotion_changed: the passenger checks the new price and confirms again. */
  const promotionChanged = ref(false)

  /** The promotion this booking can use at the fare `at`, before the bonus switch. */
  function promotionAt(at: number | null): PromotionOffer | null {
    return choosePromotion(promotions.usable, category.value, at, promotions.clock, refusedPromotions.value)
  }

  /** What the order would ask for at the fare `at`: the coupon, or the bonus while its switch is on. */
  function appliedAt(at: number | null): PromotionOffer | null {
    const p = promotionAt(at)
    return p && (p.kind === 'coupon' || useBonus.value) ? p : null
  }

  /** The promotion this order can use at its fare. */
  const promotion = computed<PromotionOffer | null>(() => promotionAt(fare.value))
  /** What the order asks for: the coupon, or the bonus while its switch is on. */
  const appliedPromotion = computed<PromotionOffer | null>(() => appliedAt(fare.value))
  /** What the passenger pays the driver: the fare less the applied promotion. */
  const payable = computed(() => (fare.value === null ? null : payableAt(fare.value)))
  /** An active coupon this order is too cheap for ("applies from 400 ₸"). */
  const couponFloor = computed(() => couponBelowMinimum(promotions.usable, category.value, fare.value, promotions.clock))

  /**
   * What the passenger would pay at the fare `at` (another class's price):
   * the fare less the promotion this booking would apply to it.
   */
  function payableAt(at: number): number {
    return at - (appliedAt(at)?.discount ?? 0)
  }

  /**
   * The promotion fields of POST /orders: the applied promotion and the
   * discount shown, or an explicit "none". Nothing while the promotions are
   * unknown (an API without them refuses unknown fields).
   */
  function promotionFields(): Pick<OrderCreateRequest, 'promotion' | 'promotion_discount'> {
    if (!promotions.data) return {}
    const p = appliedPromotion.value
    return p ? { promotion: p.kind, promotion_discount: p.discount } : { promotion: 'none', promotion_discount: 0 }
  }

  // --- Preview -------------------------------------------------------------
  /**
   * A taxi takes the class's price (its row's recommended fare). A delivery
   * keeps the passenger's fare when the class stays the same and the new
   * range allows it.
   */
  function syncFare(opt: RouteQuoteOption | null): void {
    if (!opt) return
    if (!fareEditable.value) {
      fare.value = clampFare(opt.fare, null)
      fareClass = opt.vehicle_class
      return
    }
    if(manualDestination.value) { if (fareClass !== opt.vehicle_class || !isAllowedFare(opt.fare,fare.value)) fare.value=null; fareClass=opt.vehicle_class; return }
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
    stopErrors.value = stopErrorsFrom(err, stops.value)
    if (manualRefused(err)) session.disableManualDestinations()
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
    if(manualDestination.value) {fare.value=null;fareClass=null}
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
    resetPromotion()
  }

  /** A new booking: the bonus switch on again, the bonus no longer refused (refused coupons stay with the promotions store). */
  function resetPromotion(): void {
    useBonus.value = true
    refusedPromotions.value = []
    promotionChanged.value = false
  }

  // --- Route editing -------------------------------------------------------
  function setCategory(next: Category): void {
    category.value = next
  }

  function setStopPlace(index: number, place: PlaceValue | null): void {
    if (place?.source === 'manual' && index === 0) return
    if (index === 0) {
      pickupRevision++
      pickupFallback.value = false
    }
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
    if (signal?.aborted) return
    if (!isValidLatLon(loc)) throw new Error('Invalid GPS coordinates')
    const place: PlaceValue = { lat: roundCoord(loc.lat), lon: roundCoord(loc.lon), address: '', subtitle: null, source: 'gps', inServiceArea: null }
    // Save the fix before the optional address lookup: navigation can abort it.
    setStopPlace(0, place)
    pickupAccuracy.value = loc.accuracy_m
    const revision = pickupRevision
    try {
      const r = await reverseGeocode(loc, { lang: session.language, signal })
      if (signal?.aborted || pickupRevision !== revision) return
      setStopPlace(0, { ...place, address: r.title, subtitle: r.title ? r.subtitle : null, inServiceArea: r.in_service_area })
    } catch (err) {
      if (signal?.aborted || pickupRevision !== revision) return
      if (isApiError(err) && err.code === 'outside_service_area') {
        setStopPlace(0, { ...place, inServiceArea: false })
        throw err
      }
      // Street-name availability must not discard a real fix. The route API
      // still validates service coverage and all coordinates before ordering.
    }
  }

  /**
   * A new point for pickup A from the map: the home screen's pin, «Choose on
   * map» or a dragged marker. A typed A keeps the passenger's text and takes
   * only the point (coordinates, source, locality line, Kazakhstan check);
   * any other A becomes the new place. `fallback` marks the default map
   * centre the home screen names after the GPS attempt failed.
   */
  function setPickupPoint(place: PlaceValue, fallback = false): void {
    const current = stops.value[0]?.place ?? null
    setStopPlace(0, current?.typed === true ? { ...place, address: current.address, typed: true } : place)
    pickupFallback.value = fallback
  }

  /**
   * Pickup A in the passenger's own words, for places the map search does not
   * know («Мешіттің жанында, көк қақпа»). Only the address changes: A keeps
   * the point it already has (the pin, the GPS fix or a found place), else it
   * takes the device position, because drivers are found by radius from A's
   * coordinates and the API checks them against Kazakhstan. The fallback map
   * centre is not such a point. Returns false and changes nothing without a
   * point or for an empty or too long text.
   */
  function setPickupText(text: string, device: DeviceLocation | null = null): boolean {
    const address = cleanText(text)
    if (!address || codePointLength(address) > MAX_ADDRESS_LENGTH) return false
    const current = stops.value[0]?.place ?? null
    if (current && hasCoordinates(current) && !pickupFallback.value) {
      setStopPlace(0, { ...current, address, typed: true })
      return true
    }
    if (!device || !isValidLatLon(device)) return false
    setStopPlace(0, { lat: roundCoord(device.lat), lon: roundCoord(device.lon), address, subtitle: null, source: 'gps', inServiceArea: null, typed: true })
    pickupAccuracy.value = device.accuracy_m
    return true
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
    // A taxi shows no fare field: it always goes at the chosen class's price.
    if (opt && !fareEditable.value) syncFare(opt)
    else if (opt && !isAllowedFare(opt.fare, fare.value)) errors.fare = 'invalid'
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

  /**
   * 409 promotion_changed: the server would apply another promotion (or none)
   * than `sent`. The promotions are read again and the new price waits for
   * the passenger's confirmation. When the fresh data still offers exactly
   * what was refused, the reason is one the app cannot see (the campaign's
   * coupons frozen): that coupon is left out until GET /promotions changes it
   * (later bookings too), the bonus for this booking.
   */
  async function promotionRefused(sent: PromotionOffer | null): Promise<SubmitResult> {
    await promotions.refresh()
    const fresh = appliedPromotion.value
    if (sent && fresh && fresh.kind === sent.kind && fresh.discount === sent.discount && fresh.coupon?.id === sent.coupon?.id) {
      if (fresh.coupon) promotions.refuseCoupon(fresh.coupon)
      else refusedPromotions.value = [...refusedPromotions.value, sent.kind]
    }
    promotionChanged.value = true
    return { kind: 'promotion_changed' }
  }

  async function handleSubmitError(err: unknown, sent: PromotionOffer | null): Promise<SubmitResult> {
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
      case 'promotion_changed':
        return await promotionRefused(sent)
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
        const stopsBad = stopErrorsFrom(err, stops.value)
        if (manualRefused(err)) session.disableManualDestinations()
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
    const sent = appliedPromotion.value
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
        ...promotionFields(),
      }
      const idempotencyKey = idempotencyKeyFor(JSON.stringify(body))
      reconfirm.value = false
      promotionChanged.value = false
      const order = await createOrder({ ...body, idempotency_key: idempotencyKey })
      resetDraft()
      return { kind: 'created', order }
    } catch (err) {
      return await handleSubmitError(err, sent)
    } finally {
      submitting.value = false
    }
  }

  /** Clears the route, load and details (after an order was created). Category and payment stay. */
  function resetDraft(): void {
    pickupRevision++
    runner.cancel()
    stops.value = createStops()
    pickupAccuracy.value = null
    pickupFallback.value = false
    pinPlaced.value = false
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
    resetPromotion()
    attempt = null
  }

  return {
    // route
    category,
    stops,
    pickupAccuracy,
    pickupFallback,
    pinPlaced,
    maxPoints,
    complete,
    manualDestination,
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
    fareEditable,
    paymentMethod,
    fieldErrors,
    submitting,
    reconfirm,
    // promotion
    useBonus,
    promotionChanged,
    promotion,
    appliedPromotion,
    payable,
    couponFloor,
    payableAt,
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
    setPickupPoint,
    setPickupText,
    prefill,
    schedulePreview,
    requote,
    ensureFreshQuote,
    validate,
    submit,
    resetDraft,
  }
})
