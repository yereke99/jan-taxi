<!-- Monochrome icon of a service category. -->
<script setup lang="ts">
import { Boxes, CarFront, Package } from '@lucide/vue'
import { computed } from 'vue'
import type { Category } from '../utils/status'

const props = withDefaults(defineProps<{ category: Category; size?: number }>(), { size: 22 })
const icon = computed(() => ({ taxi: CarFront, parcel: Package, other: Boxes })[props.category])
</script>

<template>
  <component :is="icon" :size="size" :stroke-width="1.75" aria-hidden="true" />
</template>
