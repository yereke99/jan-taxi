<!-- Budget usage of a campaign: committed + spent of the limit, with a thin bar (no bar without a limit). -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Campaign } from '../../api/marketing'
import { useFormat } from '../../composables/useFormat'
import { budgetUsage } from './campaignModel'

const props = defineProps<{ campaign: Pick<Campaign, 'budget_limit' | 'usage' | 'template'> }>()

const { t } = useI18n()
const fmt = useFormat()
const u = computed(() => budgetUsage(props.campaign))
const text = computed(() =>
  u.value.limit === null
    ? t('marketing.budget_used_unlimited', { used: fmt.money(u.value.used) })
    : t('marketing.budget_used', { used: fmt.money(u.value.used), limit: fmt.money(u.value.limit) }),
)
</script>

<template>
  <span v-if="campaign.template === 'broadcast_only'" class="jt-muted">—</span>
  <span v-else class="usage">
    <span class="jt-num">{{ text }}</span>
    <span v-if="u.share !== null" class="usage__track" role="meter" :aria-valuenow="Math.round(u.share * 100)" aria-valuemin="0" aria-valuemax="100" :aria-label="text">
      <span class="usage__fill" :class="{ 'is-full': u.share >= 1 }" :style="{ width: `${Math.round(u.share * 100)}%` }" />
    </span>
  </span>
</template>

<style scoped>
.usage {
  display: inline-flex;
  flex-direction: column;
  gap: 4px;
  min-width: 140px;
}

.usage__track {
  display: block;
  height: 4px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-tertiary);
  overflow: hidden;
}

.usage__fill {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--jt-color-primary);
}

.usage__fill.is-full {
  background: var(--jt-color-red);
}
</style>
