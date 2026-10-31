<!--
  The personal-data consent (Law of the RK No. 94-V) as one checkbox above
  «Тіркелуді аяқтау». The full text opens under it: what is collected, why,
  where it is stored (Kazakhstan), who sees it, retention, withdrawal and the
  support contacts. Its version is sent when the registration is finished.
-->
<script setup lang="ts">
import { Check, ChevronDown } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { CONSENT_SECTIONS, PD_CONSENT_VERSION } from '../../../features/registration/consent'
import { pickLocalized } from '../../../services/config'
import { useFormat } from '../../../composables/useFormat'
import { useRegistrationStore } from '../../../stores/registration'
import { useSessionStore } from '../../../stores/session'
import { haptics, openTelegram } from '../../../telegram'
import { formatKzPhone, telHref } from '../../../utils/phone'

const props = defineProps<{ invalid: boolean }>()

const { t } = useI18n()
const { locale } = useFormat()
const reg = useRegistrationStore()
const session = useSessionStore()
const expanded = ref(false)

const support = computed(() => session.config.support)
const hours = computed(() => pickLocalized(support.value.hours, locale.value))
const hasContacts = computed(() => Boolean(support.value.telegram_link || support.value.phone))
const showError = computed(() => props.invalid && !reg.consentGiven)

function toggle(): void {
  haptics.selection()
  reg.setConsent(!reg.consentGiven)
}

function openSupport(): void {
  if (support.value.telegram_link) openTelegram(support.value.telegram_link)
}
</script>

<template>
  <section class="consent" :class="{ 'is-invalid': showError }">
    <button type="button" class="consent__check" role="checkbox" :aria-checked="reg.consentGiven" :class="{ 'is-on': reg.consentGiven }" @click="toggle">
      <span class="consent__box" aria-hidden="true"><Check v-if="reg.consentGiven" :size="18" :stroke-width="3" /></span>
      <span class="consent__label">{{ t('registration.consent.agree') }}</span>
    </button>
    <button type="button" class="consent__more" :aria-expanded="expanded" @click="expanded = !expanded">
      <span>{{ expanded ? t('registration.consent.hide') : t('registration.consent.read') }}</span>
      <ChevronDown :size="16" :stroke-width="2.25" class="consent__chevron" :class="{ 'is-open': expanded }" aria-hidden="true" />
    </button>

    <article v-if="expanded" class="consent__body">
      <p class="consent__text">{{ t('registration.consent.intro') }}</p>
      <section v-for="s in CONSENT_SECTIONS" :key="s">
        <h3 class="consent__title">{{ t(`registration.consent.sections.${s}.title`) }}</h3>
        <p class="consent__text">{{ t(`registration.consent.sections.${s}.text`) }}</p>
      </section>
      <section>
        <h3 class="consent__title">{{ t('registration.consent.contacts') }}</h3>
        <p v-if="!hasContacts" class="consent__text">{{ t('registration.consent.contacts_none') }}</p>
        <p v-if="support.telegram_link" class="consent__text">
          <button type="button" class="consent__link" @click="openSupport">@{{ support.telegram_username }}</button>
        </p>
        <p v-if="support.phone" class="consent__text">
          <a class="consent__link" :href="telHref(support.phone)" target="_blank" rel="noopener noreferrer">{{ formatKzPhone(support.phone) }}</a>
        </p>
        <p v-if="hours" class="consent__text">{{ hours }}</p>
      </section>
      <p class="consent__version">{{ t('registration.consent.version', { version: PD_CONSENT_VERSION }) }}</p>
    </article>
  </section>
  <p v-if="showError" class="consent__error" role="alert">{{ t('registration.consent.required') }}</p>
</template>

<style scoped>
.consent {
  margin: 0 var(--jt-side-margin) var(--jt-space-5);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  overflow: hidden;
}

.consent.is-invalid {
  box-shadow: inset 0 0 0 1px var(--jt-color-red);
}

.consent__check {
  display: flex;
  align-items: flex-start;
  gap: var(--jt-space-3);
  width: 100%;
  padding: var(--jt-space-4) var(--jt-space-4) var(--jt-space-2);
  text-align: left;
  color: var(--jt-color-label);
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
  color: var(--jt-color-on-primary);
  transition: background-color var(--jt-duration-fast) var(--jt-ease);
}

.consent__check.is-on .consent__box {
  border-color: var(--jt-color-primary);
  background: var(--jt-color-primary);
}

.consent__label {
  font: var(--jt-font-body);
}

.consent__more {
  display: inline-flex;
  align-items: center;
  gap: var(--jt-space-1);
  min-height: var(--jt-tap-target);
  padding: 0 var(--jt-space-4) var(--jt-space-2) calc(var(--jt-space-4) + 26px + var(--jt-space-3));
  font: var(--jt-font-subheadline);
  color: var(--jt-color-blue-text);
}

.consent__more:active {
  opacity: var(--jt-opacity-pressed);
}

.consent__chevron {
  transition: transform var(--jt-duration-fast) var(--jt-ease);
}

.consent__chevron.is-open {
  transform: rotate(180deg);
}

.consent__body {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  padding: var(--jt-space-4);
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
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

.consent__error {
  margin: calc(-1 * var(--jt-space-3)) calc(var(--jt-side-margin) + var(--jt-space-4)) var(--jt-space-5);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}
</style>
