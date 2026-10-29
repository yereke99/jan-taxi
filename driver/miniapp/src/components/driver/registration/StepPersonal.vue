<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { isFormFieldLocked, LIMITS, validatePersonal } from '../../../features/registration/form'
import { useRegistrationStore } from '../../../stores/registration'
import { JListGroup, JTextField, JSegmented } from '../../ui'
import CorrectionNotes from './CorrectionNotes.vue'
import PhotoField from './PhotoField.vue'

const props = defineProps<{ showErrors: boolean }>()

const { t } = useI18n()
const reg = useRegistrationStore()

const r = computed(() => reg.registration!)
const errors = computed(() => (props.showErrors ? validatePersonal(reg.form) : {}))
const firstLocked = computed(() => isFormFieldLocked(reg.editable, 'first_name'))
const lastLocked = computed(() => isFormFieldLocked(reg.editable, 'last_name'))
const avatarMissing = computed(() => props.showErrors && !r.value.avatar)

function err(key: 'first_name' | 'last_name'): string {
  const e = errors.value[key]
  return e ? t(`registration.form_errors.${e}`) : ''
}
</script>

<template>
  <div class="step">
    <p class="step__text">{{ t('registration.identity.hint') }}</p>
    <p class="step__note">{{ t('registration.identity.match_hint') }}</p>
    <CorrectionNotes :items="r.corrections" :fields="['avatar', 'first_name', 'last_name','identity']" />

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
          enterkeyhint="done"
          :max="LIMITS.name"
          :readonly="lastLocked"
          :hint="lastLocked ? t('registration.locked') : ''"
          :error="err('last_name')"
          @update:model-value="reg.touch()"
        />
        <template v-if="r.identity_enabled && reg.form.identity">
          <JSegmented v-model="reg.form.identity.document_type" :aria-label="t('registration.identity.document_type')" :options="[{value:'id_card',label:t('registration.identity.id_card')},{value:'driving_licence',label:t('registration.identity.driving_licence')}]" :disabled="!reg.editable.has('identity')" @change="reg.touch()" />
          <JTextField v-model="reg.form.identity.iin" :label="t('registration.identity.iin')" inputmode="numeric" :max="12" :readonly="!reg.editable.has('identity')" autocomplete="off" @update:model-value="reg.touch()" />
          <JTextField v-model="reg.form.identity.document_number" :label="t('registration.identity.document_number')" :max="20" :readonly="!reg.editable.has('identity')" autocomplete="off" @update:model-value="reg.touch()" />
          <JTextField v-model="reg.form.identity.patronymic" :label="t('registration.identity.patronymic')" :max="60" :readonly="!reg.editable.has('identity')" autocomplete="additional-name" @update:model-value="reg.touch()" />
        </template>
      </div>
    </JListGroup>
  </div>
</template>

<style scoped src="./step.css"></style>
