<!--
  Marketing reports for a period (local dates in the report time zone):
  daily coupons, invitations, discounts, bonuses and messages, the referral
  and coupon conversion, the CSV export (reports.export, aggregates only) and
  the promotions reconciliation.
-->
<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { localDate } from '../../api/common'
import { marketingApi, type MarketingReportDay } from '../../api/marketing'
import { AppButton, Card, DataTable, DateRange, ExportButton, FiltersBar, KeyValueList, StateView, type Column, type KeyValueItem } from '../../components'
import ColumnChart from '../../components/charts/ColumnChart.vue'
import LineChart from '../../components/charts/LineChart.vue'
import { useFormat } from '../../composables/useFormat'
import { useListQuery } from '../../composables/useListQuery'
import { useResource } from '../../composables/useResource'
import KpiTile from '../dashboard/KpiTile.vue'

const DEFAULT_DAYS = 30
const MAX_DAYS = 366

const { t, te } = useI18n()
const fmt = useFormat()
const q = useListQuery({ from: '', to: '' })
const f = q.filters

function daysAgo(n: number): string {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return localDate(d)
}

const range = computed(() => ({ from: f.from || daysAgo(DEFAULT_DAYS - 1), to: f.to || localDate() }))
const tooLong = computed(() => {
  const a = Date.parse(range.value.from)
  const b = Date.parse(range.value.to)
  return Number.isFinite(a) && Number.isFinite(b) && (b - a) / 86_400_000 + 1 > MAX_DAYS
})

const res = useResource((signal) => marketingApi.report(range.value, signal))
watch(
  () => q.key.value,
  () => {
    if (!tooLong.value) void res.reload()
  },
  { immediate: true },
)
const rec = useResource((signal) => marketingApi.reconciliation(signal))
void rec.reload()

const r = computed(() => res.data.value)
const days = computed<MarketingReportDay[]>(() => r.value?.days ?? [])
const labels = computed(() => days.value.map((d) => d.date))
const total = (key: keyof Omit<MarketingReportDay, 'date'>): number => days.value.reduce((s, d) => s + d[key], 0)

const tiles = computed(() => [
  { key: 'issued', label: t('marketing.r_coupons_issued'), value: fmt.count(total('coupons_issued')), detail: t('marketing.r_expired_n', { n: fmt.count(total('coupons_expired')) }) },
  { key: 'redeemed', label: t('marketing.r_coupons_redeemed'), value: fmt.count(total('coupons_redeemed')), detail: '' },
  { key: 'referrals', label: t('marketing.r_referrals'), value: fmt.count(total('referrals')), detail: t('marketing.r_rewards_n', { n: fmt.count(total('referral_rewards')) }) },
  { key: 'discounts', label: t('marketing.r_discounts'), value: fmt.money(total('discounts_settled')), detail: '' },
  { key: 'bonus', label: t('marketing.r_bonus'), value: fmt.money(total('bonus_granted')), detail: '' },
  { key: 'messages', label: t('marketing.r_messages_sent'), value: fmt.count(total('messages_sent')), detail: t('marketing.r_failed_n', { n: fmt.count(total('messages_failed')) }) },
])

const couponSeries = computed(() => [
  { key: 'issued', label: t('marketing.r_coupons_issued'), values: days.value.map((d) => d.coupons_issued), shade: 'light' as const },
  { key: 'redeemed', label: t('marketing.r_coupons_redeemed'), values: days.value.map((d) => d.coupons_redeemed), shade: 'strong' as const },
])
const referralSeries = computed(() => [
  { key: 'referrals', label: t('marketing.r_referrals'), values: days.value.map((d) => d.referrals), shade: 'light' as const },
  { key: 'rewards', label: t('marketing.r_referral_rewards'), values: days.value.map((d) => d.referral_rewards), shade: 'strong' as const },
])

function share(part: number, whole: number): string {
  return whole > 0 ? fmt.percentFromBps(Math.round((part / whole) * 10_000)) : '—'
}

