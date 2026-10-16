<!-- From/to date pickers (YYYY-MM-DD, local dates) for list filters and reports. -->
<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(defineProps<{ from?: string | null; to?: string | null }>(), { from: null, to: null })
const emit = defineEmits<{ 'update:from': [value: string | null]; 'update:to': [value: string | null] }>()

const { t } = useI18n()
const id = useId()
const invalid = computed(() => Boolean(props.from && props.to && props.from > props.to))

function value(e: Event): string | null {
  return (e.target as HTMLInputElement).value || null
}
</script>

<template>
  <div class="range" :class="{ 'is-invalid': invalid }">
    <label :for="`${id}-from`" class="range__label">{{ t('date_range.from') }}</label>
    <input
      :id="`${id}-from`"
      type="date"
      :value="from ?? ''"
      :max="to ?? undefined"
      :aria-invalid="invalid || undefined"
      @change="emit('update:from', value($event))"
    />
    <label :for="`${id}-to`" class="range__label">{{ t('date_range.to') }}</label>
    <input
      :id="`${id}-to`"
      type="date"
      :value="to ?? ''"
      :min="from ?? undefined"
      :aria-invalid="invalid || undefined"
      @change="emit('update:to', value($event))"
    />
    <p v-if="invalid" class="range__error" role="alert">{{ t('date_range.invalid') }}</p>
  </div>
</template>

<style scoped>
.range {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.range__label {
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

input {
  height: var(--jt-control-height);
  padding: 0 8px;
  border: 0;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  font: var(--jt-font-body);
  outline: none;
}

input:focus {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}

.is-invalid input {
  box-shadow: inset 0 0 0 1px var(--jt-color-red);
}

.range__error {
  flex-basis: 100%;
  font: var(--jt-font-small);
  color: var(--jt-color-red);
}
</style>
