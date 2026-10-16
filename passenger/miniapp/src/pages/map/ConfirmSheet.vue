<!--
  Details and confirmation: category-specific fields, the fare picker, the
  payment method and a summary of the whole order. "Back to route" keeps
  everything (the booking store holds the draft). Confirm sends POST /orders
  with an idempotency key that stays the same on retries.
-->
<script setup lang="ts">
import { Check, Circle, CircleCheck, Info } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import FarePicker from '../../components/booking/FarePicker.vue'
import StopBadge from '../../components/StopBadge.vue'
import { JButton, JSheet, JSpinner, JTextArea, JTextField, toast, type SheetSnap } from '../../components/ui'
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
import type { PaymentMethod } from '../../utils/status'
import { TEXT_MAX } from '../../utils/text'

const QUOTE_CHECK_MS = 10_000

const { t } = useI18n()
const errorText = useErrorText()
const fieldErrorText = useFieldErrorText()
const router = useRouter()
const stage = useStageScreen()
const booking = useBookingStore()
const orders = useOrdersStore()
const session = useSessionStore()

const snap = ref<SheetSnap>('full')
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
  <JSheet v-model:snap="snap" :snap-points="['half', 'full']" :aria-label="t('confirm.title')" @height="(h) => (stage.sheetHeight.value = h)">
    <template #header>
      <div class="confirm-head">
        <h1 class="confirm-head__title">{{ t('confirm.title') }}</h1>
        <span class="confirm-head__category">{{ t(`category.${booking.category}`) }}</span>
      </div>
    </template>

    <div ref="contentEl" class="confirm">
      <p v-if="booking.reconfirm" class="confirm-notice" role="status">
        <Info :size="18" :stroke-width="2" aria-hidden="true" />
        <span>{{ t('confirm.requoted') }}</span>
      </p>

      <!-- Route -->
      <section class="confirm-card">
        <ol class="confirm-stops">
          <li v-for="s in placedStops" :key="s.i" class="confirm-stops__row">
            <StopBadge :letter="s.letter" size="small" />
            <span class="confirm-stops__address">{{ s.place.address }}</span>
          </li>
        </ol>
        <p v-if="quote" class="confirm-card__meta jt-tabular">
          {{ t('booking.summary', { distance: formatDistance(quote.distance_m, lang), duration: formatDuration(quote.duration_s, lang) }) }}
        </p>
        <p v-else class="confirm-card__meta is-loading"><JSpinner /> {{ t('booking.calculating') }}</p>
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
          :label="t('confirm.comment')"
          :placeholder="t('confirm.comment_placeholder')"
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

      <!-- Fare -->
      <section class="confirm-block">
        <h2 class="confirm-block__title">{{ t('confirm.fare') }}</h2>
        <FarePicker v-if="quote" v-model="booking.fare" :fare="quote.fare" :locale="lang" :error="fieldError('fare')" />
        <p class="confirm-block__hint">{{ t('confirm.fare_hint') }}</p>
      </section>

      <!-- Payment -->
      <section class="confirm-block">
        <h2 class="confirm-block__title">{{ t('confirm.payment') }}</h2>
        <div class="confirm-options" role="radiogroup" :aria-label="t('confirm.payment')">
          <button
            v-for="m in methods"
            :key="m"
            type="button"
            role="radio"
            class="confirm-option"
            :aria-checked="booking.paymentMethod === m"
            @click="booking.paymentMethod = m"
          >
            <span class="confirm-option__text">
              <span class="confirm-option__title">{{ t(`payment_method.${m}`) }}</span>
              <span class="confirm-option__subtitle">{{ t(`payment_hint.${m}`) }}</span>
            </span>
            <Check v-if="booking.paymentMethod === m" :size="20" :stroke-width="2.5" aria-hidden="true" />
          </button>
        </div>
        <p class="confirm-block__hint">{{ t('confirm.payment_note') }}</p>
      </section>

      <!-- Summary -->
      <section class="confirm-block">
        <h2 class="confirm-block__title">{{ t('confirm.summary') }}</h2>
        <dl class="confirm-summary">
          <div><dt>{{ t('confirm.service') }}</dt><dd>{{ t(`category.${booking.category}`) }}</dd></div>
          <div v-for="s in placedStops" :key="`sum-${s.i}`">
            <dt><StopBadge :letter="s.letter" size="small" /></dt>
            <dd>{{ s.place.address }}</dd>
          </div>
          <div v-if="quote"><dt>{{ t('confirm.distance') }}</dt><dd class="jt-tabular">{{ formatDistance(quote.distance_m, lang) }}</dd></div>
          <div v-if="quote"><dt>{{ t('confirm.duration') }}</dt><dd class="jt-tabular">{{ formatDuration(quote.duration_s, lang) }}</dd></div>
          <div v-if="booking.fare !== null"><dt>{{ t('confirm.fare') }}</dt><dd class="jt-tabular">{{ formatMoney(booking.fare, lang) }}</dd></div>
          <div><dt>{{ t('confirm.payment') }}</dt><dd>{{ t(`payment_method.${booking.paymentMethod}`) }}</dd></div>
          <div v-if="delivery && booking.cargoDescription.trim()"><dt>{{ t('confirm.description') }}</dt><dd>{{ booking.cargoDescription.trim() }}</dd></div>
          <div v-if="booking.category === 'parcel' && booking.recipientPhone"><dt>{{ t('confirm.recipient') }}</dt><dd class="jt-tabular">{{ booking.recipientPhone }}</dd></div>
          <div v-if="booking.comment.trim()"><dt>{{ t('detail.comment') }}</dt><dd>{{ booking.comment.trim() }}</dd></div>
        </dl>
      </section>
    </div>

    <template #footer>
      <div class="confirm-actions">
        <JButton block :loading="booking.submitting" :disabled="!quote || booking.previewStatus !== 'ready'" @click="submit">
          {{ t('confirm.submit') }}<template v-if="booking.fare !== null"> · {{ formatMoney(booking.fare, lang) }}</template>
        </JButton>
        <JButton block variant="plain" size="medium" :disabled="booking.submitting" @click="navigateBack(router)">
          {{ t('confirm.back_to_route') }}
        </JButton>
      </div>
    </template>
  </JSheet>
</template>

<style scoped>
.confirm-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--jt-space-3);
  padding-bottom: var(--jt-space-3);
}

.confirm-head__title {
  font: var(--jt-font-title2);
}

.confirm-head__category {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
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

.confirm-block {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.confirm-block__title {
  font: var(--jt-font-headline);
}

.confirm-block__hint {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.confirm-options {
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
  overflow: hidden;
}

.confirm-option {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  width: 100%;
  min-height: 60px;
  padding: 10px var(--jt-space-4);
  text-align: left;
}

.confirm-option + .confirm-option {
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.confirm-option:active {
  opacity: var(--jt-opacity-pressed);
}

.confirm-option__text {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.confirm-option__title {
  font: var(--jt-font-body);
}

.confirm-option__subtitle {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.confirm-summary {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: var(--jt-space-1) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
}

.confirm-summary > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-4);
  min-height: 44px;
  padding: 10px 0;
}

.confirm-summary > div + div {
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.confirm-summary dt {
  flex: none;
  color: var(--jt-color-label-secondary);
  font: var(--jt-font-subheadline);
}

.confirm-summary dd {
  margin: 0;
  min-width: 0;
  font: var(--jt-font-subheadline);
  text-align: right;
  overflow-wrap: anywhere;
}

.confirm-actions {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-1);
}
</style>
