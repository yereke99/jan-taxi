<!-- Driver detail: tabs for profile, documents, vehicle, moderation history, wallet, trips and location. -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { driversApi, type AdminDriverDetail } from '../../api/drivers'
import { DetailHeader, StateView, StatusBadge, Tabs } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useRealtimeEvents, useResync } from '../../composables/useRealtime'
import { useResource } from '../../composables/useResource'
import { useAuthStore } from '../../stores/auth'
import DriverActions from './DriverActions.vue'
import DriverHistoryTab from './DriverHistoryTab.vue'
import DriverLocationTab from './DriverLocationTab.vue'
import DriverProfileTab from './DriverProfileTab.vue'
import DriverReview from './DriverReview.vue'
import DriverTripsTab from './DriverTripsTab.vue'
import DriverWalletTab from './DriverWalletTab.vue'

const props = defineProps<{ id: string }>()

type TabKey = 'profile' | 'documents' | 'vehicle' | 'history' | 'wallet' | 'trips' | 'location'

const { t } = useI18n()
const fmt = useFormat()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const res = useResource((signal) => driversApi.get(props.id, signal))
void res.reload()

const driver = computed(() => res.data.value)

const tabs = computed(() => {
  const list: Array<{ value: TabKey; label: string }> = [
    { value: 'profile', label: t('drivers.tab_profile') },
    { value: 'documents', label: t('drivers.tab_documents') },
    { value: 'vehicle', label: t('drivers.tab_vehicle') },
    { value: 'history', label: t('drivers.tab_history') },
  ]
  if (auth.can('finance.view')) list.push({ value: 'wallet', label: t('drivers.tab_wallet') })
  list.push({ value: 'trips', label: t('drivers.tab_trips') }, { value: 'location', label: t('drivers.tab_location') })
  return list
})

const tab = computed<TabKey>({
  get: () => {
    const q = route.query.tab
    return tabs.value.find((x) => x.value === q)?.value ?? 'profile'
  },
  set: (v) => void router.replace({ query: { ...route.query, tab: v === 'profile' ? undefined : v } }),
})

function onUpdated(d: AdminDriverDetail): void {
  res.set(d)
}

useRealtimeEvents(
  ['driver.status_changed', 'moderation.submitted', 'moderation.approved', 'moderation.rejected', 'moderation.changes_requested', 'driver.phone_verified'],
  (ev) => {
    if (ev.data.driver_id === props.id) void res.reload({ silent: true })
  },
)
useResync(() => void res.reload({ silent: true }))
</script>

<template>
  <section class="jt-page">
    <StateView v-if="!driver" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <DetailHeader :title="`${driver.code} · ${driver.first_name} ${driver.last_name}`" :back="{ name: 'drivers' }" :back-label="t('nav.drivers')">
        <template #badges>
          <StatusBadge kind="moderation" :status="driver.status" />
          <span class="online" :class="{ 'is-on': driver.is_online }">{{ driver.is_online ? t('common.online') : t('common.offline') }}</span>
        </template>
        <template #subtitle>
          {{ driver.city ? fmt.name(driver.city.name) : '—' }}
          <template v-if="driver.vehicle"> · {{ driver.vehicle.make }} {{ driver.vehicle.model }} · <span class="jt-mono">{{ driver.vehicle.plate }}</span></template>
          <template v-if="driver.wallet"> · {{ t('finance.available') }}: {{ fmt.money(driver.wallet.available) }}</template>
        </template>
        <template #actions>
          <DriverActions :driver="driver" @updated="onUpdated" />
        </template>
      </DetailHeader>

      <p v-if="driver.status === 'suspended' && driver.suspended_reason" class="banner">
        {{ t('drivers.suspended_banner', { reason: driver.suspended_reason }) }}
      </p>

      <Tabs v-model="tab" :tabs="tabs" :aria-label="t('nav.driver_detail')" />

      <DriverProfileTab v-if="tab === 'profile'" :driver="driver" />
      <div v-else-if="tab === 'documents'" class="panel">
        <DriverReview :driver="driver" section="identity" />
      </div>
      <div v-else-if="tab === 'vehicle'" class="panel">
        <DriverReview :driver="driver" section="vehicle" />
      </div>
      <DriverHistoryTab v-else-if="tab === 'history'" :driver="driver" />
      <DriverWalletTab v-else-if="tab === 'wallet'" :driver="driver" @changed="res.reload({ silent: true })" />
      <DriverTripsTab v-else-if="tab === 'trips'" :driver="driver" />
      <DriverLocationTab v-else-if="tab === 'location'" :location="driver.last_location" :busy="driver.active_order !== null" />
    </template>
  </section>
</template>

<style scoped>
.panel {
  padding: var(--jt-space-5);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.online {
  font: 500 13px/18px var(--jt-font-family);
  color: var(--jt-color-label-secondary);
}

.online.is-on {
  color: var(--jt-color-green-text);
}

.banner {
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}
</style>
