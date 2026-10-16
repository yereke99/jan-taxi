<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!-- iOS activity indicator: eight fading spokes. -->
<script setup lang="ts">
withDefaults(
  defineProps<{
    size?: 'small' | 'large'
    /** 'dark' on light backgrounds, 'light' on black buttons. */
    color?: 'dark' | 'light' | 'gray'
    /** Accessible label; when empty the spinner is decorative. */
    label?: string
  }>(),
  { size: 'small', color: 'gray', label: '' },
)

const SPOKES = 8
</script>

<template>
  <span
    class="jt-spinner"
    :class="[`jt-spinner--${size}`, `jt-spinner--${color}`]"
    :role="label ? 'progressbar' : undefined"
    :aria-label="label || undefined"
    :aria-hidden="label ? undefined : 'true'"
  >
    <span
      v-for="i in SPOKES"
      :key="i"
      class="jt-spinner__spoke"
      :style="{ transform: `rotate(${(i - 1) * (360 / SPOKES)}deg)`, animationDelay: `${((i - 1 - SPOKES) * 1) / SPOKES}s` }"
    />
  </span>
</template>

<style scoped>
.jt-spinner {
  position: relative;
  display: inline-block;
  flex: none;
  width: 20px;
  height: 20px;
}

.jt-spinner--large {
  width: 37px;
  height: 37px;
}

.jt-spinner__spoke {
  position: absolute;
  left: calc(50% - 1.25px);
  top: 0;
  width: 2.5px;
  height: 28%;
  border-radius: 2px;
  transform-origin: 50% 178%;
  background: currentColor;
  animation: jt-spinner-fade 1s linear infinite;
}

.jt-spinner--large .jt-spinner__spoke {
  left: calc(50% - 2px);
  width: 4px;
}

.jt-spinner--dark {
  color: var(--jt-color-label);
}

.jt-spinner--light {
  color: var(--jt-color-on-primary);
}

.jt-spinner--gray {
  color: var(--jt-color-gray);
}

@keyframes jt-spinner-fade {
  from {
    opacity: 1;
  }
  to {
    opacity: 0.2;
  }
}

@media (prefers-reduced-motion: reduce) {
  .jt-spinner__spoke {
    animation-duration: 2s;
  }
}
</style>
