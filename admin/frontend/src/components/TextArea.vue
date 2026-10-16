<!-- Multi-line text field with an optional code-point counter (§11 counting rule). -->
<script setup lang="ts">
import { computed } from 'vue'
import { codePoints } from '../utils/text'

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    modelValue: string
    rows?: number
    maxLength?: number
    placeholder?: string
    invalid?: boolean
    disabled?: boolean
    mono?: boolean
  }>(),
  { rows: 3, maxLength: 0, placeholder: '', invalid: false, disabled: false, mono: false },
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const count = computed(() => codePoints(props.modelValue))
const over = computed(() => props.maxLength > 0 && count.value > props.maxLength)
</script>

<template>
  <div class="area">
    <textarea
      class="area__input"
      :class="{ 'is-invalid': invalid || over, 'jt-mono': mono }"
      :value="modelValue"
      :rows="rows"
      :placeholder="placeholder"
      :disabled="disabled"
      :aria-invalid="invalid || over || undefined"
      v-bind="$attrs"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
    />
    <span v-if="maxLength > 0" class="area__count" :class="{ 'is-over': over }" aria-hidden="true">{{ count }}/{{ maxLength }}</span>
  </div>
</template>

<style scoped>
.area {
  position: relative;
  display: flex;
  flex-direction: column;
}

.area__input {
  width: 100%;
  min-height: 64px;
  padding: 8px 10px;
  border: 0;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  font: var(--jt-font-body);
  line-height: 20px;
  resize: vertical;
  outline: none;
}

.area__input.jt-mono {
  font-family: var(--jt-font-mono);
  font-size: 13px;
}

.area__input:focus {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}

.area__input.is-invalid {
  box-shadow: inset 0 0 0 1px var(--jt-color-red);
}

.area__count {
  align-self: flex-end;
  margin-top: 2px;
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
  font-variant-numeric: tabular-nums;
}

.area__count.is-over {
  color: var(--jt-color-red);
}
</style>
