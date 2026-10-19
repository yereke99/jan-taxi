<!--
  Review before submission: every step with its state, what the server still
  misses (missing_fields mapped back to steps) and a jump to fix each one.
-->
<script setup lang="ts">
import { CircleAlert, CircleCheck } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RegistrationField } from '../../../api/types'
import { formatPlate, isCorrectionMode, isStepComplete, parseLitres, type StepId } from '../../../features/registration/form'
import { useVehicleText } from '../../../features/vehicle/useVehicleText'
import { useRegistrationStore } from '../../../stores/registration'
import { useSessionStore } from '../../../stores/session'
import { formatKzPhone } from '../../../utils/phone'
import { JListGroup, JListRow } from '../../ui'
import CorrectionNotes from './CorrectionNotes.vue'

const props = defineProps<{ steps: readonly StepId[]; problemFields: readonly RegistrationField[] }>()
const emit = defineEmits<{ edit: [step: StepId] }>()

const { t } = useI18n()
const reg = useRegistrationStore()
const session = useSessionStore()
const vt = useVehicleText()
const r = computed(() => reg.registration!)
const correction = computed(() => isCorrectionMode(r.value))
const ctx = computed(() => ({
  registration: r.value,
  form: reg.form,
  consentGiven: reg.consentGiven || reg.consentRecorded,
  classes: session.extras.vehicleClasses,
}))

const missing = computed(() => {
  const set = new Set<RegistrationField>([...r.value.missing_fields, ...props.problemFields])
  return [...set]
})

function summary(step: StepId): string {
  const f = reg.form
  switch (step) {
    case 'personal':
      return [f.first_name, f.last_name].filter(Boolean).join(' ')
    case 'phone':
      return r.value.phone_verified && r.value.phone ? formatKzPhone(r.value.phone) : t('registration.review.phone_none')
    case 'documents':
      return [r.value.documents.id_front ? t('registration.documents.front') : '', r.value.documents.id_back ? t('registration.documents.back') : '']
        .filter(Boolean)
        .join(', ')
    case 'vehicle_class': {
      if (!f.vehicle_class) return ''
      const name = vt.className(f.vehicle_class, f.vehicle_class === r.value.vehicle.vehicle_class ? r.value.vehicle.vehicle_class_name : null)
      return [name, vt.capacity(f.seats, parseLitres(f.cargo_volume_l))].filter(Boolean).join(' · ')
    }
    case 'vehicle':
      return [[f.make, f.model].filter(Boolean).join(' '), f.color, formatPlate(f.plate) ?? f.plate].filter(Boolean).join(' · ')
    case 'photos':
      return t('registration.review.photos_count', { n: r.value.vehicle.photos.length })
    case 'consent':
      return reg.consentGiven || reg.consentRecorded ? t('registration.review.consent_given') : ''
    case 'review':
      return ''
  }
}

const rows = computed(() =>
  props.steps
    .filter((s) => s !== 'review')
    .map((s) => ({ step: s, complete: isStepComplete(s, ctx.value), text: summary(s) })),
)
</script>

<template>
  <div class="step">
    <p class="step__text">{{ correction ? t('registration.review.resubmit_text') : t('registration.review.text') }}</p>

    <template v-if="correction">
      <div v-if="r.correction_comment" class="review__comment">
        <p class="review__comment-title">{{ t('registration.corrections.comment') }}</p>
        <p class="review__comment-text">«{{ r.correction_comment }}»</p>
      </div>
      <CorrectionNotes :items="r.corrections" />
    </template>

    <JListGroup>
      <JListRow
        v-for="row in rows"
        :key="row.step"
        clickable
        chevron
        :title="t(`registration.steps.${row.step}`)"
        :subtitle="row.text || (row.complete ? '' : t('registration.review.incomplete'))"
        @click="emit('edit', row.step)"
      >
        <template #icon>
          <CircleCheck v-if="row.complete" :size="22" :stroke-width="2" class="review__ok" :aria-label="t('registration.review.complete')" />
          <CircleAlert v-else :size="22" :stroke-width="2" class="review__bad" :aria-label="t('registration.review.incomplete')" />
        </template>
      </JListRow>
    </JListGroup>

    <p v-if="missing.length > 0" class="review__missing" role="alert">
      {{ t('registration.review.missing', { fields: missing.map((f) => t(`registration.field.${f}`)).join(', ') }) }}
    </p>
  </div>
</template>

<style scoped src="./step.css"></style>
<style scoped>
.review__ok {
  color: var(--jt-color-green);
}

.review__bad {
  color: var(--jt-color-red);
}

.review__comment {
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.review__comment-title {
  font: var(--jt-font-headline);
}

.review__comment-text {
  margin-top: var(--jt-space-1);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.review__missing {
  margin: calc(-1 * var(--jt-space-3)) calc(var(--jt-side-margin) + var(--jt-space-4)) var(--jt-space-6);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}
</style>
