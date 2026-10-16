<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  Floating circular map button (locate, back, menu): 48px Liquid Glass circle
  that respects the safe areas set by its container (§15.1). Icon-only, so a
  label is required for screen readers.
-->
<script setup lang="ts">
import JSpinner from './JSpinner.vue'

withDefaults(
  defineProps<{
    /** Accessible name (the button shows only an icon). */
    label: string
    /** Highlights the icon, e.g. while following the user's location. */
    active?: boolean
    loading?: boolean
    disabled?: boolean
  }>(),
  { active: false, loading: false, disabled: false },
)

const emit = defineEmits<{ click: [event: MouseEvent] }>()
</script>

<template>
  <button
    type="button"
    class="jt-map-button jt-glass"
    :class="{ 'is-active': active }"
    :aria-label="label"
    :aria-busy="loading || undefined"
    :disabled="disabled"
    @click="(e) => !loading && emit('click', e)"
  >
    <JSpinner v-if="loading" color="dark" />
    <slot v-else />
  </button>
</template>

<style scoped>
.jt-map-button {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: var(--jt-fab-size);
  height: var(--jt-fab-size);
  border-radius: 50%;
  color: var(--jt-color-label);
  transition:
    opacity 120ms linear,
    transform var(--jt-duration-fast) var(--jt-ease-spring);
}

.jt-map-button.is-active {
  color: var(--jt-color-blue);
}

.jt-map-button:active:not(:disabled) {
  opacity: var(--jt-opacity-pressed);
  transform: scale(0.94);
}

.jt-map-button:disabled {
  opacity: var(--jt-opacity-disabled);
}
</style>
