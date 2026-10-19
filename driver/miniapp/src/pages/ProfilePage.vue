<!--
  The driver's profile (GET /driver/profile): photo, name, code, masked
  phone, vehicle with its class, seats and cargo space (§21) and plate,
  rating, trips, status and city. Moderated data changes only through
  registration corrections.
-->
<script setup lang="ts">
import { Star } from '@lucide/vue'
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import DriverPage from '../components/driver/DriverPage.vue'
import PrivateImage from '../components/driver/PrivateImage.vue'
import { JBadge, JButton, JEmptyState, JListGroup, JListRow, JSkeleton } from '../components/ui'
import { useFormat } from '../composables/useFormat'
import { formatPlate } from '../features/registration/form'
import { useVehicleText } from '../features/vehicle/useVehicleText'
import { useErrorText } from '../i18n/core'
import { ROUTE } from '../router/names'
import { pickLocalized } from '../services/config'
import { useProfileStore } from '../stores/profile'
import { useSessionStore } from '../stores/session'
import { moderationStatusTone } from '../utils/status'

const { t } = useI18n()
const fmt = useFormat()
const errorText = useErrorText()
const session = useSessionStore()
const store = useProfileStore()
const vt = useVehicleText()

const p = computed(() => store.profile)
const name = computed(() => (p.value ? `${p.value.first_name} ${p.value.last_name}`.trim() : ''))
const vehicle = computed(() => {
  const v = p.value?.vehicle
  return v ? [v.color, v.make, v.model].filter(Boolean).join(' ') : ''
})
const vclass = computed(() => {
  const v = p.value?.vehicle
  if (!v?.vehicle_class) return null
  return {
    name: vt.className(v.vehicle_class, v.vehicle_class_name),
    seats: v.seats !== null ? String(v.seats) : '—',
    cargo: v.cargo_volume_l !== null ? vt.litres(v.cargo_volume_l) : '—',
  }
})
const plate = computed(() => {
  const v = p.value?.vehicle
  return v ? (formatPlate(v.plate) ?? v.plate) : ''
})
const rating = computed(() => {
  const x = p.value
  if (!x || x.rating === null) return t('profile.rating_none')
  const r = fmt.locale.value === 'en' ? x.rating.toFixed(2) : x.rating.toFixed(2).replace('.', ',')
  return t('profile.rating_value', { rating: r, count: x.rating_count })
})

onMounted(() => void store.refresh())
</script>

<template>
  <DriverPage :title="t('profile.title')" :tab-bar="session.isApproved">
    <div v-if="!p && store.loading" class="prof__skeleton" aria-busy="true">
      <JSkeleton circle height="88px" />
      <JSkeleton width="50%" height="24px" />
      <JSkeleton height="200px" radius="22px" />
    </div>

    <div v-else-if="!p && store.error" class="prof__state">
      <JEmptyState :title="t('app.load_failed_title')" :description="errorText(store.error)">
        <template #action><JButton block @click="store.refresh()">{{ t('common.retry') }}</JButton></template>
      </JEmptyState>
    </div>

    <template v-else-if="p">
      <section class="prof__head">
        <div class="prof__avatar"><PrivateImage :url="p.avatar?.url ?? null" :alt="name" round /></div>
        <h2 class="prof__name">{{ name }}</h2>
        <p class="prof__code jt-tabular">{{ t('profile.code') }}: {{ p.code }}</p>
        <p class="prof__rating"><Star :size="16" :stroke-width="2" aria-hidden="true" /> {{ rating }}</p>
      </section>

      <JListGroup>
        <JListRow :title="t('profile.status')">
          <template #accessory><JBadge :tone="moderationStatusTone(p.status)" :label="t(`moderation_status.${p.status}`)" /></template>
        </JListRow>
        <JListRow :title="t('profile.phone')" :value="session.me?.phone_masked ?? t('profile.phone_none')" />
        <JListRow v-if="p.city" :title="t('profile.city')" :value="pickLocalized(p.city.name, fmt.locale.value)" />
        <JListRow :title="t('profile.trips')" :value="String(p.completed_trips)" />
      </JListGroup>

      <JListGroup :header="t('profile.vehicle')" :footer="t('profile.moderated_note')">
        <JListRow :title="vehicle || '—'" />
        <template v-if="vclass">
          <JListRow :title="t('profile.vehicle_class')" :value="vclass.name" />
          <JListRow :title="t('profile.seats')" :value="vclass.seats" />
          <JListRow :title="t('profile.cargo')" :value="vclass.cargo" />
        </template>
        <JListRow :title="t('profile.plate')">
          <template #accessory><span class="prof__plate jt-tabular">{{ plate || '—' }}</span></template>
        </JListRow>
      </JListGroup>

      <JListGroup :header="t('profile.settings')">
        <JListRow v-if="session.isApproved" :title="t('profile.history')" :to="{ name: ROUTE.history }" />
        <JListRow :title="t('profile.language')" :value="t(`languages.${session.language}`)" :to="{ name: ROUTE.language }" />
        <JListRow :title="t('profile.support')" :to="{ name: ROUTE.support }" />
      </JListGroup>
    </template>
  </DriverPage>
</template>

<style scoped>
.prof__skeleton {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--jt-space-4);
  padding: 0 var(--jt-side-margin);
}

.prof__state {
  padding: var(--jt-space-8) var(--jt-side-margin);
}

.prof__head {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
  text-align: center;
}

.prof__avatar {
  width: 96px;
  height: 96px;
  margin-bottom: var(--jt-space-3);
}

.prof__name {
  font: var(--jt-font-title2);
  overflow-wrap: anywhere;
}

.prof__code,
.prof__rating {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.prof__rating {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.prof__plate {
  padding: 2px 8px;
  border: 1.5px solid var(--jt-color-label);
  border-radius: 6px;
  font: var(--jt-font-headline);
  letter-spacing: 0.5px;
}
</style>
