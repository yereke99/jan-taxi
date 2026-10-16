<script setup lang="ts">
import { Box, CarFront, Package } from '@lucide/vue'
import { computed } from 'vue'
import type { Category } from '../../api/types'

const props = withDefaults(defineProps<{ category: Category; size?: number }>(), { size: 20 })

const icon = computed(() => (props.category === 'parcel' ? Package : props.category === 'other' ? Box : CarFront))
</script>

<template>
  <component :is="icon" :size="size" :stroke-width="2" aria-hidden="true" />
</template>
