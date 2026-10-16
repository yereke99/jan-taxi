<!-- Header of a detail page: back link, title with badges, subtitle and actions. -->
<script setup lang="ts">
import { ChevronLeft } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { RouteLocationRaw } from 'vue-router'

withDefaults(defineProps<{ title: string; subtitle?: string; back: RouteLocationRaw; backLabel?: string }>(), {
  subtitle: '',
  backLabel: '',
})

const { t } = useI18n()
</script>

<template>
  <header class="detail-head">
    <RouterLink :to="back" class="detail-head__back">
      <ChevronLeft :size="16" aria-hidden="true" />
      {{ backLabel || t('common.back') }}
    </RouterLink>
    <div class="detail-head__row">
      <div class="detail-head__titles">
        <h2 class="detail-head__title">
          <span>{{ title }}</span>
          <slot name="badges" />
        </h2>
        <p v-if="subtitle || $slots.subtitle" class="detail-head__subtitle"><slot name="subtitle">{{ subtitle }}</slot></p>
      </div>
      <div v-if="$slots.actions" class="detail-head__actions"><slot name="actions" /></div>
    </div>
  </header>
</template>

<style scoped>
.detail-head {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

.detail-head__back {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  align-self: flex-start;
  color: var(--jt-color-label-secondary);
  font: var(--jt-font-small);
}

.detail-head__back:hover {
  color: var(--jt-color-label);
  text-decoration: none;
}

.detail-head__row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--jt-space-4);
}

.detail-head__titles {
  min-width: 0;
}

.detail-head__title {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--jt-space-2);
  font: var(--jt-font-large-title);
}

.detail-head__subtitle {
  margin-top: 2px;
  color: var(--jt-color-label-secondary);
}

.detail-head__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--jt-space-2);
}
</style>
