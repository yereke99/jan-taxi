<!-- Driver profile: identity, Telegram account, availability, moderation dates and consent. -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { isDefaultRegion, type CityRef } from '../../api/common'
import type { AdminDriverDetail } from '../../api/drivers'
import { Card, KeyValueList, type KeyValueItem } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { welcomeState } from './bonus'

const props = defineProps<{ driver: AdminDriverDetail }>()

const { t } = useI18n()
const fmt = useFormat()

/** The driver's region (registered city, or the nationwide default region KAZ). */
function regionText(c: CityRef | null): string | null {
  if (!c) return null
  const label = `${fmt.name(c.name)} · ${c.code}`
  return isDefaultRegion(c) ? t('drivers.region_default', { region: label }) : label
}

const person = computed<KeyValueItem[]>(() => {
  const d = props.driver
  return [
    { key: 'code', label: t('drivers.code'), value: d.code, mono: true },
    { key: 'name', label: t('drivers.full_name'), value: `${d.first_name} ${d.last_name}`.trim() },
    { key: 'phone', label: t('common.phone'), value: d.phone, mono: true },
    { key: 'phone_verified', label: t('drivers.phone_verified'), value: d.phone_verified ? fmt.dateTime(d.phone_verified_at) : t('common.no') },
    { key: 'city', label: t('drivers.region'), value: regionText(d.city) },
    { key: 'rating', label: t('drivers.rating'), value: d.rating === null ? null : `${d.rating.toFixed(2)} (${d.rating_count})` },
    { key: 'trips', label: t('drivers.trips'), value: fmt.count(d.completed_trips) },
  ]
})

const account = computed<KeyValueItem[]>(() => {
  const u = props.driver.user
  return [
    { key: 'tg_id', label: t('drivers.telegram_id'), value: String(u.telegram_user_id), mono: true },
    { key: 'username', label: t('drivers.telegram_username'), value: u.username ? `@${u.username}` : null, mono: true },
    { key: 'bot_started', label: t('drivers.bot_started'), value: fmt.dateTime(u.driver_bot_started_at) },
    { key: 'bot_blocked', label: t('drivers.bot_blocked'), value: u.driver_bot_blocked_at ? fmt.dateTime(u.driver_bot_blocked_at) : t('common.no') },
    { key: 'blocked', label: t('drivers.user_blocked'), value: u.is_blocked ? t('common.yes') : t('common.no') },
  ]
})

function welcomeText(): string {
  const w = welcomeState(props.driver)
  if (w.kind === 'granted') return `${fmt.money(w.amount)} · ${fmt.dateTime(w.at)}`
  return w.kind === 'awaiting_approval' ? t('bonus.welcome_awaiting') : t('bonus.welcome_not_granted')
}

const activity = computed<KeyValueItem[]>(() => {
  const d = props.driver
  return [
    { key: 'online', label: t('drivers.online'), value: d.is_online ? t('drivers.online_since', { time: fmt.dateTime(d.online_since) }) : t('common.offline') },
    { key: 'heartbeat', label: t('drivers.last_heartbeat'), value: d.last_heartbeat_at ? fmt.relative(d.last_heartbeat_at) : null },
    { key: 'active_order', label: t('drivers.active_order'), value: d.active_order ? `#${d.active_order.number}` : null },
    { key: 'created', label: t('drivers.registered_at'), value: fmt.dateTime(d.created_at) },
    { key: 'submitted', label: t('drivers.submitted'), value: fmt.dateTime(d.submitted_at) },
    { key: 'approved', label: t('drivers.approved_at'), value: fmt.dateTime(d.approved_at) },
    { key: 'welcome', label: t('bonus.welcome_title'), value: welcomeText() },
    { key: 'suspended', label: t('drivers.suspended_at'), value: d.suspended_at ? `${fmt.dateTime(d.suspended_at)} — ${d.suspended_reason ?? ''}` : null },
    { key: 'consent', label: t('drivers.pd_consent'), value: d.pd_consent_at ? [fmt.dateTime(d.pd_consent_at), d.pd_consent_version].filter(Boolean).join(' · ') : t('common.no') },
  ]
})
</script>

<template>
  <div class="profile">
    <Card :title="t('drivers.person')">
      <KeyValueList :items="person" />
    </Card>
    <Card :title="t('drivers.activity')">
      <KeyValueList :items="activity">
        <template #value-active_order>
          <RouterLink v-if="driver.active_order" :to="{ name: 'order-detail', params: { id: driver.active_order.id } }">
            #{{ driver.active_order.number }} · {{ t(`order_status.${driver.active_order.status}`) }}
          </RouterLink>
          <span v-else>—</span>
        </template>
      </KeyValueList>
    </Card>
    <Card :title="t('drivers.telegram_account')">
      <KeyValueList :items="account" />
    </Card>
    <Card v-if="driver.corrections.length" :title="t('drivers.open_corrections')">
      <ul class="corr">
        <li v-for="(c, i) in driver.corrections" :key="i">
          <strong>{{ t(`correction_field.${c.field}`) }}</strong> — {{ t(`correction_reason.${c.reason}`) }}<template v-if="c.comment">: {{ c.comment }}</template>
        </li>
      </ul>
    </Card>
  </div>
</template>

<style scoped>
.profile {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--jt-space-4);
  align-items: start;
}

.corr {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding-left: var(--jt-space-3);
  list-style: disc;
}
</style>
