<!--
  "Көлік түрі" (ARCHITECTURE §21.5): the driver picks the body type from the
  active classes (iOS radio cards with a monochrome side view, name, examples
  and "4 орын · 400 л"), then the passenger seats with a stepper bounded by
  the class and the cargo space prefilled from the class default (editable).
  Orders reach only vehicles they fit, so these values matter for dispatch.
-->
<script setup lang="ts">
import { Check, Lock } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { isFormFieldLocked, seatsMaxFor, validateVehicleClass } from '../../../features/registration/form'
import { findClass, type VehicleClassOption } from '../../../features/vehicle/classes'
import { useVehicleText } from '../../../features/vehicle/useVehicleText'
import { useFormat } from '../../../composables/useFormat'
import { pickLocalized } from '../../../services/config'
import { useRegistrationStore } from '../../../stores/registration'
import { useSessionStore } from '../../../stores/session'
import { haptics } from '../../../telegram'
import { JButton, JListGroup, JListRow, JSkeleton, JTextField } from '../../ui'
import SeatStepper from '../SeatStepper.vue'
import VehicleClassIcon from '../VehicleClassIcon.vue'
import CorrectionNotes from './CorrectionNotes.vue'

const props = defineProps<{ showErrors: boolean }>()

const { t } = useI18n()
const fmt = useFormat()
const vt = useVehicleText()
const reg = useRegistrationStore()
const session = useSessionStore()
const r = computed(() => reg.registration!)

const classes = computed(() => session.extras.vehicleClasses)
const locked = computed(() => isFormFieldLocked(reg.editable, 'vehicle_class'))
const selected = computed(() => findClass(classes.value, reg.form.vehicle_class))
const seatsMax = computed(() => seatsMaxFor(classes.value, reg.form.vehicle_class))
const errors = computed(() => (props.showErrors ? validateVehicleClass(reg.form, classes.value, r.value.vehicle.vehicle_class) : {}))

/** The stored class when it is no longer offered (kept by the API, shown read-only). */
const storedOnly = computed(() => {
  const code = r.value.vehicle.vehicle_class
  if (!code || findClass(classes.value, code)) return null
  return { name: vt.className(code, r.value.vehicle.vehicle_class_name), capacity: vt.capacity(r.value.vehicle.seats, r.value.vehicle.cargo_volume_l) }
})

function choose(c: VehicleClassOption): void {
  if (locked.value) return
  haptics.selection()
  reg.chooseClass(c)
}

function setSeats(n: number): void {
  if (locked.value) return
  reg.form.seats = n
  reg.touch()
}

function setCargo(v: string): void {
  if (locked.value) return
  reg.form.cargo_volume_l = v.replace(/[^\d\s]/g, '').slice(0, 9)
  reg.touch()
}

const classError = computed(() => {
  const e = errors.value.vehicle_class
  if (!e) return ''
  return e === 'required' ? t('registration.vehicle_class.required') : t('registration.vehicle_class.unavailable')
})

const seatsError = computed(() => (errors.value.seats ? t('registration.vehicle_class.seats_range', { max: seatsMax.value }) : ''))

const cargoError = computed(() => {
  const e = errors.value.cargo_volume_l
  if (!e) return ''
  if (e === 'too_large') return t('registration.vehicle_class.cargo_too_large', { max: vt.litres(100_000) })
  return e === 'required' ? t('registration.form_errors.required') : t('registration.vehicle_class.cargo_invalid')
})

const cargoHint = computed(() => {
  if (locked.value) return t('registration.locked')
  const c = selected.value
  return c ? t('registration.vehicle_class.cargo_hint', { litres: vt.litres(c.cargo_volume_l_default) }) : ''
})
</script>

