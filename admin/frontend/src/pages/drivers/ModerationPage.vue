<!--
  Moderation queue: pending applications, oldest first, with a side-by-side
  review of declared data and evidence. Shortcuts: J/K next/previous,
  A approve, C request corrections, R reject.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { driversApi, type AdminDriverDetail, type AdminDriverListItem } from '../../api/drivers'
import { Card, StateView, StatusBadge } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useRealtimeEvents, useResync } from '../../composables/useRealtime'
import { useResource } from '../../composables/useResource'
import DriverActions from './DriverActions.vue'
import DriverReview from './DriverReview.vue'

const QUEUE_SIZE = 100

const { t } = useI18n()
const fmt = useFormat()
const route = useRoute()
const router = useRouter()

const queue = useResource((signal) => driversApi.list({ status: 'pending' }, 1, QUEUE_SIZE, signal))
void queue.reload()

const items = computed<AdminDriverListItem[]>(() => queue.data.value?.items ?? [])
const selectedId = computed(() => (typeof route.query.driver === 'string' ? route.query.driver : (items.value[0]?.id ?? '')))
const detail = useResource((signal) => driversApi.get(selectedId.value, signal))
watch(selectedId, (id) => (id ? void detail.reload() : detail.set(null)), { immediate: true })

const actions = ref<InstanceType<typeof DriverActions> | null>(null)

/** What was asked in the previous round, so a resubmission can be checked against it. */
const previousRound = computed(() => {
  const d = detail.data.value
  if (!d || d.status !== 'pending') return null
  const prev = d.applications.find((a) => a.status === 'changes_requested' && a.round < (d.applications[0]?.round ?? 0))
  return prev && (prev.corrections.length > 0 || prev.decision_comment) ? prev : null
})

function select(id: string): void {
  void router.replace({ query: { ...route.query, driver: id } })
}

function step(delta: number): void {
  const list = items.value
  if (list.length === 0) return
  const i = list.findIndex((d) => d.id === selectedId.value)
  const next = list[Math.min(list.length - 1, Math.max(0, (i < 0 ? 0 : i) + delta))]
  if (next) select(next.id)
}

async function onUpdated(d: AdminDriverDetail): Promise<void> {
  detail.set(d)
  if (d.status === 'pending') return
  // Decided: drop it from the queue and move on to the next application.
  const list = items.value
  const i = list.findIndex((x) => x.id === d.id)
  const next = list[i + 1] ?? list[i - 1]
  await queue.reload({ silent: true })
  if (next && next.id !== d.id) select(next.id)
  else void router.replace({ query: { ...route.query, driver: undefined } })
}

function waited(submittedAt: string | null): string {
  return submittedAt ? fmt.relative(submittedAt) : '—'
}

function onKey(e: KeyboardEvent): void {
  if (e.metaKey || e.ctrlKey || e.altKey) return
  const el = e.target as HTMLElement | null
  if (el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))) return
  if (document.querySelector('[aria-modal="true"]')) return
  const key = e.key.toLowerCase()
  if (key === 'j') step(1)
  else if (key === 'k') step(-1)
  else if (key === 'a') actions.value?.run('approve')
  else if (key === 'c') actions.value?.run('corrections')
  else if (key === 'r') actions.value?.run('reject')
  else return
  e.preventDefault()
}

onMounted(() => document.addEventListener('keydown', onKey))
onBeforeUnmount(() => document.removeEventListener('keydown', onKey))

useRealtimeEvents(['moderation.submitted', 'moderation.approved', 'moderation.rejected', 'moderation.changes_requested'], () => void queue.reload({ silent: true }), 1000)
useResync(() => void queue.reload({ silent: true }))
</script>

