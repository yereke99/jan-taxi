<!--
  Personal-data consent (Law of the RK No. 94-V): what is collected, why,
  where it is stored (Kazakhstan), who sees it, retention, withdrawal and the
  support contacts. The version is sent with the application.
-->
<script setup lang="ts">
import { Check } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { CONSENT_SECTIONS, PD_CONSENT_VERSION } from '../../../features/registration/consent'
import { pickLocalized } from '../../../services/config'
import { useFormat } from '../../../composables/useFormat'
import { useRegistrationStore } from '../../../stores/registration'
import { useSessionStore } from '../../../stores/session'
import { haptics, openTelegram } from '../../../telegram'
import { formatKzPhone, telHref } from '../../../utils/phone'

const props = defineProps<{ showErrors: boolean }>()

const { t } = useI18n()
const { locale } = useFormat()
const reg = useRegistrationStore()
const session = useSessionStore()

const support = computed(() => session.config.support)
const hours = computed(() => pickLocalized(support.value.hours, locale.value))
const hasContacts = computed(() => Boolean(support.value.telegram_link || support.value.phone))

function toggle(): void {
  haptics.selection()
  reg.setConsent(!reg.consentGiven)
}

function openSupport(): void {
  if (support.value.telegram_link) openTelegram(support.value.telegram_link)
}
</script>

<template>
  <div class="step">
    <p class="step__text">{{ t('registration.consent.intro') }}</p>

    <article class="consent">
      <section v-for="s in CONSENT_SECTIONS" :key="s" class="consent__section">
        <h3 class="consent__title">{{ t(`registration.consent.sections.${s}.title`) }}</h3>
        <p class="consent__text">{{ t(`registration.consent.sections.${s}.text`) }}</p>
      </section>
      <section class="consent__section">
        <h3 class="consent__title">{{ t('registration.consent.contacts') }}</h3>
        <p v-if="!hasContacts" class="consent__text">{{ t('registration.consent.contacts_none') }}</p>
        <p v-if="support.telegram_link" class="consent__text">
          <button type="button" class="consent__link" @click="openSupport">@{{ support.telegram_username }}</button>
        </p>
        <p v-if="support.phone" class="consent__text">
          <a class="consent__link" :href="telHref(support.phone)">{{ formatKzPhone(support.phone) }}</a>
        </p>
        <p v-if="hours" class="consent__text">{{ hours }}</p>
      </section>
      <p class="consent__version">{{ t('registration.consent.version', { version: PD_CONSENT_VERSION }) }}</p>
    </article>

    <button
      type="button"
      class="consent__check"
      role="checkbox"
      :aria-checked="reg.consentGiven"
      :class="{ 'is-on': reg.consentGiven, 'is-invalid': props.showErrors && !reg.consentGiven }"
      @click="toggle"
    >
      <span class="consent__box" aria-hidden="true"><Check v-if="reg.consentGiven" :size="18" :stroke-width="3" /></span>
      <span class="consent__label">{{ t('registration.consent.agree') }}</span>
    </button>
    <p v-if="props.showErrors && !reg.consentGiven" class="step__note consent__error" role="alert">{{ t('registration.consent.required') }}</p>
  </div>
</template>

<style scoped src="./step.css"></style>
<style scoped>
.consent {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.consent__title {
  margin-bottom: var(--jt-space-1);
  font: var(--jt-font-headline);
}

.consent__text {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.consent__link {
  font: inherit;
  color: var(--jt-color-blue-text);
}

.consent__version {
  font: var(--jt-font-caption);
  color: var(--jt-color-label-tertiary);
}

.consent__check {
  display: flex;
  align-items: flex-start;
  gap: var(--jt-space-3);
  margin: 0 var(--jt-side-margin) var(--jt-space-5);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  text-align: left;
}

.consent__check.is-invalid {
  box-shadow: inset 0 0 0 1px var(--jt-color-red);
}

.consent__box {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  border: 2px solid var(--jt-color-label-tertiary);
  color: #fff;
  transition: background-color var(--jt-duration-fast) var(--jt-ease);
}

.consent__check.is-on .consent__box {
  border-color: var(--jt-color-primary);
  background: var(--jt-color-primary);
}

.consent__label {
  font: var(--jt-font-body);
}

.consent__error {
  margin-top: calc(-1 * var(--jt-space-3));
  color: var(--jt-color-red-text);
}
</style>
