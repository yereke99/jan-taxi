<!--
  Details and confirmation: category-specific fields, the fare picker, the
  payment method and a summary of the whole order. "Back to route" keeps
  everything (the booking store holds the draft). Confirm sends POST /orders
  with an idempotency key that stays the same on retries.
-->
<script setup lang="ts">
import { ChevronRight, Circle, CircleCheck, Info } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import FarePicker from '../../components/booking/FarePicker.vue'
import { JButton, JSheet, JSpinner, JTextArea, JTextField, showActionSheet, toast, type SheetSnap } from '../../components/ui'
import { isDelivery } from '../../domain/details'
import { prohibitedItemKeys } from '../../domain/prohibited'
import { useErrorText, useFieldErrorText } from '../../i18n/core'
import { STOP_LETTERS } from '../../maps'
import { ROUTE } from '../../router/names'
import { useAutoFit, useStageScreen } from '../../stage/mapStage'
import { useBookingStore, type FormField } from '../../stores/booking'
import { useOrdersStore } from '../../stores/orders'
import { useSessionStore } from '../../stores/session'
import { haptics, navigateBack } from '../../telegram'
import { formatDistance, formatDuration, formatMoney } from '../../utils/format'
import type { LatLon } from '../../utils/geo'
import { formatKzPhoneInput } from '../../utils/phone'
import { CATEGORIES, type Category, type PaymentMethod } from '../../utils/status'
import { TEXT_MAX } from '../../utils/text'

const QUOTE_CHECK_MS = 10_000

const { t } = useI18n()
const errorText = useErrorText()
const fieldErrorText = useFieldErrorText()
const router = useRouter()
const stage = useStageScreen()
stage.controls.back = true
const booking = useBookingStore()
const orders = useOrdersStore()
const session = useSessionStore()

const snap = ref<SheetSnap>('half')
const contentEl = ref<HTMLElement | null>(null)
const lang = computed(() => session.language)
const quote = computed(() => (booking.quoteCurrent ? booking.quote : null))
const delivery = computed(() => isDelivery(booking.category))
const prohibitedKeys = computed(() => prohibitedItemKeys(session.config.delivery.prohibited_items))
const placedStops = computed(() => booking.stops.flatMap((s, i) => (s.place ? [{ letter: STOP_LETTERS[i] ?? '', place: s.place, i }] : [])))
const methods = computed<PaymentMethod[]>(() => quote.value?.payment_methods ?? ['cash', 'bank_transfer'])

function fieldError(f: FormField): string {
  const reason = booking.fieldErrors[f]
  return reason ? fieldErrorText(reason) : ''
}

const phone = computed({
  get: () => booking.recipientPhone,
  set: (v: string) => (booking.recipientPhone = formatKzPhoneInput(v)),
})

const cargoPlaceholder = computed(() =>
  booking.category === 'parcel' ? t('confirm.cargo_placeholder_parcel') : t('confirm.cargo_placeholder_other'),
)

// --- Map: the route being confirmed ---------------------------------------------

function framePoints(): LatLon[] {
  const pts: LatLon[] = placedStops.value.map((s) => s.place)
  const b = quote.value?.bbox
  if (b) pts.push({ lon: b[0], lat: b[1] }, { lon: b[2], lat: b[3] })
  return pts
}

const autoFit = useAutoFit(stage, framePoints)
watch(
  quote,
  (q) => {
    stage.stops.setStops(placedStops.value.map((s) => ({ lat: s.place.lat, lon: s.place.lon, letter: s.letter })))
    stage.route.setRoute(q?.geometry ?? null)
    autoFit.refit()
  },
  { immediate: true },
)

// --- Quote freshness -------------------------------------------------------------

let freshTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  if (!booking.complete) {
    void router.replace({ name: ROUTE.book })
    return
  }
  if (!booking.quoteCurrent) booking.schedulePreview(true)
  void session.ensureConfig()
  booking.ensureFreshQuote()
  freshTimer = setInterval(() => booking.ensureFreshQuote(), QUOTE_CHECK_MS)
})
onBeforeUnmount(() => clearInterval(freshTimer))

// --- Submit ----------------------------------------------------------------------

async function focusFirstError(): Promise<void> {
  if (snap.value !== 'full') snap.value = 'full'
  await nextTick()
  const el = contentEl.value?.querySelector<HTMLElement>('[aria-invalid="true"], [data-invalid="true"]')
  el?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  if (el && 'focus' in el) el.focus({ preventScroll: true })
}

async function choosePayment(): Promise<void> {
  const choice = await showActionSheet({
    title: t('confirm.payment'),
    message: t('confirm.payment_note'),
    actions: methods.value.map((m) => ({ id: m, label: t(`payment_method.${m}`) })),
  })
  if (choice && (methods.value as string[]).includes(choice)) booking.paymentMethod = choice as PaymentMethod
}

