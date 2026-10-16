<!--
  "Become a driver": what it takes, and a button that opens the driver bot's
  Mini App on its registration screen (direct link from the public config).
  Registration itself happens in the driver app.
-->
<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import ScrollPage from '../components/ScrollPage.vue'
import { JButton } from '../components/ui'
import { useSessionStore } from '../stores/session'
import { directLink, openTelegram } from '../telegram'

const { t } = useI18n()
const session = useSessionStore()

const link = computed(() => {
  const b = session.config.bots
  return b.driver_register_link ?? b.driver_link ?? (b.driver_username ? directLink(b.driver_username, 'register') : null)
})

const steps = computed(() => [t('become_driver.step_register'), t('become_driver.step_review'), t('become_driver.step_work')])

function open(): void {
  if (link.value) openTelegram(link.value)
}

onMounted(() => void session.ensureConfig())
</script>

<template>
  <ScrollPage :title="t('become_driver.title')">
    <div class="driver-page">
      <p class="driver-page__lead">{{ t('become_driver.lead') }}</p>
      <ol class="driver-page__steps">
        <li v-for="(s, i) in steps" :key="i" class="driver-page__step">
          <span class="driver-page__num" aria-hidden="true">{{ i + 1 }}</span>
          <span>{{ s }}</span>
        </li>
      </ol>
      <p class="driver-page__note">{{ t('become_driver.note') }}</p>
      <JButton v-if="link" block @click="open">{{ t('become_driver.open_bot') }}</JButton>
      <p v-else class="driver-page__note" role="status">{{ t('become_driver.unavailable') }}</p>
    </div>
  </ScrollPage>
</template>

<style scoped>
.driver-page {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-5);
  margin: 0 var(--jt-side-margin);
}

.driver-page__lead {
  font: var(--jt-font-body);
}

.driver-page__steps {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.driver-page__step {
  display: flex;
  align-items: flex-start;
  gap: var(--jt-space-3);
  font: var(--jt-font-callout);
}

.driver-page__num {
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font: var(--jt-font-footnote);
  font-weight: 600;
}

.driver-page__note {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}
</style>
