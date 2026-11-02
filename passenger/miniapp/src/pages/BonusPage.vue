<!--
  «Бонус балансы» (§23.5): the balance, what is available and what an active
  order holds, and the history of credits and orders paid (newest first,
  cursor pages). Credits are green: money in. Both re-read on realtime
  hints. Opened from the profile or the bot's «Бонустарым» button (start
  parameter bonus).
-->
<script setup lang="ts">
import { computed, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { listBonusHistory } from '../api/promotions'
import type { BonusEntry } from '../api/types'
import ScrollPage from '../components/ScrollPage.vue'
import { JButton, JEmptyState, JListGroup, JListRow, JSkeleton, JSpinner } from '../components/ui'
import { useErrorText } from '../i18n/core'
import { usePromotionsStore } from '../stores/promotions'
import { useSessionStore } from '../stores/session'
import { formatDateTime, formatMoney } from '../utils/format'

const { t } = useI18n()
const errorText = useErrorText()
const promotions = usePromotionsStore()
const session = useSessionStore()

const lang = computed(() => session.language)
const bonus = computed(() => promotions.data?.bonus ?? null)
/** The bonus is switched off for orders for now. */
const paused = computed(() => promotions.data?.redemptions_enabled === false)

// --- History ----------------------------------------------------------------------

const items = ref<BonusEntry[]>([])
const nextCursor = ref<string | null>(null)
const loaded = ref(false)
const loading = ref(false)
const error = shallowRef<unknown>(null)
let request = 0

/** The first page (replaces the list) or the next one after the cursor. */
async function loadHistory(first: boolean): Promise<void> {
  if (!first && (!nextCursor.value || loading.value)) return
  const id = ++request
  loading.value = true
  try {
    const page = await listBonusHistory({ before: first ? null : nextCursor.value })
    if (id !== request) return
    const seen = new Set(first ? [] : items.value.map((e) => e.id))
    items.value = first ? page.items : [...items.value, ...page.items.filter((e) => !seen.has(e.id))]
    nextCursor.value = page.next_cursor
    error.value = null
    loaded.value = true
  } catch (err) {
    if (id === request) error.value = err
  } finally {
    if (id === request) loading.value = false
  }
}

/** "+300 ₸" for money in, "−200 ₸" for money out. */
function signed(amount: number): string {
  return amount > 0 ? `+${formatMoney(amount, lang.value)}` : formatMoney(amount, lang.value)
}

/** "Тапсырыс №1024 · 01.11.2026, 10:00" */
function entryMeta(e: BonusEntry): string {
  const date = formatDateTime(e.created_at, lang.value)
  return e.order_number ? `${t('order.number', { n: e.order_number })} · ${date}` : date
}

onMounted(() => {
  void promotions.refresh()
  void loadHistory(true)
})
// A credit or a spend arrived (promotions.updated): the history has a new row.
watch(() => promotions.changes, () => void loadHistory(true))
</script>

<template>
  <ScrollPage :title="t('bonus.title')">
    <JEmptyState v-if="promotions.unavailable" class="bonus-empty" :title="t('promo.unavailable_title')" :description="t('promo.unavailable_text')" />

    <template v-else>
      <section class="bonus-card">
        <template v-if="bonus">
          <p class="bonus-card__label">{{ t('bonus.balance') }}</p>
          <p class="bonus-card__value jt-tabular">{{ formatMoney(bonus.balance, lang) }}</p>
          <p v-if="paused && bonus.available > 0" class="bonus-card__label" role="status">{{ t('bonus.paused') }}</p>
        </template>
        <template v-else-if="promotions.error && !promotions.loading">
          <p class="bonus-card__label" role="alert">{{ errorText(promotions.error) }}</p>
          <JButton variant="secondary" size="medium" @click="promotions.refresh()">{{ t('common.retry') }}</JButton>
        </template>
        <template v-else>
          <JSkeleton width="40%" height="16px" />
          <JSkeleton width="55%" height="34px" />
        </template>
      </section>

      <JListGroup v-if="bonus" :footer="t('bonus.explainer')">
        <JListRow :title="t('bonus.available')" :value="formatMoney(bonus.available, lang)" />
        <JListRow :title="t('bonus.reserved')" :value="formatMoney(bonus.reserved, lang)" />
      </JListGroup>

      <div v-if="!loaded && loading" class="bonus-skeleton"><JSkeleton height="132px" radius="22px" /></div>
      <JListGroup v-else-if="items.length > 0" :header="t('bonus.history')">
        <JListRow v-for="e in items" :key="e.id" :title="t(`bonus.entry.${e.type}`)" :subtitle="entryMeta(e)">
          <template #accessory>
            <span class="bonus-amount jt-tabular" :class="{ 'is-credit': e.amount > 0 }">{{ signed(e.amount) }}</span>
          </template>
        </JListRow>
      </JListGroup>
      <p v-else-if="loaded" class="bonus-note">{{ t('bonus.empty') }}</p>

      <div v-if="error" class="bonus-error" role="alert">
        <p>{{ errorText(error) }}</p>
        <JButton variant="secondary" size="medium" @click="loadHistory(items.length === 0)">{{ t('common.retry') }}</JButton>
      </div>
      <div v-else-if="nextCursor" class="bonus-more">
        <JSpinner v-if="loading" />
        <JButton v-else variant="plain" size="medium" @click="loadHistory(false)">{{ t('bonus.load_more') }}</JButton>
      </div>
    </template>
  </ScrollPage>
</template>

<style scoped>
.bonus-empty {
  margin-top: var(--jt-space-8);
  padding: 0 var(--jt-side-margin);
}

.bonus-card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--jt-space-2);
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
  padding: var(--jt-space-5) var(--jt-space-4) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.bonus-card__label {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.bonus-card__value {
  font: var(--jt-font-large-title);
  letter-spacing: var(--jt-tracking-large-title);
}

.bonus-skeleton {
  margin: 0 var(--jt-side-margin);
}

.bonus-amount {
  font: var(--jt-font-headline);
}

/* Money in. */
.bonus-amount.is-credit {
  color: var(--jt-color-green-text);
}

.bonus-note {
  margin: 0 calc(var(--jt-side-margin) + var(--jt-space-4));
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.bonus-more {
  display: flex;
  justify-content: center;
  padding: 0 0 var(--jt-space-4);
}

.bonus-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--jt-space-3);
  padding: var(--jt-space-4);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  text-align: center;
}
</style>
