<!-- String-valued select for list filters: '' is the "all" choice shown as the placeholder. -->
<script setup lang="ts">
import { computed } from 'vue'
import SelectInput from './SelectInput.vue'
import type { SelectOption } from './types'

const props = defineProps<{ modelValue: string; options: SelectOption<string>[]; placeholder: string; ariaLabel?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const value = computed(() => (props.modelValue === '' ? null : props.modelValue))
</script>

<template>
  <SelectInput :model-value="value" :options="options" :placeholder="placeholder" :aria-label="ariaLabel ?? placeholder" @update:model-value="(v) => emit('update:modelValue', v ?? '')" />
</template>
