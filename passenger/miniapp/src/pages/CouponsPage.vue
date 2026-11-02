<!--
  «Менің купоным» (§23.4): the first-ride offer while it can still be earned
  (with the service the first order must be: a first delivery earns no taxi
  coupon), the passenger's coupons with their state and terms, and the
  informational promotions. An active coupon counts down to expires_at on the
  server's clock (offset from GET /promotions), ticking in step with that
  instant, so it reads expired exactly when the server stops taking it; one
  the server refused for an order this session says it cannot be used now.
  Opened from the profile or the bot's «Менің купоным» button (start
  parameter coupons).
-->
<script setup lang="ts">
import { Gift, TicketPercent } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import type { CouponStatus, PromoCoupon } from '../api/types'
import ScrollPage from '../components/ScrollPage.vue'
import { JBadge, JButton, JEmptyState, JListGroup, JListRow, JSkeleton, type Tone } from '../components/ui'
import { countdown, couponStatus, isCouponActive, type Countdown } from '../domain/promotions'
import { useErrorText } from '../i18n/core'
import { ROUTE } from '../router/names'
import { pickLocalized } from '../services/config'
import { usePromotionsStore } from '../stores/promotions'
import { useSessionStore } from '../stores/session'
import { formatDateTime, formatDuration, formatMoney } from '../utils/format'
import { CATEGORIES, type Category } from '../utils/status'

const STATUS_TONE: Record<CouponStatus, Tone> = {
  issued: 'dark',
  reserved: 'neutral',
  redeemed: 'success',
  expired: 'neutral',
  voided: 'neutral',
}

const { t } = useI18n()
const errorText = useErrorText()
const router = useRouter()
const promotions = usePromotionsStore()
const session = useSessionStore()

const lang = computed(() => session.language)
const data = computed(() => promotions.data)
const coupons = computed(() => data.value?.coupons ?? [])
const offer = computed(() => data.value?.offers.find((o) => o.kind === 'first_ride_coupon') ?? null)
const banners = computed(() => data.value?.banners ?? [])
/** Coupons are switched off for now: an active one waits. */
const paused = computed(() => data.value?.redemptions_enabled === false)

// --- Countdown ------------------------------------------------------------------

/** The server's clock as the countdowns show it. */
const now = ref(promotions.serverNow())
let timer: ReturnType<typeof setTimeout> | undefined

/**
 * Advances the clock when a countdown's seconds change: in step with each
 * active coupon's expires_at, so the last tick lands on that instant.
 */
function tick(): void {
  clearTimeout(timer)
  now.value = promotions.serverNow()
  const waits = coupons.value
    .filter((c) => isCouponActive(c, now.value))
    .map((c) => (Date.parse(c.expires_at) - now.value) % 1000 || 1000)
  if (waits.length > 0) timer = setTimeout(tick, Math.min(...waits))
}

watch(() => [promotions.data, promotions.offsetMs] as const, tick, { immediate: true })
onBeforeUnmount(() => clearTimeout(timer))

const timers = computed(() => new Map<string, Countdown>(coupons.value.map((c) => [c.id, countdown(c.expires_at, now.value)])))

