<!--
  Loading / error state for a page or panel. 403 shows the friendly
  "no permission" view, 404 a "not found" message; other errors the
  translated error with a retry button.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useErrors } from '../composables/useErrors'
import { isApiError } from '../services/api'
import AppButton from './AppButton.vue'
import AppSpinner from './AppSpinner.vue'

const props = withDefaults(defineProps<{ loading?: boolean; error?: unknown; compact?: boolean }>(), {
  loading: false,
  compact: false,
})
const emit = defineEmits<{ retry: [] }>()

const { t } = useI18n()
const errors = useErrors()

const kind = computed<'forbidden' | 'not_found' | 'error' | null>(() => {
  if (!props.error) return null
  if (isApiError(props.error) && props.error.status === 403) return 'forbidden'
  if (isApiError(props.error) && props.error.status === 404) return 'not_found'
  return 'error'
})
</script>

<template>
  <div v-if="kind" class="state" :class="{ 'state--compact': compact }" role="alert">
    <template v-if="kind === 'forbidden'">
      <h2 class="state__title">{{ t('pages.forbidden_title') }}</h2>
      <p class="state__text">{{ t('pages.forbidden_text') }}</p>
    </template>
    <template v-else-if="kind === 'not_found'">
      <h2 class="state__title">{{ t('pages.not_found_title') }}</h2>
      <p class="state__text">{{ t('pages.record_not_found') }}</p>
    </template>
    <template v-else>
      <h2 class="state__title">{{ t('pages.load_failed') }}</h2>
      <p class="state__text">{{ errors.text(error) }}</p>
      <AppButton variant="primary" size="sm" @click="emit('retry')">{{ t('common.retry') }}</AppButton>
    </template>
  </div>
  <div v-else-if="loading" class="state state--loading" :class="{ 'state--compact': compact }">
    <AppSpinner :size="20" :label="t('common.loading')" />
  </div>
</template>

<style scoped>
.state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--jt-space-2);
  min-height: 240px;
  padding: var(--jt-space-6);
  text-align: center;
}

.state--compact {
  min-height: 120px;
  padding: var(--jt-space-4);
}

.state__title {
  font: var(--jt-font-title3);
}

.state__text {
  max-width: 420px;
  color: var(--jt-color-label-secondary);
  margin-bottom: var(--jt-space-2);
}

.state--loading {
  color: var(--jt-color-label-secondary);
}
</style>
