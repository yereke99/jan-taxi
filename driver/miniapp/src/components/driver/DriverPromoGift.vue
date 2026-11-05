<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { JPromoGift } from '../ui'
import { useFormat } from '../../composables/useFormat'
import { ROUTE } from '../../router/names'
import { useDriverPromotionsStore } from '../../stores/promotions'
import { useTelegramFullscreen } from '../../telegram'

const { t } = useI18n()
const router = useRouter()
const fmt = useFormat()
const promotions = useDriverPromotionsStore()
const tg = useTelegramFullscreen()
const value = computed(() => `+${fmt.money(promotions.gift?.amount ?? 0)}`)
let timer: ReturnType<typeof setInterval> | undefined
function refresh(): void { if (document.visibilityState !== 'hidden' && tg.state.isActive) void promotions.refresh() }
onMounted(() => { refresh(); timer = setInterval(refresh, 60_000); document.addEventListener('visibilitychange', refresh) })
onBeforeUnmount(() => { clearInterval(timer); document.removeEventListener('visibilitychange', refresh) })
</script>

<template>
  <JPromoGift v-if="promotions.gift" :label="t('promotions.gift')" :value="value" :aria-label="`${t('promotions.title')} · ${value}`" @click="router.push({ name: ROUTE.promotions })" />
</template>
