<script setup lang="ts">
import { Check, CircleCheck, CircleX, Lock } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { matchColor, VEHICLE_COLORS, type VehicleColor } from '../../../features/registration/colors'
import { formatPlate, isFormFieldLocked, isValidPlate, LIMITS, normalizePlateInput, validateVehicle } from '../../../features/registration/form'
import { useFormat } from '../../../composables/useFormat'
import { useRegistrationStore } from '../../../stores/registration'
import { haptics } from '../../../telegram'
import { JListGroup, JListRow, JSpinner, JTextField } from '../../ui'
import CorrectionNotes from './CorrectionNotes.vue'

const props = defineProps<{ showErrors: boolean }>()

const { t } = useI18n()
const { locale } = useFormat()
const reg = useRegistrationStore()
const r = computed(() => reg.registration!)

const makeLocked = computed(() => isFormFieldLocked(reg.editable, 'make'))
const colorLocked = computed(() => isFormFieldLocked(reg.editable, 'color'))
const plateLocked = computed(() => isFormFieldLocked(reg.editable, 'plate'))
const errors = computed(() => (props.showErrors ? validateVehicle(reg.form) : {}))

const matched = computed(() => matchColor(reg.form.color))
const customMode = ref(reg.form.color.trim() !== '' && matched.value === null)

function err(key: 'make' | 'model' | 'color'): string {
  const e = errors.value[key]
  return e ? t(`registration.form_errors.${e}`) : ''
}

function chooseColor(c: VehicleColor): void {
  if (colorLocked.value) return
  haptics.selection()
  customMode.value = false
  reg.form.color = c.name[locale.value]
  reg.touch()
}

function chooseCustom(): void {
  if (colorLocked.value) return
  haptics.selection()
  if (!customMode.value && matched.value) reg.form.color = ''
  customMode.value = true
}

function onPlate(v: string): void {
  reg.form.plate = v.toUpperCase()
  reg.touch()
}

type PlateState = { tone: 'neutral' | 'ok' | 'bad' | 'busy'; text: string }

const plateState = computed<PlateState>(() => {
  const v = reg.form.plate.trim()
  if (plateLocked.value) return { tone: 'neutral', text: t('registration.locked') }
  if (!v) return props.showErrors ? { tone: 'bad', text: t('registration.form_errors.required') } : { tone: 'neutral', text: t('registration.vehicle.plate_hint') }
  if (!isValidPlate(v)) {
    const typing = normalizePlateInput(v).length < 7 && !props.showErrors
    return typing ? { tone: 'neutral', text: t('registration.vehicle.plate_hint') } : { tone: 'bad', text: t('registration.vehicle.plate_invalid') }
  }
  const res = reg.plate.result
  if (reg.plate.checking || !res || reg.plate.forInput !== normalizePlateInput(v)) {
    return reg.plate.checking ? { tone: 'busy', text: t('registration.vehicle.plate_checking') } : { tone: 'neutral', text: formatPlate(v) ?? '' }
  }
  if (res.available === false) return { tone: 'bad', text: t('registration.vehicle.plate_taken') }
  return { tone: 'ok', text: t('registration.vehicle.plate_free', { plate: res.display ?? formatPlate(v) ?? v }) }
})
</script>

