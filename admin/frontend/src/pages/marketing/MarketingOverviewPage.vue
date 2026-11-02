<!--
  Marketing overview: the three emergency switches, the key figures (live
  campaigns, coupons, referral rewards, bonus balances, drivers'
  compensation), the health of the promotion machinery and every campaign
  with its budget usage. Aggregates only, no personal data.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { marketingApi, type Campaign } from '../../api/marketing'
import { Card, DataTable, StateView, StatusBadge, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useResync } from '../../composables/useRealtime'
import { useResource } from '../../composables/useResource'
import KpiTile from '../dashboard/KpiTile.vue'
import UsageBar from './UsageBar.vue'

const { t } = useI18n()
const fmt = useFormat()
const router = useRouter()
const res = useResource((signal) => marketingApi.overview(signal))
void res.reload()

const o = computed(() => res.data.value)

const switches = computed(() => {
  const s = o.value?.switches
  if (!s) return []
  return [
    { key: 'awards', label: t('marketing.s_awards_enabled'), on: s.awards_enabled },
    { key: 'redemptions', label: t('marketing.s_redemptions_enabled'), on: s.redemptions_enabled },
    { key: 'broadcasts', label: t('marketing.s_broadcasts_enabled'), on: s.broadcasts_enabled },
  ]
})

function sum(m: Partial<Record<string, number>> | undefined): number {
  return Object.values(m ?? {}).reduce<number>((a, n) => a + (n ?? 0), 0)
}

const tiles = computed(() => {
  const m = o.value
  if (!m) return []
  const c = m.coupons
  const r = m.referrals
  return [
    {
      key: 'active',
      label: t('marketing.o_active_campaigns'),
      value: fmt.count(m.campaigns.filter((x) => x.status === 'active').length),
      detail: t('marketing.o_campaigns_total', { n: m.campaigns.filter((x) => x.status !== 'archived').length }),
      to: { name: 'campaigns', query: { status: 'active' } },
    },
    { key: 'issued', label: t('marketing.o_coupons_issued'), value: fmt.count(sum(c)), detail: t('marketing.o_coupons_valid', { n: c.issued ?? 0 }), to: { name: 'coupons' } },
    { key: 'redeemed', label: t('marketing.o_coupons_redeemed'), value: fmt.count(c.redeemed ?? 0), detail: t('marketing.o_coupons_reserved', { n: c.reserved ?? 0 }), to: { name: 'coupons', query: { tab: 'coupons', status: 'redeemed' } } },
    { key: 'expired', label: t('marketing.o_coupons_expired'), value: fmt.count(c.expired ?? 0), detail: t('marketing.o_coupons_voided', { n: c.voided ?? 0 }), to: { name: 'coupons', query: { tab: 'coupons', status: 'expired' } } },
    {
      key: 'rewards',
      label: t('marketing.o_referral_rewards'),
      value: fmt.count(r.rewarded ?? 0),
      detail: t('marketing.o_referrals_detail', { n: sum(r), review: r.review ?? 0 }),
      to: { name: 'referrals', query: { status: 'rewarded' } },
    },
    {
      key: 'bonus',
      label: t('marketing.o_bonus_outstanding'),
      value: fmt.money(m.bonus.outstanding),
      detail: t('marketing.o_bonus_detail', { reserved: fmt.money(m.bonus.reserved), n: m.bonus.accounts }),
      to: { name: 'coupons', query: { tab: 'ledger' } },
    },
    {
      key: 'compensation',
      label: t('marketing.o_compensation'),
      value: fmt.money(m.compensation.credited),
      detail: t('marketing.o_compensation_detail', { amount: fmt.money(m.compensation.pending) }),
      to: { name: 'coupons', query: { tab: 'promotions' } },
    },
  ]
})

