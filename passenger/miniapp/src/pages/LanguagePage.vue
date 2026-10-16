<!-- Language: kk / ru / en with a checkmark; saved server-side (PATCH /me/preferences), applied immediately. -->
<script setup lang="ts">
import { Check } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ScrollPage from '../components/ScrollPage.vue'
import { JListGroup, JListRow, JSpinner, toast } from '../components/ui'
import { LOCALES, useErrorText, type Locale } from '../i18n/core'
import { useSessionStore } from '../stores/session'
import { haptics } from '../telegram'

const { t } = useI18n()
const errorText = useErrorText()
const session = useSessionStore()
const pending = ref<Locale | null>(null)

async function choose(l: Locale): Promise<void> {
  if (l === session.language || pending.value) return
  pending.value = l
  try {
    await session.setLanguage(l)
    haptics.selection()
    toast.success(t('language.saved'), 1500)
  } catch (err) {
    toast.error(errorText(err))
  } finally {
    pending.value = null
  }
}
</script>

<template>
  <ScrollPage :title="t('language.title')">
    <JListGroup :footer="t('language.hint')">
      <JListRow
        v-for="l in LOCALES"
        :key="l"
        clickable
        :chevron="false"
        :title="t(`languages.${l}`)"
        :aria-pressed="session.language === l"
        @click="choose(l)"
      >
        <template #accessory>
          <JSpinner v-if="pending === l" />
          <Check v-else-if="session.language === l" :size="20" :stroke-width="2.5" aria-hidden="true" />
        </template>
      </JListRow>
    </JListGroup>
  </ScrollPage>
</template>
