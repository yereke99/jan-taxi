<!--
  "Сапарым" tab without a known trip: looks up the passenger's active order
  and opens it; when there is none, an empty state offers to order a ride.
-->
<script setup lang="ts">
import { Route } from '@lucide/vue'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { JButton, JSheet, JSpinner } from '../../components/ui'
import { ROUTE } from '../../router/names'
import { useStageScreen } from '../../stage/mapStage'
import { useLocationStore } from '../../stores/location'
import { useOrdersStore } from '../../stores/orders'

const { t } = useI18n()
const router = useRouter()
const stage = useStageScreen()
const orders = useOrdersStore()

const location = useLocationStore()

stage.controls.locate = true

const loading = ref(true)

// Without a trip the map shows where the passenger is (once; panning wins).
let centred = false
let userMoved = false
const offMove = stage.onMoveStart((user) => {
  if (user) userMoved = true
})
onBeforeUnmount(offMove)
watch(
  () => [stage.map.value, location.current] as const,
  ([map, loc]) => {
    if (!map || !loc || centred || userMoved) return
    centred = true
    stage.centerOn(loc, 16)
  },
  { immediate: true },
)

onMounted(async () => {
  await orders.loadActive()
  const active = orders.active[0]
  if (active) {
    await router.replace({ name: ROUTE.order, params: { id: active.id } })
    return
  }
  loading.value = false
})
</script>

<template>
  <JSheet snap="content" :snap-points="['content']" tab-bar :aria-label="t('tabs.trip')" @height="(h) => (stage.sheetHeight.value = h)">
    <div v-if="loading" class="trip-empty" role="status"><JSpinner /></div>
    <div v-else class="trip-empty">
      <span class="trip-empty__icon"><Route :size="28" :stroke-width="1.75" aria-hidden="true" /></span>
      <h2 class="trip-empty__title">{{ t('trip.empty_title') }}</h2>
      <p class="trip-empty__text">{{ t('trip.empty_text') }}</p>
      <JButton block @click="router.replace({ name: ROUTE.home })">{{ t('trip.order_now') }}</JButton>
    </div>
  </JSheet>
</template>

<style scoped>
.trip-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--jt-space-2);
  padding: var(--jt-space-2) 0 var(--jt-space-1);
  text-align: center;
}

.trip-empty__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 56px;
  height: 56px;
  border-radius: 50%;
  background: var(--jt-color-fill-secondary);
}

.trip-empty__title {
  font: var(--jt-font-title3);
}

.trip-empty__text {
  margin-bottom: var(--jt-space-3);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}
</style>
