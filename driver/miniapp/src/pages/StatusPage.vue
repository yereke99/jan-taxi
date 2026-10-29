<!--
  Application status for drivers who cannot work yet: under review (pending),
  rejected or suspended — what it means, the reason when there is one, and
  the way to support. A moderation decision re-routes automatically
  (realtime moderation.* / driver.status_changed).
-->
<script setup lang="ts">
import { Ban, Hourglass, ShieldAlert } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import DriverPage from '../components/driver/DriverPage.vue'
import { JButton, JListGroup, JListRow } from '../components/ui'
import { useFormat } from '../composables/useFormat'
import { usePolling } from '../composables/usePolling'
import { ROUTE } from '../router/names'
import { useProfileStore } from '../stores/profile'
import { useRegistrationStore } from '../stores/registration'
import { useSessionStore } from '../stores/session'

const { t } = useI18n()
const fmt = useFormat()
const session = useSessionStore()
const reg = useRegistrationStore()
const profile = useProfileStore()
const refreshing = ref(false)

const state = computed(() => session.driverState)
const code = computed(() => session.driver?.code ?? '')
const submittedAt = computed(() => reg.registration?.submitted_at ?? null)
const reason = computed(() => {
  if (state.value === 'rejected') return reg.registration?.rejection_reason ?? null
  if (state.value === 'suspended') return profile.profile?.suspended_reason ?? null
  return null
})

const view = computed(() => {
  switch (state.value) {
    case 'rejected':
      return { icon: Ban, title: t('status.rejected_title'), text: t('status.rejected_text') }
    case 'suspended':
      return { icon: ShieldAlert, title: t('status.suspended_title'), text: t('status.suspended_text') }
    default:
      return { icon: Hourglass, title: t('status.pending_title'), text: t('status.pending_text') }
  }
})

async function refresh(): Promise<void> {
  if (refreshing.value) return
  refreshing.value = true
  try {
    await Promise.all([session.refreshMe(), reg.refresh(), state.value === 'suspended' ? profile.refresh() : Promise.resolve()])
  } finally {
    refreshing.value = false
  }
}

// Realtime moves the driver on a decision; this is the fallback.
usePolling(() => Promise.all([session.refreshMe(),reg.refresh()]), 60_000)

onMounted(() => {
  void reg.refresh()
  if (state.value === 'suspended') void profile.refresh()
})
</script>

<template>
  <DriverPage :title="t('status.title')" :large="false">
    <section class="status">
      <component :is="view.icon" :size="48" :stroke-width="1.5" class="status__icon" aria-hidden="true" />
      <h2 class="status__title">{{ view.title }}</h2>
      <p class="status__text">{{ view.text }}</p>
      <p v-if="state === 'pending' && reg.registration?.verification" class="status__text">{{ t(`status.verification_${reg.registration.verification.status}`) }}</p>
      <p v-if="reason" class="status__reason">{{ t('status.reason', { reason }) }}</p>
      <p v-if="state === 'pending' && submittedAt" class="status__meta">{{ t('status.submitted_at', { time: fmt.dateTime(submittedAt) }) }}</p>
      <p v-if="code" class="status__meta jt-tabular">{{ t('status.code', { code }) }}</p>
    </section>

    <JListGroup :footer="state === 'pending' ? '' : t('status.contact_support')">
      <JListRow v-if="state === 'suspended'" :title="t('nav.wallet')" :to="{ name: ROUTE.wallet }" />
      <JListRow :title="t('support.title')" :to="{ name: ROUTE.support }" />
      <JListRow :title="t('language.title')" :value="t(`languages.${session.language}`)" :to="{ name: ROUTE.language }" />
    </JListGroup>

    <div class="status__actions">
      <JButton variant="secondary" block :loading="refreshing" @click="refresh">{{ t('status.refresh') }}</JButton>
    </div>
  </DriverPage>
</template>

<style scoped>
.status {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--jt-space-2);
  margin: var(--jt-space-4) var(--jt-side-margin) var(--jt-space-6);
  padding: var(--jt-space-8) var(--jt-space-5);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  text-align: center;
}

.status__icon {
  margin-bottom: var(--jt-space-2);
  color: var(--jt-color-label);
}

.status__title {
  font: var(--jt-font-title2);
}

.status__text {
  font: var(--jt-font-body);
  color: var(--jt-color-label-secondary);
}

.status__reason {
  margin-top: var(--jt-space-2);
  font: var(--jt-font-body);
  overflow-wrap: anywhere;
}

.status__meta {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.status__actions {
  margin: 0 var(--jt-side-margin);
}
</style>
