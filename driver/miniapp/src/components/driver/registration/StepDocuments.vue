<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRegistrationStore } from '../../../stores/registration'
import CorrectionNotes from './CorrectionNotes.vue'
import PhotoField from './PhotoField.vue'

const props = defineProps<{ showErrors: boolean }>()

const { t } = useI18n()
const reg = useRegistrationStore()
const r = computed(() => reg.registration!)
</script>

<template>
  <div class="step">
    <p class="step__text">{{ t('registration.documents.text') }}</p>
    <CorrectionNotes :items="r.corrections" :fields="['id_front', 'id_back']" />

    <div class="step__block">
      <PhotoField kind="id_front" :file="r.documents.id_front" :label="t('registration.documents.front')" :locked="!reg.editable.has('id_front')" />
      <p v-if="props.showErrors && !r.documents.id_front" class="step__error" role="alert">{{ t('registration.form_errors.photo_required') }}</p>
    </div>
    <div class="step__block">
      <PhotoField kind="id_back" :file="r.documents.id_back" :label="t('registration.documents.back')" :locked="!reg.editable.has('id_back')" />
      <p v-if="props.showErrors && !r.documents.id_back" class="step__error" role="alert">{{ t('registration.form_errors.photo_required') }}</p>
    </div>
    <p class="step__note">{{ t('registration.documents.privacy') }}</p>
  </div>
</template>

<style scoped src="./step.css"></style>