<template>
  <section class="queue">
    <aside class="queue__list">
      <header class="queue__head">
        <h2 class="jt-section-title">{{ t('drivers.queue_title') }}</h2>
        <span class="queue__count jt-num">{{ queue.data.value?.total ?? 0 }}</span>
      </header>
      <StateView v-if="queue.error.value || (queue.loading.value && !queue.data.value)" :loading="queue.loading.value" :error="queue.error.value" compact @retry="queue.reload()" />
      <p v-else-if="items.length === 0" class="queue__empty">{{ t('drivers.queue_empty') }}</p>
      <ol v-else class="queue__items">
        <li v-for="d in items" :key="d.id">
          <button type="button" class="queue__item" :class="{ 'is-active': d.id === selectedId }" :aria-current="d.id === selectedId ? 'true' : undefined" @click="select(d.id)">
            <span class="queue__row">
              <span class="jt-mono">{{ d.code }}</span>
              <span class="jt-muted queue__wait">{{ waited(d.submitted_at) }}</span>
            </span>
            <span class="queue__name">{{ d.first_name }} {{ d.last_name }}</span>
            <span class="queue__meta">{{ d.vehicle ? `${d.vehicle.make} ${d.vehicle.model} · ${d.vehicle.plate}` : '—' }}</span>
          </button>
        </li>
      </ol>
      <p class="queue__keys jt-muted">{{ t('drivers.queue_shortcuts') }}</p>
    </aside>

    <div class="queue__review">
      <StateView v-if="selectedId && !detail.data.value" :loading="detail.loading.value" :error="detail.error.value" @retry="detail.reload()" />
      <Card v-else-if="detail.data.value">
        <template #title>
          <RouterLink :to="{ name: 'driver-detail', params: { id: detail.data.value.id } }" class="review-title">
            {{ detail.data.value.code }} · {{ detail.data.value.first_name }} {{ detail.data.value.last_name }}
          </RouterLink>
        </template>
        <template #actions>
          <StatusBadge kind="moderation" :status="detail.data.value.status" />
        </template>
        <p class="jt-muted review-sub">
          {{ t('drivers.round_n', { n: detail.data.value.applications.length }) }} ·
          {{ t('drivers.submitted_at', { time: fmt.dateTime(detail.data.value.submitted_at) }) }}
        </p>
        <div v-if="previousRound" class="previous" role="note">
          <strong>{{ t('drivers.previous_corrections', { n: previousRound.round }) }}</strong>
          <ul>
            <li v-for="c in previousRound.corrections" :key="c.field">
              {{ t(`correction_field.${c.field}`) }} — {{ t(`correction_reason.${c.reason}`) }}<template v-if="c.comment">: {{ c.comment }}</template>
            </li>
          </ul>
          <p v-if="previousRound.decision_comment">{{ previousRound.decision_comment }}</p>
        </div>
        <DriverReview :driver="detail.data.value" />
        <div class="review-actions">
          <DriverActions ref="actions" :driver="detail.data.value" only="moderation" @updated="onUpdated" />
        </div>
      </Card>
    </div>
  </section>
</template>

<style scoped>
.previous {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: var(--jt-space-4);
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-blue-tint);
  color: var(--jt-color-blue-text);
  font: var(--jt-font-small);
}

.previous ul {
  padding-left: var(--jt-space-4);
  list-style: disc;
}

.queue {
  display: grid;
  grid-template-columns: 260px minmax(0, 1fr);
  gap: var(--jt-space-4);
  align-items: start;
}

.queue__list {
  position: sticky;
  top: 0;
  display: flex;
  flex-direction: column;
  max-height: calc(100dvh - var(--jt-topbar-height) - 24px);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
  overflow: hidden;
}

.queue__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--jt-space-4) var(--jt-space-4) var(--jt-space-2);
}

.queue__count {
  min-width: 24px;
  padding: 0 8px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font: 600 12px/20px var(--jt-font-family);
  text-align: center;
}

.queue__empty {
  padding: var(--jt-space-6) var(--jt-space-4);
  color: var(--jt-color-label-secondary);
  text-align: center;
}

.queue__items {
  flex: 1;
  overflow-y: auto;
  padding: 0 var(--jt-space-2) var(--jt-space-2);
}

.queue__item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  width: 100%;
  padding: 10px 12px;
  border-radius: var(--jt-radius-inner);
  text-align: left;
}

.queue__item:hover {
  background: var(--jt-color-hover);
}

.queue__item.is-active {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.queue__item.is-active .jt-muted,
.queue__item.is-active .queue__meta {
  color: rgba(255, 255, 255, 0.7);
}

.queue__row {
  display: flex;
  justify-content: space-between;
  gap: var(--jt-space-2);
}

.queue__wait {
  font: var(--jt-font-caption);
}

.queue__name {
  font: var(--jt-font-body-strong);
}

.queue__meta {
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.queue__keys {
  padding: var(--jt-space-2) var(--jt-space-4) var(--jt-space-3);
  font: var(--jt-font-caption);
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.review-title {
  color: inherit;
}

.review-sub {
  margin-bottom: var(--jt-space-4);
}

.review-actions {
  position: sticky;
  bottom: 0;
  margin: var(--jt-space-4) calc(-1 * var(--jt-space-5)) calc(-1 * var(--jt-space-5));
  padding: var(--jt-space-3) var(--jt-space-5);
  border-radius: 0 0 var(--jt-radius-card) var(--jt-radius-card);
  background: rgba(255, 255, 255, 0.92);
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}
</style>