<template>
  <div class="step">
    <p class="step__text">{{ t('registration.vehicle_class.text') }}</p>
    <CorrectionNotes :items="r.corrections" :fields="['vehicle_class']" />

    <div v-if="classes.length === 0 && session.configLoading" class="vclass__skeleton" aria-busy="true">
      <JSkeleton v-for="i in 3" :key="i" height="84px" radius="22px" />
    </div>

    <div v-else-if="classes.length === 0" class="step__block" role="alert">
      <p class="vclass__empty">{{ t('registration.vehicle_class.load_failed') }}</p>
      <JButton variant="secondary" size="medium" :loading="session.configLoading" @click="session.refreshConfig()">{{ t('common.retry') }}</JButton>
    </div>

    <template v-else>
      <div class="vclass__list" role="radiogroup" :aria-label="t('registration.steps.vehicle_class')">
        <button
          v-for="c in classes"
          :key="c.code"
          type="button"
          role="radio"
          class="vclass"
          :class="{ 'is-selected': reg.form.vehicle_class === c.code, 'is-locked': locked }"
          :aria-checked="reg.form.vehicle_class === c.code"
          :disabled="locked && reg.form.vehicle_class !== c.code"
          @click="choose(c)"
        >
          <span class="vclass__art"><VehicleClassIcon :icon="c.icon" :width="60" /></span>
          <span class="vclass__text">
            <span class="vclass__name">{{ pickLocalized(c.name, fmt.locale.value) }}</span>
            <span v-if="c.examples" class="vclass__examples">{{ c.examples }}</span>
            <span class="vclass__cap jt-tabular">{{ vt.capacity(c.seats_max, c.cargo_volume_l_default) }}</span>
          </span>
          <span class="vclass__mark" aria-hidden="true">
            <Lock v-if="locked && reg.form.vehicle_class === c.code" :size="16" :stroke-width="2.25" />
            <Check v-else-if="reg.form.vehicle_class === c.code" :size="16" :stroke-width="3" />
          </span>
        </button>
      </div>
      <p v-if="storedOnly" class="step__note vclass__stored">
        {{ t('registration.vehicle_class.stored_inactive', { name: storedOnly.name }) }}
      </p>
      <p v-if="classError" class="vclass__error" role="alert">{{ classError }}</p>
    </template>

    <template v-if="reg.form.vehicle_class">
      <JListGroup :header="t('registration.vehicle_class.capacity')" :footer="seatsError || t('registration.vehicle_class.seats_footer', { max: seatsMax })">
        <JListRow :title="t('registration.vehicle_class.seats')">
          <template #accessory>
            <span class="vclass__seats">
              <span class="vclass__seats-value jt-tabular" aria-live="polite">{{ reg.form.seats ?? '—' }}</span>
              <SeatStepper
                :model-value="reg.form.seats ?? 1"
                :min="1"
                :max="seatsMax"
                :disabled="locked"
                :label="t('registration.vehicle_class.seats')"
                @update:model-value="setSeats"
              />
            </span>
          </template>
        </JListRow>
      </JListGroup>

      <JListGroup :header="t('registration.vehicle_class.cargo')">
        <div class="step__fields">
          <JTextField
            :model-value="reg.form.cargo_volume_l"
            :placeholder="selected ? String(selected.cargo_volume_l_default) : ''"
            inputmode="numeric"
            enterkeyhint="done"
            :readonly="locked"
            :hint="cargoError ? '' : cargoHint"
            :error="cargoError"
            @update:model-value="setCargo"
          />
        </div>
      </JListGroup>
    </template>
  </div>
</template>

<style scoped src="./step.css"></style>
<style scoped>
.vclass__skeleton {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
}

.vclass__empty {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.vclass__list {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
}

.vclass {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  width: 100%;
  min-height: 84px;
  padding: var(--jt-space-3) var(--jt-space-4) var(--jt-space-3) var(--jt-space-3);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 1px transparent;
  text-align: left;
  color: var(--jt-color-label);
  transition:
    box-shadow var(--jt-duration-fast) var(--jt-ease),
    transform var(--jt-duration-spring) var(--jt-ease-spring);
  touch-action: manipulation;
  user-select: none;
}

.vclass:active:not(:disabled) {
  transform: scale(0.98);
}

.vclass.is-selected {
  box-shadow: inset 0 0 0 2px var(--jt-color-label);
}

.vclass:disabled {
  opacity: var(--jt-opacity-disabled);
}

.vclass__art {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 76px;
  height: 56px;
  border-radius: 16px;
  background: var(--jt-color-fill-secondary);
}

.vclass__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.vclass__name {
  font: var(--jt-font-headline);
}

.vclass__examples {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.vclass__cap {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.vclass__mark {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  box-shadow: inset 0 0 0 1.5px var(--jt-color-label-tertiary);
  color: var(--jt-color-on-primary);
}

.vclass.is-selected .vclass__mark {
  background: var(--jt-color-primary);
  box-shadow: none;
}

.vclass.is-locked.is-selected .vclass__mark {
  background: transparent;
  color: var(--jt-color-label-secondary);
}

.vclass__stored {
  margin-top: calc(-1 * var(--jt-space-4));
}

.vclass__error {
  margin: calc(-1 * var(--jt-space-4)) calc(var(--jt-side-margin) + var(--jt-space-4)) var(--jt-space-6);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}

.vclass__seats {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
}

.vclass__seats-value {
  min-width: 24px;
  font: var(--jt-font-headline);
  text-align: right;
}
</style>
