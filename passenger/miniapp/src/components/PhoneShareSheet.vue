<!--
  Pre-order prompt to share the phone through Telegram (ARCHITECTURE §22.5),
  driven by the phone store: why it is asked (so the driver can call), who
  sees the number, "Share number" (Telegram's own contact dialog) and "Not
  now". While the bot confirms the number the button shows progress; "Not
  now" still lets the order go ahead at once.
-->
<script setup lang="ts">
import { Phone } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { usePhoneStore } from '../stores/phone'
import ModalPanel from './ModalPanel.vue'
import { JButton, toast } from './ui'

const { t } = useI18n()
const phone = usePhoneStore()
const busy = computed(() => phone.flow !== 'idle')

async function share(): Promise<void> {
  const outcome = await phone.share()
  if (outcome === 'verified') {
    toast.success(t('phone.saved'))
  } else if (outcome === 'timeout') {
    toast.show(t('phone.not_confirmed'))
  }
}
</script>

<template>
  <ModalPanel :open="phone.open" :label="t('phone.title')" :locked="phone.flow === 'requesting'" @close="phone.skip()">
    <div class="phone-ask">
      <span class="phone-ask__icon" aria-hidden="true"><Phone :size="26" :stroke-width="2" /></span>
      <h2 class="phone-ask__title">{{ t('phone.title') }}</h2>
      <p class="phone-ask__text">{{ t('phone.text') }}</p>
      <p class="phone-ask__note">{{ t('phone.note') }}</p>
      <p class="phone-ask__status" role="status" aria-live="polite">{{ phone.flow === 'waiting' ? t('phone.waiting') : '' }}</p>
    </div>
    <template #footer>
      <JButton block :loading="busy" @click="share">{{ t('phone.share') }}</JButton>
      <JButton block variant="plain" :disabled="phone.flow === 'requesting'" @click="phone.skip()">{{ t('phone.skip') }}</JButton>
    </template>
  </ModalPanel>
</template>

<style scoped>
.phone-ask {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--jt-space-2);
  text-align: center;
}

.phone-ask__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 56px;
  height: 56px;
  margin-bottom: var(--jt-space-1);
  border-radius: 50%;
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.phone-ask__title {
  font: var(--jt-font-title3);
}

.phone-ask__text {
  font: var(--jt-font-callout);
}

.phone-ask__note {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

/* Reserved line so the panel does not jump when the status appears. */
.phone-ask__status {
  min-height: 18px;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label);
}
</style>
