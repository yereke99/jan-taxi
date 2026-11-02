<!--
  Order number in marketing tables: the order page when its id is known,
  otherwise the orders list searched by number; plain text without orders.view.
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import { useAuthStore } from '../../stores/auth'

const props = withDefaults(defineProps<{ number: number | null | undefined; id?: string | null }>(), { id: null })

const auth = useAuthStore()
const to = computed<RouteLocationRaw | null>(() => {
  if (props.number === null || props.number === undefined || !auth.can('orders.view')) return null
  return props.id ? { name: 'order-detail', params: { id: props.id } } : { name: 'orders', query: { q: String(props.number) } }
})
</script>

<template>
  <span v-if="number === null || number === undefined" class="jt-muted">—</span>
  <RouterLink v-else-if="to" :to="to" class="jt-mono" @click.stop>#{{ number }}</RouterLink>
  <span v-else class="jt-mono">#{{ number }}</span>
</template>
