<!-- Support contacts from the public config (settings support.*): Telegram chat and phone. -->
<script setup lang="ts">
import { MessageCircle, Phone } from '@lucide/vue'
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import ScrollPage from '../components/ScrollPage.vue'
import { JEmptyState, JListGroup, JListRow } from '../components/ui'
import { pickLocalized } from '../services/config'
import { useSessionStore } from '../stores/session'
import { openTelegram } from '../telegram'
import { formatKzPhone, telHref } from '../utils/phone'

const { t } = useI18n()
const session = useSessionStore()

const support = computed(() => session.config.support)
const hours = computed(() => pickLocalized(support.value.hours, session.language))
const hasContacts = computed(() => Boolean(support.value.telegram_link || support.value.phone))

function call(): void {
  const phone = support.value.phone
  if (phone) window.location.href = telHref(phone)
}

onMounted(() => void session.ensureConfig())
</script>

<template>
  <ScrollPage :title="t('support.title')">
    <p class="support-lead">{{ t('support.lead') }}</p>

    <JListGroup v-if="hasContacts" :footer="hours ? `${t('support.hours')}: ${hours}` : ''">
      <JListRow
        v-if="support.telegram_link"
        clickable
        chevron
        :title="t('support.telegram')"
        :subtitle="support.telegram_username ? `@${support.telegram_username}` : ''"
        @click="openTelegram(support.telegram_link)"
      >
        <template #icon><MessageCircle :size="22" :stroke-width="1.75" /></template>
      </JListRow>
      <JListRow v-if="support.phone" clickable chevron :title="t('support.phone')" :subtitle="formatKzPhone(support.phone)" @click="call">
        <template #icon><Phone :size="22" :stroke-width="1.75" /></template>
      </JListRow>
    </JListGroup>

    <JEmptyState v-else class="support-empty" :title="t('support.title')" :description="t('support.empty')" />
  </ScrollPage>
</template>

<style scoped>
.support-lead {
  margin: 0 calc(var(--jt-side-margin) + var(--jt-space-4)) var(--jt-space-5);
  font: var(--jt-font-callout);
  color: var(--jt-color-label-secondary);
}

.support-empty {
  padding: 0 var(--jt-side-margin);
}
</style>
