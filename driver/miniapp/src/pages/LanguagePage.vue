<!-- Interface language (PATCH /me/preferences): shared with the driver bot. -->
<script setup lang="ts">
import { Check } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import DriverPage from '../components/driver/DriverPage.vue'
import { JListGroup, JListRow, JSpinner, toast } from '../components/ui'
import { LOCALES, type Locale } from '../i18n/core'
import { useSessionStore } from '../stores/session'
import { haptics } from '../telegram'

const { t } = useI18n()
const session = useSessionStore()
const pending = ref<Locale | null>(null)

async function choose(l: Locale): Promise<void> {
  if (l === session.language || pending.value) return
  haptics.selection()
  pending.value = l
  try {
    await session.setLanguage(l)
  } catch {
    toast.error(t('language.failed'))
  } finally {
    pending.value = null
  }
}
</script>

<template>
  <DriverPage :title="t('language.title')">
    <JListGroup :footer="t('language.hint')">
      <JListRow v-for="l in LOCALES" :key="l" clickable :title="t(`languages.${l}`)" :disabled="pending !== null && pending !== l" @click="choose(l)">
        <template #accessory>
          <JSpinner v-if="pending === l" size="small" />
          <Check v-else-if="session.language === l" :size="20" :stroke-width="2.5" :aria-label="t('common.select')" />
        </template>
      </JListRow>
    </JListGroup>
  </DriverPage>
</template>