const health = computed(() => {
  const h = o.value?.health
  if (!h) return []
  return [
    {
      key: 'events',
      label: t('marketing.h_events'),
      value: t('marketing.h_events_value', { pending: h.events_pending, failed: h.events_failed }),
      detail: h.oldest_pending_s !== null && h.events_pending > 0 ? t('marketing.h_oldest', { age: fmt.duration(h.oldest_pending_s) }) : '',
      bad: h.events_failed > 0 || (h.oldest_pending_s ?? 0) > 300,
      to: null,
    },
    { key: 'stuck', label: t('marketing.h_stuck'), value: fmt.count(h.stuck_reservations), detail: '', bad: h.stuck_reservations > 0, to: null },
    { key: 'promotions', label: t('marketing.h_review_promotions'), value: fmt.count(h.review_promotions), detail: '', bad: h.review_promotions > 0, to: { name: 'coupons', query: { tab: 'promotions', review: 'true' } } },
    { key: 'referrals', label: t('marketing.h_review_referrals'), value: fmt.count(h.review_referrals), detail: '', bad: h.review_referrals > 0, to: { name: 'referrals', query: { status: 'review' } } },
    { key: 'queue', label: t('marketing.h_broadcast_queue'), value: fmt.count(h.broadcast_queue), detail: '', bad: false, to: { name: 'broadcasts', query: { status: 'sending' } } },
    { key: 'overdue', label: t('marketing.h_overdue'), value: fmt.count(h.overdue_scheduled), detail: '', bad: h.overdue_scheduled > 0, to: null },
  ]
})
const healthy = computed(() => health.value.every((h) => !h.bad))

const columns = computed<Column<Campaign>[]>(() => [
  { key: 'title', label: t('marketing.campaign'), value: (r) => fmt.name(r.title) },
  { key: 'template', label: t('marketing.template'), format: (v) => t(`marketing.template_${String(v)}`) },
  { key: 'status', label: t('common.status') },
  { key: 'usage', label: t('marketing.col_usage') },
])

const campaigns = computed(() => (o.value?.campaigns ?? []).filter((c) => c.status !== 'archived'))

useResync(() => void res.reload({ silent: true }))
</script>

<template>
  <section class="jt-page">
    <StateView v-if="!o" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <Card :title="t('marketing.s_title')" :subtitle="t('marketing.s_hint')">
        <template #actions>
          <RouterLink :to="{ name: 'marketing-settings' }" class="jt-link-button">{{ t('marketing.o_open_settings') }}</RouterLink>
        </template>
        <ul class="switches">
          <li v-for="s in switches" :key="s.key" class="switches__item">
            <span>{{ s.label }}</span>
            <span class="pill" :class="s.on ? 'pill--on' : 'pill--off'">{{ s.on ? t('marketing.s_on') : t('marketing.s_off') }}</span>
          </li>
        </ul>
      </Card>

      <h2 class="jt-section-title">{{ t('marketing.o_kpis') }}</h2>
      <div class="tiles">
        <KpiTile v-for="k in tiles" :key="k.key" :label="k.label" :value="k.value" :detail="k.detail" :to="k.to ?? null" />
      </div>

      <Card :title="t('marketing.o_health')" :subtitle="healthy ? t('marketing.o_health_ok') : ''">
        <ul class="health">
          <li v-for="h in health" :key="h.key" class="health__item" :class="{ 'is-bad': h.bad }">
            <span class="health__label">
              <RouterLink v-if="h.to" :to="h.to">{{ h.label }}</RouterLink>
              <template v-else>{{ h.label }}</template>
            </span>
            <span class="health__value jt-num">{{ h.value }}</span>
            <span v-if="h.detail" class="jt-muted health__detail">{{ h.detail }}</span>
          </li>
        </ul>
      </Card>

      <Card :title="t('nav.campaigns')" flush>
        <DataTable
          :columns="columns"
          :rows="campaigns"
          row-key="id"
          :empty-text="t('marketing.no_campaigns')"
          clickable
          @row-click="(r) => router.push({ name: 'campaign-detail', params: { id: r.id } })"
        >
          <template #cell-status="{ row }">
            <StatusBadge kind="campaign" :status="row.status" />
          </template>
          <template #cell-usage="{ row }">
            <UsageBar :campaign="row" />
          </template>
        </DataTable>
      </Card>
    </template>
  </section>
</template>

<style scoped>
.switches {
  display: flex;
  flex-direction: column;
}

.switches__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
  padding: 8px 0;
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.switches__item:last-child {
  border-bottom: 0;
}

.pill {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 10px;
  border-radius: var(--jt-radius-round);
  font: 600 12px/16px var(--jt-font-family);
}

.pill--on {
  background: var(--jt-color-green-tint);
  color: var(--jt-color-green-text);
}

.pill--off {
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}

.tiles {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--jt-space-3);
}

.health {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--jt-space-3);
}

.health__item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 12px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-fill-secondary);
}

.health__item.is-bad {
  background: var(--jt-color-red-tint);
}

.health__label {
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.health__value {
  font: var(--jt-font-title3);
}

.health__item.is-bad .health__value {
  color: var(--jt-color-red-text);
}

.health__detail {
  font: var(--jt-font-caption);
}
</style>
