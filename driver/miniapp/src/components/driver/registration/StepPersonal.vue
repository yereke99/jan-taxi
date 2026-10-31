<!--
  Registration page 1, personal details (§11): the profile photo, first name,
  surname, the optional patronymic under it, the IIN (12 digits, checked as
  it is typed) and the phone number confirmed through Telegram.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { isFormFieldLocked, LIMITS, validatePersonal } from '../../../features/registration/form'
import { useRegistrationStore } from '../../../stores/registration'
import { JListGroup, JTextField } from '../../ui'
import CorrectionNotes from './CorrectionNotes.vue'
import IinField from './IinField.vue'
import PhoneSection from './PhoneSection.vue'
import PhotoField from './PhotoField.vue'

const props = defineProps<{ showErrors: boolean }>()

const { t } = useI18n()
const reg = useRegistrationStore()

const r = computed(() => reg.registration!)
const errors = computed(() => (props.showErrors ? validatePersonal(reg.form) : {}))
const firstLocked = computed(() => isFormFieldLocked(reg.editable, 'first_name'))
const lastLocked = computed(() => isFormFieldLocked(reg.editable, 'last_name'))
const identityLocked = computed(() => isFormFieldLocked(reg.editable, 'iin'))
const avatarMissing = computed(() => props.showErrors && !r.value.avatar)
const iinTaken = computed(() => (reg.taken.iin !== '' && reg.taken.iin === reg.form.iin ? t('registration.personal.iin_errors.taken') : ''))

function err(key: 'first_name' | 'last_name' | 'patronymic'): string {
  const e = errors.value[key]
  return e ? t(`registration.form_errors.${e}`) : ''
}
</script>

<template>
  <div class="step">
    <p class="step__text">{{ t('registration.personal.text') }}</p>
    <CorrectionNotes :items="r.corrections" :fields="['avatar', 'first_name', 'last_name', 'identity', 'phone', 'id_front', 'id_back']" />

    <div class="step__block">
      <PhotoField
        kind="avatar"
        :file="r.avatar"
        :label="t('registration.personal.avatar')"
        :hint="t('registration.personal.avatar_hint')"
        :locked="!reg.editable.has('avatar')"
        facing="user"
        round
      />
      <p v-if="avatarMissing" class="step__error" role="alert">{{ t('registration.form_errors.photo_required') }}</p>
    </div>

    <JListGroup>
      <div class="step__fields">
        <JTextField
          v-model="reg.form.first_name"
          :label="t('registration.personal.first_name')"
          autocomplete="given-name"
          enterkeyhint="next"
          :max="LIMITS.name"
          :readonly="firstLocked"
          :hint="firstLocked ? t('registration.locked') : ''"
          :error="err('first_name')"
          @update:model-value="reg.touch()"
        />
        <JTextField
          v-model="reg.form.last_name"
          :label="t('registration.personal.last_name')"
          autocomplete="family-name"
          enterkeyhint="next"
          :max="LIMITS.name"
          :readonly="lastLocked"
          :hint="lastLocked ? t('registration.locked') : ''"
          :error="err('last_name')"
          @update:model-value="reg.touch()"
        />
        <JTextField
          v-model="reg.form.patronymic"
          :label="t('registration.personal.patronymic')"
          autocomplete="additional-name"
          enterkeyhint="next"
          :max="LIMITS.patronymic"
          :readonly="identityLocked"
          :hint="identityLocked ? t('registration.locked') : t('registration.personal.optional')"
          :error="err('patronymic')"
          @update:model-value="reg.touch()"
        />
        <IinField
          v-model="reg.form.iin"
          :label="t('registration.personal.iin')"
          :show-errors="showErrors"
          :server-error="iinTaken"
          :readonly="identityLocked"
          :hint="identityLocked ? t('registration.locked') : t('registration.personal.iin_hint')"
          @update:model-value="reg.touch()"
        />
      </div>
    </JListGroup>

    <PhoneSection :show-errors="showErrors" />
  </div>
</template>

<style scoped src="./step.css"></style>