/** Switches taxi / parcel / other here; the route is re-quoted for the new service. */
async function chooseService(): Promise<void> {
  const choice = await showActionSheet({
    title: t('confirm.service'),
    actions: CATEGORIES.map((c) => ({ id: c, label: t(`category.${c}`) })),
  })
  if (!choice || choice === booking.category || !(CATEGORIES as readonly string[]).includes(choice)) return
  haptics.selection()
  booking.setCategory(choice as Category)
}

async function submit(): Promise<void> {
  const res = await booking.submit()
  switch (res.kind) {
    case 'created':
      haptics.notification('success')
      void orders.loadActive()
      await router.replace({ name: ROUTE.order, params: { id: res.order.id } })
      return
    case 'active_exists':
      toast.show(t('confirm.active_exists'))
      void orders.loadActive()
      await router.replace({ name: ROUTE.order, params: { id: res.orderId } })
      return
    case 'requoted':
      haptics.notification('warning')
      return
    case 'invalid':
      haptics.notification('error')
      await focusFirstError()
      return
    case 'route_invalid':
      toast.error(t('confirm.route_changed'))
      navigateBack(router)
      return
    case 'busy':
      return
    case 'failed':
      toast.error(errorText(res.error))
  }
}
</script>

<template>
  <JSheet v-model:snap="snap" :snap-points="['half', 'full']" :half-ratio="0.605" :aria-label="t('confirm.title')" @height="(h) => (stage.sheetHeight.value = h)">
    <template #header>
      <div class="confirm-head">
        <h1 class="confirm-head__title">{{ t('confirm.title') }}</h1>
      </div>
    </template>

    <div ref="contentEl" class="confirm">
      <p v-if="booking.reconfirm" class="confirm-notice" role="status">
        <Info :size="18" :stroke-width="2" aria-hidden="true" />
        <span>{{ t('confirm.requoted') }}</span>
      </p>

      <!-- Order facts (inset-grouped) -->
      <section class="confirm-facts">
        <button type="button" class="confirm-fact is-button" @click="chooseService">
          <span>{{ t('confirm.service') }}</span>
          <span class="confirm-fact__value">{{ t(`category.${booking.category}`) }}</span>
          <ChevronRight :size="18" :stroke-width="2" class="confirm-fact__chevron" aria-hidden="true" />
        </button>
        <div class="confirm-fact">
          <span>{{ t('booking.title') }}</span>
          <span v-if="quote" class="confirm-fact__value jt-tabular">
            {{ t('confirm.points', { n: placedStops.length }) }} · {{ formatDistance(quote.distance_m, lang) }}
          </span>
          <span v-else class="confirm-fact__value"><JSpinner /></span>
        </div>
        <div v-if="quote" class="confirm-fact">
          <span>{{ t('confirm.duration') }}</span>
          <span class="confirm-fact__value jt-tabular">~{{ formatDuration(quote.duration_s, lang) }}</span>
        </div>
        <button type="button" class="confirm-fact is-button" @click="choosePayment">
          <span>{{ t('confirm.payment') }}</span>
          <span class="confirm-fact__value">{{ t(`payment_method.${booking.paymentMethod}`) }}</span>
          <ChevronRight :size="18" :stroke-width="2" class="confirm-fact__chevron" aria-hidden="true" />
        </button>
      </section>

      <!-- Fare -->
      <section class="confirm-fare">
        <h2 class="confirm-fare__title">{{ t('confirm.fare') }}</h2>
        <FarePicker v-if="quote" v-model="booking.fare" :fare="quote.fare" :locale="lang" :error="fieldError('fare')" />
        <p v-if="quote" class="confirm-fare__range jt-tabular">
          {{ t('confirm.fare_range', { min: formatMoney(quote.fare.min, lang), max: formatMoney(quote.fare.max, lang), step: formatMoney(quote.fare.step, lang) }) }}
        </p>
      </section>

      <!-- Category-specific details -->
      <section class="confirm-fields">
        <template v-if="delivery">
          <JTextArea
            v-model="booking.cargoDescription"
            :label="t('confirm.cargo_description')"
            :placeholder="cargoPlaceholder"
            :max="TEXT_MAX"
            :rows="2"
            :error="fieldError('cargo_description')"
          />
          <JTextField
            v-if="booking.category === 'parcel'"
            v-model="phone"
            type="tel"
            inputmode="tel"
            autocomplete="off"
            placeholder="+7 7__ ___ __ __"
            :label="t('confirm.recipient_phone')"
            :hint="t('confirm.recipient_hint')"
            :error="fieldError('recipient_phone')"
          />
        </template>
        <JTextArea
          v-model="booking.comment"
          :placeholder="t('confirm.comment')"
          :max="TEXT_MAX"
          :rows="1"
          :error="fieldError('comment')"
        />
      </section>

      <section v-if="delivery" class="confirm-card">
        <h2 class="confirm-card__title">{{ t('confirm.prohibited_title') }}</h2>
        <ul v-if="prohibitedKeys.length > 0" class="confirm-prohibited">
          <li v-for="key in prohibitedKeys" :key="key">{{ t(key) }}</li>
        </ul>
        <button
          type="button"
          role="checkbox"
          class="confirm-ack"
          :aria-checked="booking.prohibitedItemsAck"
          :data-invalid="fieldError('prohibited_items_ack') ? 'true' : undefined"
          @click="booking.prohibitedItemsAck = !booking.prohibitedItemsAck"
        >
          <CircleCheck v-if="booking.prohibitedItemsAck" :size="24" :stroke-width="2" class="confirm-ack__icon is-on" aria-hidden="true" />
          <Circle v-else :size="24" :stroke-width="1.75" class="confirm-ack__icon" aria-hidden="true" />
          <span>{{ t('confirm.prohibited_ack') }}</span>
        </button>
        <p v-if="fieldError('prohibited_items_ack')" class="confirm-error" role="alert">{{ fieldError('prohibited_items_ack') }}</p>
      </section>

    </div>

    <template #footer>
      <div class="confirm-actions">
        <JButton block :loading="booking.submitting" :disabled="!quote || booking.previewStatus !== 'ready'" @click="submit">
          {{ t('confirm.submit') }}<template v-if="booking.fare !== null"> · {{ formatMoney(booking.fare, lang) }}</template>
        </JButton>
      </div>
    </template>
  </JSheet>
