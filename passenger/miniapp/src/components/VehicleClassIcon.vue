<!--
  Monochrome side view of a vehicle class (ARCHITECTURE §21.1 icon names):
  sedan, universal (estate), minivan, microbus, cargo_van and truck. Drawn in
  currentColor with the windows cut out in the background colour, so it
  follows the surrounding text colour.
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { VehicleIcon } from '../services/config'

const props = withDefaults(defineProps<{ icon: VehicleIcon; size?: number }>(), { size: 40 })

interface Shape {
  /** Body outline (filled). */
  body: string
  /** Windows (cut out). */
  windows: string
}

// 48 × 24 view box; the wheels sit on y = 19.5 under every body.
const SHAPES: Record<VehicleIcon, Shape> = {
  sedan: {
    body: 'M3 18.5v-4.2c0-1.2.8-2.2 2-2.4l7-1.3 5.3-4.3c.9-.7 2-1.1 3.2-1.1h8.9c1.3 0 2.5.6 3.3 1.5l3.2 3.8 6 1.1c1.2.2 2.1 1.3 2.1 2.5v4.4z',
    windows: 'M18.6 7.6c.5-.4 1.2-.6 1.9-.6h3.7v3.7h-9.4zm7.4-.6h3.2c.7 0 1.4.3 1.8.8l2.4 2.9H26z',
  },
  universal: {
    body: 'M3 18.5v-4.2c0-1.2.8-2.2 2-2.4l7-1.3 5.3-4.3c.9-.7 2-1.1 3.2-1.1h18.1c1.1 0 2.1.6 2.6 1.6l2.3 4.4c.3.6.5 1.2.5 1.9v5.4z',
    windows: 'M18.6 7.6c.5-.4 1.2-.6 1.9-.6h3.7v3.7h-9.4zm7.4-.6h5.4v3.7H26zm7.2 0h4.6c.5 0 .9.3 1.1.7l1.5 3h-7.2z',
  },
  minivan: {
    body: 'M3 18.5v-5.6c0-1 .6-1.9 1.5-2.3l5.8-2.6 4.6-3.6c.9-.7 2-1.1 3.2-1.1h21.4c1.2 0 2.2.7 2.7 1.7l1.9 4.3c.2.5.3 1 .3 1.6v7.6z',
    windows: 'M15.8 6.8c.5-.4 1.1-.6 1.7-.6h4.1v4.4H10.8zm7.6-.6h6.3v4.4h-6.3zm8.1 0h6.1c.5 0 .9.3 1.1.7l1.7 3.7h-8.9z',
  },
  microbus: {
    body: 'M3 18.5V9.8c0-.8.3-1.6.9-2.2l3.4-3.6c.6-.6 1.4-1 2.2-1h31.8c2 0 3.7 1.7 3.7 3.7v11.8z',
    windows: 'M9.2 5.2h4.6v5H5.6zm6.4 0h6.6v5h-6.6zm8.4 0h6.6v5H24zm8.4 0h6.6v5h-6.6zm8.4 0h1.9c.8 0 1.4.6 1.4 1.4v3.6h-3.3z',
  },
  cargo_van: {
    body: 'M3 18.5V9.8c0-.8.3-1.6.9-2.2l3.4-3.6c.6-.6 1.4-1 2.2-1h31.8c2 0 3.7 1.7 3.7 3.7v11.8z',
    windows: 'M9.2 5.2h4.8v5H5.6z',
  },
  truck: {
    body: 'M3 18.5V5.2C3 4 4 3 5.2 3h22.6c1.2 0 2.2 1 2.2 2.2v13.3zm28.5 0V8.6c0-.9.7-1.6 1.6-1.6h6.4c.9 0 1.7.5 2.1 1.2l2.8 4.6c.4.6.6 1.3.6 2v3.7z',
    windows: 'M34.3 8.8h5.1c.4 0 .7.2.9.5l2.3 3.8h-8.3z',
  },
}

const shape = computed(() => SHAPES[props.icon] ?? SHAPES.sedan)
const height = computed(() => Math.round((props.size * 24) / 48))
</script>

<template>
  <svg class="vc-icon" :width="size" :height="height" viewBox="0 0 48 24" aria-hidden="true" focusable="false">
    <path :d="shape.body" fill="currentColor" />
    <path :d="shape.windows" class="vc-icon__window" />
    <circle cx="12.5" cy="19.5" r="3.6" fill="currentColor" class="vc-icon__wheel" />
    <circle cx="36" cy="19.5" r="3.6" fill="currentColor" class="vc-icon__wheel" />
    <circle cx="12.5" cy="19.5" r="1.3" class="vc-icon__window" />
    <circle cx="36" cy="19.5" r="1.3" class="vc-icon__window" />
  </svg>
</template>

<style scoped>
.vc-icon {
  flex: none;
  display: block;
}

.vc-icon__window {
  fill: var(--jt-color-bg);
}

.vc-icon__wheel {
  stroke: var(--jt-color-bg);
  stroke-width: 1.4;
}
</style>