const funnel = computed(() => {
  const x = r.value?.funnel
  if (!x) return { referral: [], coupons: [] }
  return {
    referral: [
      { key: 'attributed', label: t('marketing.funnel_attributed'), n: x.attributed, share: '' },
      { key: 'qualified', label: t('marketing.funnel_qualified'), n: x.qualified, share: share(x.qualified, x.attributed) },
      { key: 'rewarded', label: t('marketing.funnel_rewarded'), n: x.rewarded, share: share(x.rewarded, x.qualified) },
      { key: 'capped', label: t('marketing.funnel_capped'), n: x.capped, share: share(x.capped, x.qualified) },
    ],
    coupons: [
      { key: 'issued', label: t('marketing.r_coupons_issued'), n: x.coupons_issued, share: '' },
      { key: 'redeemed', label: t('marketing.r_coupons_redeemed'), n: x.coupons_redeemed, share: share(x.coupons_redeemed, x.coupons_issued) },
    ],
  }
})

function exportQuery(): Record<string, string | undefined> {
  return { from: range.value.from, to: range.value.to }
}

const recTotals = computed<KeyValueItem[]>(() => {
  const x = rec.data.value?.totals
  if (!x) return []
  return [
    { key: 'discounts', label: t('marketing.t_discounts_settled'), value: fmt.money(x.discounts_settled) },
    { key: 'credited', label: t('marketing.t_compensation_credited'), value: fmt.money(x.compensation_credited) },
    { key: 'reversed', label: t('marketing.t_compensation_reversed'), value: fmt.money(x.compensation_reversed) },
    { key: 'bonus', label: t('marketing.t_bonus_outstanding'), value: fmt.money(x.bonus_outstanding) },
    { key: 'bonus_reserved', label: t('marketing.t_bonus_reserved'), value: fmt.money(x.bonus_reserved) },
    { key: 'coupons', label: t('marketing.t_coupon_liability'), value: fmt.money(x.coupon_liability) },
    { key: 'reserved', label: t('marketing.t_promotions_reserved'), value: fmt.money(x.promotions_reserved) },
    { key: 'review', label: t('marketing.t_promotions_under_review'), value: fmt.count(x.promotions_under_review) },
    { key: 'events', label: t('marketing.t_events'), value: t('marketing.h_events_value', { pending: x.events_pending, failed: x.events_failed }) },
  ]
})

type Issue = { kind: string; ref: string; note?: string }
const issueColumns = computed<Column<Issue>[]>(() => [
  { key: 'kind', label: t('marketing.issue_kind'), format: (v) => (te(`marketing.issue_${String(v)}`, 'kk') ? t(`marketing.issue_${String(v)}`) : String(v)) },
  { key: 'ref', label: 'ID', mono: true },
  { key: 'note', label: t('common.note') },
])
</script>

