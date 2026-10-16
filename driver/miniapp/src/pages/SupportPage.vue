<!-- Support contacts from the public config (app_settings support.*) and the driver bot. -->
<script setup lang="ts">
import { Bot, Clock3, MessageCircle, Phone } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import DriverPage from '../components/driver/DriverPage.vue'
import { JListGroup, JListRow } from '../components/ui'
import { useFormat } from '../composables/useFormat'
import { pickLocalized } from '../services/config'
import { useSessionStore } from '../stores/session'
import { openTelegram } from '../telegram'
import { formatKzPhone, telHref } from '../utils/phone'

const { t } = useI18n()
const { locale } = useFormat()
const session = useSessionStore()

const support = computed(() => session.config.support)
const hours = computed(() => pickLocalized(support.value.hours, locale.value))
const botUsername = computed(() => session.config.bots.driver_username)
const none = computed(() => !support.value.telegram_link && !support.value.phone)
const code = computed(() => session.driver?.code ?? '')

function openSupportChat(): void {
  if (support.value.telegram_link) openTelegram(support.value.telegram_link)
}

function call(phone: string): void {
  window.location.href = telHref(phone)
}

function openBot(): void {
  if (botUsername.value) openTelegram(`https://t.me/${botUsername.value}`)
}
</script>

<template>
  <DriverPage :title="t('support.title')">
    <p class="sup__text">{{ t('support.text') }}</p>

    <JListGroup :footer="code ? t('support.code_hint', { code }) : ''">
      <JListRow v-if="support.telegram_link" clickable chevron :title="t('support.telegram')" :subtitle="`@${support.telegram_username}`" @click="openSupportChat">
        <template #icon><MessageCircle :size="22" :stroke-width="2" /></template>
      </JListRow>
      <JListRow v-if="support.phone" clickable chevron :title="t('support.phone')" :subtitle="formatKzPhone(support.phone)" @click="call(support.phone)">
        <template #icon><Phone :size="22" :stroke-width="2" /></template>
      </JListRow>
      <JListRow v-if="hours" :title="t('support.hours')" :subtitle="hours">
        <template #icon><Clock3 :size="22" :stroke-width="2" /></template>
      </JListRow>
      <JListRow v-if="botUsername" clickable chevron :title="t('support.bot')" :subtitle="`@${botUsername}`" @click="openBot">
        <template #icon><Bot :size="22" :stroke-width="2" /></template>
      </JListRow>
    </JListGroup>

    <p v-if="none" class="sup__text">{{ t('support.none') }}</p>
  </DriverPage>
</template>

<style scoped>
.sup__text {
  margin: 0 calc(var(--jt-side-margin) + var(--jt-space-1)) var(--jt-space-5);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}
</style>