function status(c: PromoCoupon): CouponStatus {
  return couponStatus(c, now.value)
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

function timerParts(c: PromoCoupon): Array<{ key: string; value: string; label: string }> {
  const left = timers.value.get(c.id)
  if (!left) return []
  return [
    { key: 'd', value: pad(left.days), label: t('coupons.days') },
    { key: 'h', value: pad(left.hours), label: t('coupons.hours') },
    { key: 'm', value: pad(left.minutes), label: t('coupons.minutes') },
    { key: 's', value: pad(left.seconds), label: t('coupons.seconds') },
  ]
}

function timerLabel(c: PromoCoupon): string {
  const left = timers.value.get(c.id)
  return left ? `${t('coupons.expires_in')}: ${t('coupons.countdown', { d: left.days, h: left.hours, m: left.minutes, s: left.seconds })}` : ''
}

// --- Terms ----------------------------------------------------------------------

function taxiOnly(categories: readonly Category[]): boolean {
  return categories.length === 1 && categories[0] === 'taxi'
}

function categoryList(categories: readonly Category[]): string {
  return categories.map((cat) => t(`category.${cat}`)).join(', ')
}

/** "Taxi orders only", or the services a coupon of several categories applies to. */
function categoriesRule(c: PromoCoupon): string {
  return taxiOnly(c.categories) ? t('coupons.rule_taxi') : t('coupons.rule_categories', { list: categoryList(c.categories) })
}

/** "After your first taxi ride you get a 400 ₸ coupon", or the first ride of any service. */
const offerText = computed(() => {
  const o = offer.value
  if (!o) return ''
  const amount = formatMoney(o.amount, lang.value)
  return taxiOnly(o.categories) ? t('coupons.offer_text_taxi', { amount }) : t('coupons.offer_text', { amount })
})

/**
 * Which first order earns the coupon, when not every service does: the
 * server issues it only for a first completed order of the offer's services
 * (§23.4), so a passenger who sends a parcel first never gets it.
 */
const offerCondition = computed(() => {
  const cats = offer.value?.categories ?? []
  if (cats.length === 0 || CATEGORIES.every((c) => cats.includes(c))) return ''
  return taxiOnly(cats) ? t('coupons.offer_other_first') : t('coupons.offer_categories', { list: categoryList(cats) })
})

function openOrder(id: string): void {
  void router.push({ name: ROUTE.order, params: { id } })
}

onMounted(() => void promotions.refresh())
</script>

<template>
  <ScrollPage :title="t('coupons.title')">
    <div v-if="!data && promotions.loading" class="promo-loading">
      <JSkeleton height="180px" radius="22px" />
    </div>

    <JEmptyState v-else-if="promotions.unavailable" class="promo-empty" :title="t('promo.unavailable_title')" :description="t('promo.unavailable_text')" />

    <JEmptyState v-else-if="!data" class="promo-empty" :title="t('app.load_failed_title')" :description="errorText(promotions.error)">
      <template #action>
        <JButton block @click="promotions.refresh()">{{ t('common.retry') }}</JButton>
      </template>
    </JEmptyState>

    <template v-else>
      <!-- The first-ride coupon still to be earned -->
      <section v-if="offer" class="promo-card offer">
        <span class="offer__icon" aria-hidden="true"><Gift :size="24" :stroke-width="1.75" /></span>
        <span class="offer__text">
          <span class="offer__title">{{ pickLocalized(offer.title, lang) || t('coupons.offer_title') }}</span>
          <span>{{ offerText }}</span>
          <span v-if="offerCondition" class="offer__meta">{{ offerCondition }}</span>
          <span v-if="offer.validity_hours" class="offer__meta">{{ t('coupons.offer_validity', { duration: formatDuration(offer.validity_hours * 3600, lang) }) }}</span>
        </span>
      </section>

      <section
        v-for="c in coupons"
        :key="c.id"
        class="promo-card coupon"
        :class="{ 'is-past': status(c) !== 'issued' && status(c) !== 'reserved' }"
      >
        <div class="coupon__head">
          <span class="coupon__amount jt-tabular">{{ formatMoney(c.amount, lang) }}</span>
          <JBadge class="coupon__status" :tone="STATUS_TONE[status(c)]" :label="t(`coupons.status.${status(c)}`)" />
        </div>
        <p v-if="c.title" class="coupon__title">{{ pickLocalized(c.title, lang) }}</p>

        <template v-if="status(c) === 'issued'">
          <p class="coupon__caption">{{ t('coupons.expires_in') }}</p>
          <div class="coupon__timer" role="timer" :aria-label="timerLabel(c)">
            <span v-for="part in timerParts(c)" :key="part.key" class="coupon__unit" aria-hidden="true">
              <span class="coupon__digits jt-tabular">{{ part.value }}</span>
              <span class="coupon__unit-label">{{ part.label }}</span>
            </span>
          </div>
          <p class="coupon__meta jt-tabular">{{ t('coupons.valid_until', { date: formatDateTime(c.expires_at, lang) }) }}</p>
          <p v-if="paused" class="coupon__meta" role="status">{{ t('coupons.paused') }}</p>
          <p v-else-if="promotions.couponRefused(c.id)" class="coupon__meta" role="status">{{ t('coupons.refused') }}</p>
          <ul class="coupon__rules" :aria-label="t('coupons.rules')">
            <li>{{ t('coupons.rule_auto') }}</li>
            <li v-if="c.categories.length > 0">{{ categoriesRule(c) }}</li>
            <li v-if="c.min_order_fare > 0" class="jt-tabular">{{ t('coupons.rule_min_fare', { min: formatMoney(c.min_order_fare, lang) }) }}</li>
            <li>{{ t('coupons.rule_one_ride') }}</li>
            <li>{{ t('coupons.rule_personal') }}</li>
            <li>{{ t('coupons.rule_no_cash') }}</li>
          </ul>
        </template>

        <template v-else-if="status(c) === 'reserved'">
          <p class="coupon__meta">{{ t('coupons.reserved_text') }}</p>
          <JButton v-if="c.reserved_order_id" variant="secondary" size="medium" @click="openOrder(c.reserved_order_id)">{{ t('coupons.open_order') }}</JButton>
        </template>

        <p v-else-if="status(c) === 'redeemed' && c.redeemed_at" class="coupon__meta jt-tabular">
          {{ t('coupons.redeemed_on', { date: formatDateTime(c.redeemed_at, lang) }) }}
        </p>
        <p v-else-if="status(c) === 'expired'" class="coupon__meta jt-tabular">
          {{ t('coupons.expired_on', { date: formatDateTime(c.expires_at, lang) }) }}
        </p>
      </section>

      <JEmptyState v-if="coupons.length === 0 && !offer" class="promo-empty" :title="t('coupons.empty_title')" :description="t('coupons.empty_text')">
        <template #icon><TicketPercent :size="40" :stroke-width="1.5" /></template>
      </JEmptyState>

      <JListGroup v-if="banners.length > 0" :header="t('coupons.banners')">
        <JListRow v-for="b in banners" :key="b.id" :title="pickLocalized(b.title, lang)" :subtitle="pickLocalized(b.description, lang)" />
      </JListGroup>
    </template>
  </ScrollPage>
</template>

<style scoped>
.promo-loading {
  margin: 0 var(--jt-side-margin);
}

.promo-empty {
  margin-top: var(--jt-space-8);
  padding: 0 var(--jt-side-margin);
}

.promo-card {
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.offer {
  display: flex;
  align-items: flex-start;
  gap: var(--jt-space-3);
}

.offer__icon {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  border-radius: 50%;
  background: var(--jt-color-fill-secondary);
}

.offer__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  font: var(--jt-font-callout);
}

.offer__title {
  font: var(--jt-font-headline);
}

.offer__meta {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.coupon {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--jt-space-2);
}

/* Used, expired or void: kept for the record, without competing with an active one. */
.coupon.is-past .coupon__amount,
.coupon.is-past .coupon__title {
  color: var(--jt-color-label-secondary);
}

.coupon__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
  width: 100%;
}

.coupon__amount {
  font: var(--jt-font-large-title);
  letter-spacing: var(--jt-tracking-large-title);
}

.coupon__title {
  margin-top: -4px;
  font: var(--jt-font-headline);
}

.coupon__caption {
  margin-top: var(--jt-space-2);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  text-transform: uppercase;
}

.coupon__timer {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--jt-space-2);
  width: 100%;
}

.coupon__unit {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  padding: var(--jt-space-2) 0;
  border-radius: 14px;
  background: var(--jt-color-fill-secondary);
}

.coupon__digits {
  font: var(--jt-font-title2);
}

.coupon__unit-label {
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
}

.coupon__meta {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.coupon__rules {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
  margin-top: var(--jt-space-1);
  padding: var(--jt-space-3) 0 0 18px;
  border-top: var(--jt-hairline) solid var(--jt-color-separator);
  list-style: disc;
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}
</style>