<template>
  <section class="jt-page">
    <FiltersBar :show-search="false" @reset="q.reset">
      <DateRange v-model:from="f.from" v-model:to="f.to" />
      <span class="jt-muted range-note">{{ t('marketing.r_period_note', { from: fmt.date(range.from), to: fmt.date(range.to), tz: r?.timezone ?? '' }) }}</span>
      <template #actions>
        <ExportButton kind="marketing" :query="exportQuery" />
      </template>
    </FiltersBar>
    <p v-if="tooLong" class="warn" role="alert">{{ t('dashboard.range_too_long', { n: MAX_DAYS }) }}</p>

    <StateView v-if="!r" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <h2 class="jt-section-title">{{ t('marketing.r_totals') }}</h2>
      <div class="tiles">
        <KpiTile v-for="k in tiles" :key="k.key" :label="k.label" :value="k.value" :detail="k.detail" />
      </div>

      <div class="jt-grid-2">
        <Card :title="t('marketing.r_coupons_chart')">
          <ColumnChart :labels="labels" :series="couponSeries" :format="(n) => fmt.count(n)" :caption="t('marketing.r_coupons_chart')" />
        </Card>
        <Card :title="t('marketing.r_referrals_chart')">
          <ColumnChart :labels="labels" :series="referralSeries" :format="(n) => fmt.count(n)" :caption="t('marketing.r_referrals_chart')" />
        </Card>
        <Card :title="t('marketing.r_discounts_chart')">
          <LineChart :labels="labels" :values="days.map((d) => d.discounts_settled)" :label="t('marketing.r_discounts')" :format="(n) => fmt.money(n)" :caption="t('marketing.r_discounts_chart')" />
        </Card>
        <Card :title="t('marketing.r_bonus_chart')">
          <LineChart :labels="labels" :values="days.map((d) => d.bonus_granted)" :label="t('marketing.r_bonus')" :format="(n) => fmt.money(n)" :caption="t('marketing.r_bonus_chart')" />
        </Card>
        <Card :title="t('marketing.r_messages_chart')" :subtitle="t('marketing.sent_means')">
          <LineChart :labels="labels" :values="days.map((d) => d.messages_sent)" :label="t('marketing.r_messages_sent')" :format="(n) => fmt.count(n)" :caption="t('marketing.r_messages_chart')" />
        </Card>
        <Card :title="t('marketing.funnel_title')">
          <div class="funnel">
            <ol class="funnel__steps">
              <li v-for="s in funnel.referral" :key="s.key" class="funnel__step">
                <span>{{ s.label }}</span>
                <span class="jt-num"><strong>{{ fmt.count(s.n) }}</strong><span v-if="s.share" class="jt-muted"> · {{ s.share }}</span></span>
              </li>
            </ol>
            <ol class="funnel__steps">
              <li v-for="s in funnel.coupons" :key="s.key" class="funnel__step">
                <span>{{ s.label }}</span>
                <span class="jt-num"><strong>{{ fmt.count(s.n) }}</strong><span v-if="s.share" class="jt-muted"> · {{ s.share }}</span></span>
              </li>
            </ol>
          </div>
        </Card>
      </div>
    </template>

    <Card :title="t('marketing.reconciliation_title')" :subtitle="t('marketing.reconciliation_hint')" :flush="Boolean(rec.data.value && !rec.data.value.ok)">
      <template #actions>
        <AppButton size="sm" :loading="rec.loading.value" @click="rec.reload()">{{ t('finance.check_again') }}</AppButton>
      </template>
      <StateView v-if="rec.error.value" :error="rec.error.value" compact @retry="rec.reload()" />
      <template v-else-if="rec.data.value">
        <p v-if="rec.data.value.ok" class="ok">
          <span class="ok__dot" aria-hidden="true" />
          {{ t('marketing.reconciliation_ok', { time: fmt.dateTime(rec.data.value.checked_at) }) }}
        </p>
        <template v-else>
          <p class="bad" role="alert">{{ t('marketing.reconciliation_bad', { n: rec.data.value.issues.length, time: fmt.dateTime(rec.data.value.checked_at) }) }}</p>
          <DataTable :columns="issueColumns" :rows="rec.data.value.issues" :row-key="(i) => `${i.kind}:${i.ref}`" />
        </template>
        <div class="totals" :class="{ 'totals--flush': !rec.data.value.ok }">
          <KeyValueList :items="recTotals" />
        </div>
      </template>
    </Card>
  </section>
</template>

<style scoped>
.range-note {
  font: var(--jt-font-small);
}

.warn {
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}

.tiles {
  display: grid;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  gap: var(--jt-space-3);
}

.tiles :deep(.kpi__value) {
  font: var(--jt-font-title);
}

.funnel {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
}

.funnel__steps {
  display: flex;
  flex-direction: column;
}

.funnel__step {
  display: flex;
  justify-content: space-between;
  gap: var(--jt-space-3);
  padding: 8px 0;
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.funnel__step:last-child {
  border-bottom: 0;
}

.ok {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: var(--jt-space-3);
}

.ok__dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: var(--jt-color-green);
}

.bad {
  margin: 0 var(--jt-space-5) var(--jt-space-3);
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}

.totals--flush {
  padding: var(--jt-space-3) var(--jt-space-5) var(--jt-space-4);
}
</style>
