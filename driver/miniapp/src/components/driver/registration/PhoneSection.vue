<!--
  Phone number on the first registration page (§11): Telegram's
  requestContact sends the contact to the driver bot, which verifies
  contact.user_id == from.id and reports it to the API. The app never reads or
  sends the number itself; it waits for driver.phone_verified (and polls
  GET /driver/registration). A number the API refused as another driver's
  (409 phone_taken) is shared again.
-->
<script setup lang="ts">
import { CircleCheck, Lock, Smartphone } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { phoneDone } from '../../../features/registration/form'
import { useRegistrationStore } from '../../../stores/registration'
import { useSessionStore } from '../../../stores/session'
import { openTelegram } from '../../../telegram'
import { formatKzPhone } from '../../../utils/phone'
import { JButton, JSpinner } from '../../ui'

const props = defineProps<{ showErrors: boolean }>()

const { t } = useI18n()
const reg = useRegistrationStore()
const session = useSessionStore()

const r = computed(() => reg.registration!)
const locked = computed(() => !reg.editable.has('phone'))
const flagged = computed(() => r.value.corrections.some((c) => c.field === 'phone'))
const busy = computed(() => reg.phoneFlow === 'requesting' || reg.phoneFlow === 'waiting')
const verified = computed(() => r.value.phone_verified && !busy.value)
/** A flagged or taken phone can be shared again even though one is on file. */
const canShare = computed(() => !locked.value && (!verified.value || flagged.value || reg.taken.phone))
const shareLabel = computed(() => {
  if (verified.value) return t('registration.phone.share_again')
  return reg.phoneFlow === 'idle' ? t('registration.phone.share') : t('registration.phone.retry')
})
const phoneText = computed(() => (r.value.phone ? formatKzPhone(r.value.phone) : ''))
const botLink = computed(() => {
  const u = session.config.bots.driver_username
  return u ? `https://t.me/${u}` : null
})

const problem = computed(() => {
  switch (reg.phoneFlow) {
    case 'cancelled':
      return verified.value ? '' : t('registration.phone.cancelled')
    case 'failed':
      return verified.value ? '' : t('registration.phone.failed')
    case 'unavailable':
      return t('registration.phone.unavailable')
    case 'timeout':
      return t('registration.phone.timeout')
  }
  if (reg.taken.phone) return t('registration.phone.taken')
  return props.showErrors && !phoneDone(r.value) ? t('registration.form_errors.phone_required') : ''
})

function openBot(): void {
  if (botLink.value) openTelegram(botLink.value)
}
</script>

<template>
  <section class="phone-section" :aria-label="t('registration.personal.phone')">
    <h3 class="phone-section__header">{{ t('registration.personal.phone') }}</h3>
    <div class="step__block phone">
      <template v-if="verified">
        <CircleCheck :size="40" :stroke-width="1.75" class="phone__ok" aria-hidden="true" />
        <p class="phone__title">{{ t('registration.phone.verified') }}</p>
        <p class="phone__number jt-tabular">{{ phoneText }}</p>
        <p v-if="locked" class="phone__locked"><Lock :size="14" :stroke-width="2" aria-hidden="true" /> {{ t('registration.locked') }}</p>
      </template>

      <template v-else-if="busy">
        <JSpinner size="large" color="dark" />
        <p class="phone__title">{{ reg.phoneFlow === 'requesting' ? t('registration.phone.requesting') : t('registration.phone.waiting') }}</p>
      </template>

      <template v-else>
        <Smartphone :size="40" :stroke-width="1.5" class="phone__icon" aria-hidden="true" />
        <p class="phone__title">{{ t('registration.phone.title') }}</p>
        <p class="phone__text">{{ t('registration.phone.text') }}</p>
      </template>

      <p v-if="problem" class="phone__problem" role="alert">{{ problem }}</p>
    </div>

    <div v-if="canShare || busy" class="phone__actions">
      <JButton block :variant="verified ? 'secondary' : 'primary'" :loading="busy" @click="reg.requestPhone()">
        {{ shareLabel }}
      </JButton>
      <JButton v-if="botLink && (reg.phoneFlow === 'unavailable' || reg.phoneFlow === 'failed' || reg.phoneFlow === 'timeout')" variant="secondary" block @click="openBot">
        {{ t('registration.phone.open_bot') }}
      </JButton>
    </div>
    <p class="step__note phone__privacy">{{ t('registration.phone.privacy') }}</p>
  </section>
</template>

<style scoped src="./step.css"></style>
<style scoped>
.phone-section__header {
  padding: 0 calc(var(--jt-side-margin) + var(--jt-space-4)) 7px;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  text-transform: uppercase;
  letter-spacing: 0.1px;
}

.phone {
  align-items: center;
  text-align: center;
  padding: var(--jt-space-6) var(--jt-space-4);
  gap: var(--jt-space-3);
}

.phone__ok {
  color: var(--jt-color-green);
}

.phone__icon {
  color: var(--jt-color-label);
}

.phone__title {
  font: var(--jt-font-headline);
}

.phone__text {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.phone__number {
  font: var(--jt-font-title2);
}

.phone__locked {
  display: inline-flex;
  align-items: center;
  gap: var(--jt-space-1);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.phone__problem {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-red-text);
}

.phone__actions {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
}

.phone__privacy {
  margin-top: 0;
}
</style>
