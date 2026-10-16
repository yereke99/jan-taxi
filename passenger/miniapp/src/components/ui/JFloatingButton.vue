<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!-- Kept for compatibility: the floating map button is JMapButton (48px glass circle, §15.1). -->
<script setup lang="ts">
import JMapButton from './JMapButton.vue'

withDefaults(
  defineProps<{
    label: string
    active?: boolean
    loading?: boolean
    disabled?: boolean
  }>(),
  { active: false, loading: false, disabled: false },
)

const emit = defineEmits<{ click: [event: MouseEvent] }>()
</script>

<template>
  <JMapButton :label="label" :active="active" :loading="loading" :disabled="disabled" @click="(e) => emit('click', e)">
    <slot />
  </JMapButton>
</template>
