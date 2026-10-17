<!--
  2GIS hand-off for the remaining stops (utils/twogis.ts): "Open in 2GIS"
  (the full route where 2GIS accepts via points, otherwise the next stop),
  always "Next stop only", and a visible note whenever the full route could
  not be passed. Links are opened from the tap itself (Telegram requires a
  user gesture) with openLink; only https links are used.
-->
<script setup lang="ts">
import { Navigation } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Stop } from '../../api/types'
import type { Fix } from '../../features/location/locationSender'
import { useSessionStore } from '../../stores/session'
import { haptics, openExternal, useTelegramFullscreen } from '../../telegram'
import { normalizeTwoGisBase, planNavigation } from '../../features/navigation/twogis'
import { toast } from '../ui'

const props = defineProps<{ remaining: readonly Stop[]; position: Fix | null }>()

const { t } = useI18n()
const session = useSessionStore()
const tg = useTelegramFullscreen()

const plan = computed(() =>
  planNavigation({
    base: normalizeTwoGisBase(session.config.navigation.twogis_base_url),
    platform: tg.state.platform,
    origin: props.position ? { lat: props.position.lat, lon: props.position.lon } : null,
    remaining: props.remaining.map((s) => ({ lat: s.lat, lon: s.lon })),
  }),
)

function open(url: string): void {
  haptics.impact('light')
  if (!openExternal(url)) toast.error(t('trip.open_failed'))
}
</script>

<template>
  <section v-if="plan" class="nav">
    <button type="button" class="nav__main" @click="open(plan.primaryUrl)">
      <Navigation :size="20" :stroke-width="2.25" aria-hidden="true" />
      {{ t('trip.open_2gis') }}
    </button>
    <!-- Only when the main button passes the whole route; otherwise both open the next stop. -->
    <button v-if="plan.primaryMode === 'full'" type="button" class="nav__next" @click="open(plan.nextUrl)">{{ t('trip.next_only') }}</button>
    <p v-if="plan.note" class="nav__note">{{ t(`trip.note_${plan.note}`) }}</p>
  </section>
</template>

<style scoped>
.nav {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  margin-bottom: var(--jt-space-4);
}

.nav__main,
.nav__next {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--jt-space-2);
  width: 100%;
  border-radius: var(--jt-radius-capsule);
  font: var(--jt-font-headline);
}

.nav__main {
  height: var(--jt-button-height);
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label);
}

.nav__next {
  height: var(--jt-button-height-medium);
  color: var(--jt-color-label);
  font: var(--jt-font-subheadline);
  font-weight: 600;
}

.nav__main:active,
.nav__next:active {
  opacity: var(--jt-opacity-pressed);
}

.nav__note {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  text-align: center;
}
</style>
