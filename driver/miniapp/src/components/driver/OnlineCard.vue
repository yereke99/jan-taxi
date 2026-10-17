<!--
  The big online switch (PATCH /driver/availability) and what blocks it:
  insufficient balance → top-up, missing location → enable it, outside
  Kazakhstan, profile not approved. While online, location problems that stop
  dispatch are shown under the switch.
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { suggestedTopUpAmount } from '../../features/orders/acceptOutcome'
import { useFormat } from '../../composables/useFormat'
import { useDriverErrorText } from '../../composables/useDriverErrorText'
import { ROUTE } from '../../router/names'
import { useDutyStore, type OnlineOutcome } from '../../stores/duty'
import { useLocationStore } from '../../stores/location'
import { useSessionStore } from '../../stores/session'
import { useEnsureLocation } from '../../features/location/useEnsureLocation'
import { canOpenLocationSettings, haptics, openLocationSettings } from '../../telegram'
import { JButton, JSwitch, toast } from '../ui'

const { t } = useI18n()
const fmt = useFormat()
const errorText = useDriverErrorText()
const router = useRouter()
const duty = useDutyStore()
const location = useLocationStore()
const session = useSessionStore()
const gps = useEnsureLocation()

const problem = ref<Exclude<OnlineOutcome, { ok: true }> | null>(null)

const online = computed(() => duty.isOnline)

const locationIssue = computed(() => {
  if (!online.value) return null
  switch (location.issue) {
    case 'denied':
      return t('duty.location_denied_banner')
    case 'unavailable':
      return t('duty.location_unavailable_banner')
    case 'outside_service_area':
      return t('duty.location_outside_banner')
    default:
      return null
  }
})

const topUpAmount = computed(() => {
  const p = problem.value
  if (!p || p.reason !== 'insufficient_balance') return null
  return suggestedTopUpAmount(p.required, p.available, p.topupMin ?? session.config.topup?.min ?? null)
})

async function toggle(next: boolean): Promise<void> {
  problem.value = null
  const res = await duty.setOnline(next)
  if (res.ok) {
    haptics.notification('success')
    if (res.online && res.withoutLocation) toast.show({ message: t('duty.without_location'), duration: 6000 })
    return
  }
  haptics.notification('error')
  if (res.reason === 'error') {
    if (res.error) toast.error(errorText(res.error))
    return
  }
  problem.value = res
}

function topUp(): void {
  const amount = topUpAmount.value
  void router.push({ name: ROUTE.topup, query: amount ? { amount: String(amount) } : {} })
}

/** Sends a fresh fix (or opens Telegram's location settings after a denial), then goes online. */
async function enableLocation(): Promise<void> {
  if (await gps.fix()) await toggle(true)
}

/** While online: the location settings after a denial, else one more fix. */
function fixIssue(): void {
  if (location.issue === 'denied' && canOpenLocationSettings()) void openLocationSettings()
  else void gps.ensure()
}

const locationProblem = computed(() => {
  if (gps.state.value === 'failed') return t('orders.location_failed')
  if (gps.state.value === 'outside') return t('duty.outside_text')
  return t('duty.location_text')
})
</script>

<template>
  <section class="online" :class="{ 'is-on': online }">
    <div class="online__row">
      <div class="online__text">
        <p class="online__title">{{ online ? t('home.online') : t('home.offline') }}</p>
        <p class="online__hint">{{ online ? t('home.online_hint') : t('home.offline_hint') }}</p>
      </div>
      <JSwitch class="online__switch" :model-value="online" :busy="duty.switching" :label="t('home.online_switch')" @update:model-value="toggle" />
    </div>

    <div v-if="problem" class="online__problem" role="alert">
      <template v-if="problem.reason === 'insufficient_balance'">
        <p class="online__problem-title">{{ t('duty.insufficient_title') }}</p>
        <p class="online__problem-text">
          {{ t('duty.insufficient_text', { amount: fmt.money(problem.required ?? 0), available: fmt.money(problem.available ?? 0) }) }}
        </p>
        <JButton size="medium" block @click="topUp">{{ t('wallet.top_up') }}</JButton>
      </template>
      <template v-else-if="problem.reason === 'location_required'">
        <p class="online__problem-title">{{ t('duty.location_title') }}</p>
        <p class="online__problem-text">{{ locationProblem }}</p>
        <JButton size="medium" variant="secondary" block :loading="gps.state.value === 'locating'" @click="enableLocation">
          {{ gps.state.value === 'denied' && canOpenLocationSettings() ? t('common.open_settings') : t('orders.enable_location') }}
        </JButton>
      </template>
      <template v-else-if="problem.reason === 'outside_service_area'">
        <p class="online__problem-title">{{ t('duty.outside_title') }}</p>
        <p class="online__problem-text">{{ t('duty.outside_text') }}</p>
      </template>
      <p v-else class="online__problem-text">{{ t('duty.not_approved') }}</p>
    </div>

    <p v-else-if="locationIssue" class="online__issue" role="status">
      {{ locationIssue }}
      <button type="button" class="online__link" @click="fixIssue">
        {{ location.issue === 'denied' && canOpenLocationSettings() ? t('common.open_settings') : t('common.retry') }}
      </button>
    </p>
  </section>
</template>

<style scoped>
.online {
  margin: 0 var(--jt-side-margin) var(--jt-space-5);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.online__row {
  display: flex;
  align-items: center;
  gap: var(--jt-space-4);
  min-height: 64px;
}

.online__text {
  flex: 1;
  min-width: 0;
}

.online__title {
  font: var(--jt-font-title2);
}

.online.is-on .online__title {
  color: var(--jt-color-green-text);
}

.online__hint {
  margin-top: 2px;
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

/* A larger switch: drivers toggle it at arm's length, often in sunlight. */
.online__switch {
  flex: none;
  transform: scale(1.25);
  transform-origin: right center;
}

.online__problem {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  margin-top: var(--jt-space-4);
  padding-top: var(--jt-space-4);
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.online__problem-title {
  font: var(--jt-font-headline);
}

.online__problem-text {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.online__issue {
  margin-top: var(--jt-space-3);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-red-text);
}

.online__link {
  margin-left: var(--jt-space-1);
  font: inherit;
  font-weight: 600;
  color: var(--jt-color-blue-text);
}
</style>
