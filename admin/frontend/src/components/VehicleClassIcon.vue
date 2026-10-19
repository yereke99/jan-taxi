<!-- Monochrome glyph of a vehicle class icon (sedan, universal, minivan, microbus, cargo_van, truck). -->
<script setup lang="ts">
import { Bus, BusFront, Car, CarFront, Truck, Van } from '@lucide/vue'
import { computed, type Component } from 'vue'

const props = withDefaults(defineProps<{ icon: string | null | undefined; size?: number }>(), { size: 18 })

const ICONS: Record<string, Component> = {
  sedan: Car,
  universal: CarFront,
  minivan: BusFront,
  microbus: Bus,
  cargo_van: Van,
  truck: Truck,
}

const glyph = computed(() => ICONS[props.icon ?? ''] ?? Car)
</script>

<template>
  <component :is="glyph" :size="size" aria-hidden="true" class="vc-icon" />
</template>

<style scoped>
.vc-icon {
  flex: none;
  color: var(--jt-color-label);
}
</style>
