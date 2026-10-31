<!--
  Trip history (GET /driver/orders/history): completed and cancelled trips
  with their fare; cursor pagination. Drivers see fares only: commissions
  live in the wallet history, not on order screens.
-->
<script setup lang="ts">
import { computed, onMounted, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { listHistory } from '../api/orders'
import type { DriverOrderSummary } from '../api/types'
import CategoryIcon from '../components/driver/CategoryIcon.vue'
import DriverPage from '../components/driver/DriverPage.vue'
import { JBadge, JButton, JEmptyState, JSkeleton } from '../components/ui'
import { useFormat } from '../composables/useFormat'
import { useAddressText, useErrorText } from '../i18n/core'
import { ROUTE } from '../router/names'
import { orderStatusTone } from '../utils/status'

const { t } = useI18n()
const addr = useAddressText()
const fmt = useFormat()
const errorText = useErrorText()
const router = useRouter()

const items = ref<DriverOrderSummary[]>([])
const next = ref<string | null>(null)
const loading = ref(false)
const loaded = ref(false)
const error = shallowRef<unknown>(null)

const totals = computed(() => {
  const done = items.value.filter((o) => o.status === 'completed')
  return { count: done.length, fare: done.reduce((s, o) => s + o.fare, 0) }
})

async function load(reset: boolean): Promise<void> {
  if (loading.value || (!reset && loaded.value && !next.value)) return
  loading.value = true
  try {
    const res = await listHistory(reset ? null : next.value)
    items.value = reset ? res.items : [...items.value, ...res.items]
    next.value = res.next_cursor
    loaded.value = true
    error.value = null
  } catch (err) {
    error.value = err
  } finally {
    loading.value = false
  }
}

function open(o: DriverOrderSummary): void {
  void router.push({ name: ROUTE.order, params: { id: o.id } })
}

onMounted(() => void load(true))
</script>

<template>
  <DriverPage :title="t('history.title')">
    <div v-if="!loaded && loading" class="hist__skeleton" aria-busy="true">
      <JSkeleton v-for="i in 4" :key="i" height="112px" radius="22px" />
    </div>

    <div v-else-if="!loaded && error" class="hist__state">
      <JEmptyState :title="t('app.load_failed_title')" :description="errorText(error)">
        <template #action><JButton block @click="load(true)">{{ t('common.retry') }}</JButton></template>
      </JEmptyState>
    </div>

    <div v-else-if="loaded && items.length === 0" class="hist__state">
      <JEmptyState :title="t('history.empty_title')" :description="t('history.empty_text')" />
    </div>

    <template v-else>
      <section v-if="totals.count > 0" class="hist__totals">
        <p class="hist__totals-label">{{ t('history.loaded_totals', { n: totals.count }) }}</p>
        <p class="hist__totals-value jt-tabular">{{ fmt.money(totals.fare) }}</p>
      </section>

      <button v-for="o in items" :key="o.id" type="button" class="hist__item" @click="open(o)">
        <div class="hist__top">
          <span class="hist__cat">
            <CategoryIcon :category="o.category" :size="16" /> №{{ o.number }}<span v-if="o.locality" class="hist__locality"> · {{ o.locality }}</span>
          </span>
          <span class="hist__date">{{ fmt.dateTime(o.completed_at ?? o.cancelled_at ?? o.created_at) }}</span>
        </div>
        <p class="hist__route">{{ addr(o.pickup.address) }} → {{ addr(o.destination.address) }}</p>
        <div class="hist__bottom">
          <JBadge :tone="orderStatusTone(o.status)" :label="t(`trip.status.${o.status}`)" />
          <span v-if="o.status === 'completed'" class="hist__money jt-tabular">{{ fmt.money(o.fare) }}</span>
          <span v-else class="hist__money jt-tabular hist__muted">{{ fmt.money(o.fare) }}</span>
        </div>
      </button>

      <div v-if="next" class="hist__more">
        <JButton variant="secondary" block :loading="loading" @click="load(false)">{{ t('common.loading_more') }}</JButton>
      </div>
      <p v-if="error && loaded" class="hist__error" role="alert">{{ errorText(error) }}</p>
    </template>
  </DriverPage>
</template>

<style scoped>
.hist__skeleton {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  padding: 0 var(--jt-side-margin);
}

.hist__state {
  padding: var(--jt-space-8) var(--jt-side-margin);
}

.hist__totals {
  margin: 0 var(--jt-side-margin) var(--jt-space-5);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.hist__totals-label {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.hist__totals-value {
  font: var(--jt-font-title1);
  color: var(--jt-color-green-text);
}

.hist__item {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  width: calc(100% - 2 * var(--jt-side-margin));
  margin: 0 var(--jt-side-margin) var(--jt-space-3);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  text-align: left;
}

.hist__item:active {
  background: var(--jt-color-fill-secondary);
}

.hist__top {
  display: flex;
  justify-content: space-between;
  gap: var(--jt-space-2);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.hist__cat {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  color: var(--jt-color-label);
  font-weight: 600;
}

.hist__date {
  flex: none;
}

.hist__locality {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--jt-color-label-secondary);
  font-weight: 500;
}

.hist__route {
  font: var(--jt-font-body);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
}

.hist__bottom {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--jt-space-2);
}

.hist__money {
  font: var(--jt-font-subheadline);
  font-weight: 600;
}

.hist__muted {
  color: var(--jt-color-label-secondary);
  text-decoration: line-through;
}

.hist__more {
  margin: 0 var(--jt-side-margin);
}

.hist__error {
  margin: var(--jt-space-3) var(--jt-side-margin);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}
</style>
