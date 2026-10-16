<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { extraPhotos, MAX_EXTRA_PHOTOS } from '../../../features/registration/form'
import { useRegistrationStore } from '../../../stores/registration'
import CorrectionNotes from './CorrectionNotes.vue'
import ExtraPhotos from './ExtraPhotos.vue'
import PhotoField from './PhotoField.vue'

const props = defineProps<{ showErrors: boolean }>()

const { t } = useI18n()
const reg = useRegistrationStore()
const r = computed(() => reg.registration!)
const locked = computed(() => !reg.editable.has('vehicle_photos'))
const main = computed(() => r.value.vehicle.photos.find((p) => p.kind === 'main') ?? null)
const extras = computed(() => extraPhotos(r.value))
</script>

<template>
  <div class="step">
    <p class="step__text">{{ t('registration.photos.text', { max: MAX_EXTRA_PHOTOS }) }}</p>
    <CorrectionNotes :items="r.corrections" :fields="['vehicle_photos']" />

    <div class="step__block">
      <PhotoField kind="vehicle_main" :file="main" :label="t('registration.photos.main')" :hint="t('registration.photos.main_hint')" :locked="locked" />
      <p v-if="props.showErrors && !main" class="step__error" role="alert">{{ t('registration.form_errors.photo_required') }}</p>
    </div>

    <div class="step__block">
      <p class="extra-title">
        {{ t('registration.photos.extra') }}
        <span class="extra-count jt-tabular">{{ extras.length }}/{{ MAX_EXTRA_PHOTOS }}</span>
      </p>
      <ExtraPhotos :photos="extras" :locked="locked" />
      <p class="extra-hint">{{ locked ? t('registration.locked') : t('registration.photos.extra_hint') }}</p>
    </div>
  </div>
</template>

<style scoped src="./step.css"></style>
<style scoped>
.extra-title {
  display: flex;
  justify-content: space-between;
  font: var(--jt-font-headline);
}

.extra-count {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.extra-hint {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}
</style>
