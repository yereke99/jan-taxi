<!-- Notifications: the Telegram outbox (orders.manage) and the template editor (config.manage). -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { Tabs } from '../../components'
import { useAuthStore } from '../../stores/auth'
import OutboxTable from './OutboxTable.vue'
import TemplatesEditor from './TemplatesEditor.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

type TabKey = 'outbox' | 'templates'
const tabs = computed(() => {
  const list: Array<{ value: TabKey; label: string }> = [{ value: 'outbox', label: t('system.outbox') }]
  if (auth.can('config.manage')) list.push({ value: 'templates', label: t('system.templates') })
  return list
})

const tab = computed<TabKey>({
  get: () => (route.query.tab === 'templates' && auth.can('config.manage') ? 'templates' : 'outbox'),
  set: (v) => void router.replace({ query: v === 'outbox' ? {} : { tab: v } }),
})
</script>

<template>
  <section class="jt-page">
    <Tabs v-if="tabs.length > 1" v-model="tab" :tabs="tabs" :aria-label="t('nav.notifications')" />
    <OutboxTable v-if="tab === 'outbox'" />
    <TemplatesEditor v-else />
  </section>
</template>