</template>

<style scoped>
.confirm-facts {
  display: flex;
  flex-direction: column;
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
  overflow: hidden;
}

.confirm-fact {
  position: relative;
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  min-height: 44px;
  padding: 0 var(--jt-space-4);
  font: var(--jt-font-body);
  text-align: left;
}

.confirm-fact + .confirm-fact::before {
  content: '';
  position: absolute;
  top: 0;
  left: var(--jt-space-4);
  right: 0;
  border-top: var(--jt-hairline) solid var(--jt-color-separator);
}

.confirm-fact > span:first-child {
  flex: 1;
}

.confirm-fact__value {
  color: var(--jt-color-label-secondary);
}

.confirm-fact__chevron {
  margin-right: -4px;
  color: var(--jt-color-label-tertiary);
}

.confirm-fact.is-button:active {
  background: var(--jt-color-fill-tertiary);
}

.confirm-fare {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  padding: var(--jt-space-3) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
  --jt-fare-step-bg: var(--jt-color-bg);
}

.confirm-fare :deep(.fare__step) {
  width: 46px;
  height: 46px;
}

.confirm-fare__title {
  font: var(--jt-font-headline);
}

.confirm-fare__range {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  text-align: center;
}

.confirm-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--jt-space-3);
  padding-bottom: var(--jt-space-3);
}

.confirm-head__title {
  font: var(--jt-font-title1);
}


.confirm {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-5);
  padding-bottom: var(--jt-space-4);
}

.confirm-notice {
  display: flex;
  align-items: flex-start;
  gap: var(--jt-space-2);
  padding: var(--jt-space-3);
  border-radius: 16px;
  background: var(--jt-color-blue-tint);
  color: var(--jt-color-blue-text);
  font: var(--jt-font-subheadline);
}

.confirm-card {
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
}

.confirm-card__title {
  margin-bottom: var(--jt-space-2);
  font: var(--jt-font-headline);
}

.confirm-card__meta {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  margin-top: var(--jt-space-3);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.confirm-stops {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

.confirm-stops__row {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
}

.confirm-stops__address {
  flex: 1;
  min-width: 0;
  font: var(--jt-font-callout);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.confirm-fields {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
}

.confirm-prohibited {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: var(--jt-space-3);
  padding-left: 18px;
  list-style: disc;
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.confirm-ack {
  display: flex;
  align-items: flex-start;
  gap: var(--jt-space-3);
  width: 100%;
  padding-top: var(--jt-space-3);
  border-top: var(--jt-hairline) solid var(--jt-color-separator);
  font: var(--jt-font-callout);
  text-align: left;
}

.confirm-ack__icon {
  flex: none;
  color: var(--jt-color-label-tertiary);
}

.confirm-ack__icon.is-on {
  color: var(--jt-color-label);
}

.confirm-ack[data-invalid='true'] .confirm-ack__icon {
  color: var(--jt-color-red);
}

.confirm-error {
  margin-top: var(--jt-space-2);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}

.confirm-actions {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-1);
}
</style>
