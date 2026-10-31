<!--
  The body type on the vehicle page (ARCHITECTURE §21.5): one row with the
  chosen class's side view, name and capacity — Sedan for a new registration.
  Tapping it lists the active classes right under it; picking one closes the
  list. Seats and cargo space take the class defaults (the API applies them),
  so the car gets the orders it fits.
-->
<script setup lang="ts">
import { Check, ChevronDown, Lock } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { isFormFieldLocked, validateVehicleClass } from '../../../features/registration/form'
import { findClass, VEHICLE_ICONS, type VehicleClassOption, type VehicleIcon } from '../../../features/vehicle/classes'
import { useVehicleText } from '../../../features/vehicle/useVehicleText'
import { useFormat } from '../../../composables/useFormat'
import { pickLocalized } from '../../../services/config'
import { useRegistrationStore } from '../../../stores/registration'
import { useSessionStore } from '../../../stores/session'
import { haptics } from '../../../telegram'
import { JButton, JListGroup, JSkeleton } from '../../ui'
import VehicleClassIcon from '../VehicleClassIcon.vue'

const props = defineProps<{ showErrors: boolean }>()

const { t } = useI18n()
const fmt = useFormat()
const vt = useVehicleText()
const reg = useRegistrationStore()
const session = useSessionStore()

const open = ref(false)
const r = computed(() => reg.registration!)
const classes = computed(() => session.extras.vehicleClasses)
const locked = computed(() => isFormFieldLocked(reg.editable, 'vehicle_class'))
const code = computed(() => reg.form.vehicle_class)
const selected = computed(() => findClass(classes.value, code.value))
/** The chosen class is the one on file: its stored seats and cargo space apply. */
const stored = computed(() => code.value !== '' && code.value === r.value.vehicle.vehicle_class)

const name = computed(() => (code.value ? vt.className(code.value, stored.value ? r.value.vehicle.vehicle_class_name : null) : ''))
const capacity = computed(() => {
  if (stored.value) return vt.capacity(r.value.vehicle.seats, r.value.vehicle.cargo_volume_l)
  const c = selected.value
  return c ? vt.capacity(c.seats_default, c.cargo_volume_l_default) : ''
})
const icon = computed<VehicleIcon>(() => {
  if (selected.value) return selected.value.icon
  return (VEHICLE_ICONS as readonly string[]).includes(code.value) ? (code.value as VehicleIcon) : 'sedan'
})
/** The stored class is no longer offered: the API keeps it, the list offers the active ones. */
const storedInactive = computed(() => stored.value && classes.value.length > 0 && !selected.value)

const error = computed(() => {
  const e = props.showErrors ? validateVehicleClass(reg.form, classes.value, r.value.vehicle.vehicle_class).vehicle_class : undefined
  if (!e) return ''
  return e === 'required' ? t('registration.vehicle_class.required') : t('registration.vehicle_class.unavailable')
})

const footer = computed(() => {
  if (error.value) return ''
  if (locked.value) return t('registration.locked')
  if (storedInactive.value) return t('registration.vehicle_class.stored_inactive', { name: name.value })
  return t('registration.vehicle.class_footer')
})

function toggle(): void {
  if (locked.value) return
  haptics.selection()
  open.value = !open.value
}

function choose(c: VehicleClassOption): void {
  haptics.selection()
  reg.chooseClass(c)
  open.value = false
}
</script>

<template>
  <JListGroup :header="t('registration.vehicle.class')" :footer="footer">
    <button
      type="button"
      class="vrow"
      :class="{ 'is-locked': locked }"
      :aria-expanded="locked ? undefined : open"
      :disabled="locked"
      @click="toggle"
    >
      <span class="vrow__art"><VehicleClassIcon :icon="icon" :width="52" /></span>
      <span class="vrow__text">
        <span class="vrow__name">{{ name || t('registration.vehicle.class_choose') }}</span>
        <span v-if="capacity" class="vrow__cap jt-tabular">{{ capacity }}</span>
      </span>
      <Lock v-if="locked" :size="16" :stroke-width="2" class="vrow__lock" :aria-label="t('registration.locked')" />
      <ChevronDown v-else :size="20" :stroke-width="2.25" class="vrow__chevron" :class="{ 'is-open': open }" aria-hidden="true" />
    </button>

    <template v-if="open">
      <div v-if="classes.length === 0 && session.configLoading" class="vrow__loading" aria-busy="true">
        <JSkeleton v-for="i in 3" :key="i" height="48px" radius="12px" />
      </div>
      <div v-else-if="classes.length === 0" class="vrow__empty" role="alert">
        <p>{{ t('registration.vehicle_class.load_failed') }}</p>
        <JButton variant="secondary" size="medium" :loading="session.configLoading" @click="session.refreshConfig()">{{ t('common.retry') }}</JButton>
      </div>
      <div v-else class="vrow__list" role="radiogroup" :aria-label="t('registration.vehicle.class')">
        <button
          v-for="c in classes"
          :key="c.code"
          type="button"
          role="radio"
          class="vrow vrow--option"
          :aria-checked="code === c.code"
          @click="choose(c)"
        >
          <span class="vrow__art is-small"><VehicleClassIcon :icon="c.icon" :width="40" /></span>
          <span class="vrow__text">
            <span class="vrow__name">{{ pickLocalized(c.name, fmt.locale.value) }}</span>
            <span v-if="c.examples" class="vrow__examples">{{ c.examples }}</span>
            <span class="vrow__cap jt-tabular">{{ vt.capacity(c.seats_max, c.cargo_volume_l_default) }}</span>
          </span>
          <Check v-if="code === c.code" :size="20" :stroke-width="2.5" class="vrow__check" aria-hidden="true" />
        </button>
      </div>
    </template>
  </JListGroup>
  <p v-if="error" class="vrow__error" role="alert">{{ error }}</p>
</template>

<style scoped>
.vrow {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  width: 100%;
  min-height: 72px;
  padding: var(--jt-space-2) var(--jt-space-4) var(--jt-space-2) var(--jt-space-3);
  background: var(--jt-color-bg);
  color: var(--jt-color-label);
  text-align: left;
  touch-action: manipulation;
  user-select: none;
}

.vrow:active:not(:disabled) {
  background: var(--jt-color-fill-tertiary);
}

.vrow.is-locked:disabled {
  opacity: 1;
}

.vrow--option {
  min-height: 60px;
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.vrow__art {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 68px;
  height: 52px;
  border-radius: 14px;
  background: var(--jt-color-fill-secondary);
}

.vrow__art.is-small {
  width: 52px;
  height: 40px;
  border-radius: 10px;
}

.vrow__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.vrow__name {
  font: var(--jt-font-headline);
}

.vrow__examples {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.vrow__cap {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.vrow__chevron {
  flex: none;
  color: var(--jt-color-label-tertiary);
  transition: transform var(--jt-duration-fast) var(--jt-ease);
}

.vrow__chevron.is-open {
  transform: rotate(180deg);
}

.vrow__lock {
  flex: none;
  color: var(--jt-color-label-secondary);
}

.vrow__check {
  flex: none;
  color: var(--jt-color-label);
}

.vrow__loading {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  padding: var(--jt-space-3) var(--jt-space-4);
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.vrow__empty {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--jt-space-3);
  padding: var(--jt-space-4);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.vrow__error {
  margin: calc(-1 * var(--jt-space-4)) calc(var(--jt-side-margin) + var(--jt-space-4)) var(--jt-space-6);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}
</style>
