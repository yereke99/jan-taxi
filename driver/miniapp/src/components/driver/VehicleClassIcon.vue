<!--
  Monochrome side view of a vehicle class (ARCHITECTURE §21: sedan,
  universal, minivan, microbus, cargo_van, truck). Inline SVG drawn with
  currentColor, so it follows the text colour and both colour schemes.
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { VehicleIcon } from '../../features/vehicle/classes'

const props = withDefaults(defineProps<{ icon: VehicleIcon; width?: number }>(), { width: 56 })

interface Shape {
  /** Body outline with the wheel arches. */
  body: string
  /** Windows and panel lines. */
  details: string
  /** Wheel centres (x) on the ground line. */
  wheels: number[]
}

// viewBox 0 0 64 32; wheels r=3.6 centred on y=24, arches r=5.5.
const SHAPES: Record<VehicleIcon, Shape> = {
  sedan: {
    body: 'M3.5 24V18.6c0-1.7 1.2-3.1 2.9-3.4L17 13.6l5.9-5.2c.8-.7 1.8-1.1 2.9-1.1h11.4c1.1 0 2.1.4 2.9 1.2l5.3 5.2 10.4 1.1c1.9.2 3.4 1.8 3.4 3.7V24h-5.2a5.5 5.5 0 0 0-11 0H21.5a5.5 5.5 0 0 0-11 0Z',
    details: 'M20.2 13.4l4.6-4h6.3v4Zm13.1 0v-4h4.4l4.1 4Z',
    wheels: [16, 48],
  },
  universal: {
    body: 'M3.5 24V18.6c0-1.7 1.2-3.1 2.9-3.4L17 13.6l5.9-5.2c.8-.7 1.8-1.1 2.9-1.1h27.9c1 0 1.9.5 2.5 1.3l3.4 4.6c.6.8.9 1.7.9 2.7V24h-5.2a5.5 5.5 0 0 0-11 0H21.5a5.5 5.5 0 0 0-11 0Z',
    details: 'M20.2 13.4l4.6-4h7.6v4Zm14.4 0v-4h17.6l2.9 4Z',
    wheels: [16, 48],
  },
  minivan: {
    body: 'M3.5 24v-7.4c0-1.5.9-2.8 2.3-3.3l4.6-1.8 6.3-5.6c.8-.7 1.8-1.1 2.9-1.1h35.5c2.3 0 4.3 1.6 4.8 3.8l.9 4c.1.5.2 1 .2 1.5V24h-5.2a5.5 5.5 0 0 0-11 0H21.5a5.5 5.5 0 0 0-11 0Z',
    details: 'M12.4 12.6l5.9-5.2h9.1v5.2Zm17.2 0V7.4h11v5.2Zm13.2 0V7.4h12.5l1.2 5.2ZM29.6 15.2v5.6',
    wheels: [16, 48],
  },
  microbus: {
    body: 'M3.5 24V7.2c0-2 1.6-3.7 3.7-3.7h50.6c2.1 0 3.7 1.7 3.7 3.7V24h-5.2a5.5 5.5 0 0 0-11 0H19.5a5.5 5.5 0 0 0-11 0Z',
    details: 'M3.5 13.2h5.8V7.4H5.6M13.2 7.4h7.6v5.8h-7.6Zm11.6 0h7.6v5.8h-7.6Zm11.6 0H44v5.8h-7.6Zm11.6 0h7.6v5.8H48Z',
    wheels: [14, 50],
  },
  cargo_van: {
    body: 'M3.5 24v-7.6c0-1.3.7-2.5 1.9-3.2l3.4-1.9 5-6.1c.7-.9 1.8-1.4 2.9-1.4h39.6c2.1 0 3.7 1.7 3.7 3.7V24h-5.2a5.5 5.5 0 0 0-11 0H19.5a5.5 5.5 0 0 0-11 0Z',
    details: 'M10.8 11.6l4.9-5.8h5.1v5.8ZM22.8 5.8v15M24.8 13.4h3',
    wheels: [14, 50],
  },
  truck: {
    body: 'M3.5 24v-9.4c0-.9.3-1.7.8-2.4l3.6-4.6c.7-.9 1.8-1.4 2.9-1.4H18V24h-1.5a5.5 5.5 0 0 0-11 0ZM20 19.5V5.2c0-1 .8-1.7 1.7-1.7h37.6c1 0 1.7.8 1.7 1.7v14.3ZM18 24h5.4M31.6 24h13.8M53.6 24H61',
    details: 'M6.6 13.2l3.7-4.6h4.9v4.6Z',
    wheels: [11, 27.5, 49.5],
  },
}

const shape = computed(() => SHAPES[props.icon] ?? SHAPES.sedan)
const height = computed(() => Math.round((props.width * 32) / 64))
</script>

<template>
  <svg
    class="vicon"
    :width="width"
    :height="height"
    viewBox="0 0 64 32"
    fill="none"
    stroke="currentColor"
    stroke-width="1.7"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    <path :d="shape.body" />
    <path :d="shape.details" stroke-width="1.4" />
    <circle v-for="x in shape.wheels" :key="x" :cx="x" cy="24" r="3.4" />
    <circle v-for="x in shape.wheels" :key="`hub-${x}`" :cx="x" cy="24" r="0.9" fill="currentColor" stroke="none" />
  </svg>
</template>

<style scoped>
.vicon {
  display: block;
  flex: none;
}
</style>