<template>
  <div class="step">
    <p class="step__text">{{ t('registration.vehicle.text') }}</p>
    <CorrectionNotes :items="r.corrections" :fields="['vehicle_make_model', 'vehicle_color', 'vehicle_plate']" />

    <JListGroup>
      <div class="step__fields">
        <JTextField
          v-model="reg.form.make"
          :label="t('registration.vehicle.make')"
          :placeholder="t('registration.vehicle.make_placeholder')"
          :max="LIMITS.make"
          enterkeyhint="next"
          :readonly="makeLocked"
          :hint="makeLocked ? t('registration.locked') : ''"
          :error="err('make')"
          @update:model-value="reg.touch()"
        />
        <JTextField
          v-model="reg.form.model"
          :label="t('registration.vehicle.model')"
          :placeholder="t('registration.vehicle.model_placeholder')"
          :max="LIMITS.model"
          enterkeyhint="next"
          :readonly="makeLocked"
          :hint="makeLocked ? t('registration.locked') : ''"
          :error="err('model')"
          @update:model-value="reg.touch()"
        />
      </div>
    </JListGroup>

    <JListGroup :header="t('registration.vehicle.color')" :footer="colorLocked ? t('registration.locked') : err('color')">
      <template v-if="colorLocked">
        <JListRow :title="reg.form.color || '—'">
          <template #accessory><Lock :size="16" :stroke-width="2" class="veh__lock" aria-hidden="true" /></template>
        </JListRow>
      </template>
      <template v-else>
        <JListRow v-for="c in VEHICLE_COLORS" :key="c.id" clickable :title="c.name[locale]" @click="chooseColor(c)">
          <template #icon><span class="veh__swatch" :style="{ background: c.swatch }" aria-hidden="true" /></template>
          <template #accessory>
            <Check v-if="!customMode && matched?.id === c.id" :size="20" :stroke-width="2.5" class="veh__check" :aria-label="t('common.select')" />
          </template>
        </JListRow>
        <JListRow clickable :title="t('registration.vehicle.color_custom')" @click="chooseCustom">
          <template #icon><span class="veh__swatch is-custom" aria-hidden="true" /></template>
          <template #accessory>
            <Check v-if="customMode" :size="20" :stroke-width="2.5" class="veh__check" :aria-label="t('common.select')" />
          </template>
        </JListRow>
      </template>
    </JListGroup>
    <div v-if="customMode && !colorLocked" class="veh__custom">
      <JTextField
        v-model="reg.form.color"
        :placeholder="t('registration.vehicle.color_custom_placeholder')"
        :max="LIMITS.color"
        :error="err('color')"
        @update:model-value="reg.touch()"
      />
    </div>

    <JListGroup :header="t('registration.vehicle.plate')">
      <div class="step__fields">
        <JTextField
          :model-value="reg.form.plate"
          placeholder="123 ABC 04"
          autocomplete="off"
          enterkeyhint="done"
          :max="LIMITS.plate"
          :readonly="plateLocked"
          @update:model-value="onPlate"
        />
        <p class="veh__plate" :class="`is-${plateState.tone}`" :role="plateState.tone === 'bad' ? 'alert' : 'status'" aria-live="polite">
          <JSpinner v-if="plateState.tone === 'busy'" size="small" />
          <CircleCheck v-else-if="plateState.tone === 'ok'" :size="16" :stroke-width="2.25" aria-hidden="true" />
          <CircleX v-else-if="plateState.tone === 'bad'" :size="16" :stroke-width="2.25" aria-hidden="true" />
          <span>{{ plateState.text }}</span>
        </p>
      </div>
    </JListGroup>
  </div>
</template>

<style scoped src="./step.css"></style>
<style scoped>
.veh__swatch {
  width: 22px;
  height: 22px;
  border-radius: 50%;
  box-shadow: inset 0 0 0 1px rgba(0, 0, 0, 0.18);
}

.veh__swatch.is-custom {
  box-shadow: none;
  border: 1.5px dashed var(--jt-color-label-tertiary);
}

.veh__check {
  color: var(--jt-color-label);
}

.veh__lock {
  color: var(--jt-color-label-secondary);
}

.veh__custom {
  margin: calc(-1 * var(--jt-space-3)) var(--jt-side-margin) var(--jt-space-6);
}

.veh__plate {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  min-height: 20px;
  margin-top: calc(-1 * var(--jt-space-1));
  padding: 0 var(--jt-space-1);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.veh__plate.is-ok {
  color: var(--jt-color-green-text);
}

.veh__plate.is-bad {
  color: var(--jt-color-red-text);
}
</style>
