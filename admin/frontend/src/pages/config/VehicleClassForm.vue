<!--
  Create or edit a vehicle class (§21): names, examples, icon, seats, cargo
  space, what it carries and which classes' orders its drivers may take.
  The code is fixed once created; the same rules as the API are checked
  before saving.
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { VEHICLE_CLASS_ICONS, vehicleClassesApi, type VehicleClass } from '../../api/vehicleClasses'
import { AppButton, Drawer, FormField, TextInput, ToggleSwitch, VehicleClassIcon, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { validateVehicleClassForm, type VehicleClassForm } from '../../forms/validation'

const props = defineProps<{ open: boolean; vehicleClass: VehicleClass | null; all: VehicleClass[] }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; saved: [vehicleClass: VehicleClass] }>()

const { t } = useI18n()
const fmt = useFormat()
const errors = useErrors()

function initial(c: VehicleClass | null): VehicleClassForm {
  return {
    code: c?.code ?? '',
    nameKk: c?.name.kk ?? '',
    nameRu: c?.name.ru ?? '',
    nameEn: c?.name.en ?? '',
    examples: c?.examples ?? '',
    seats_default: String(c?.seats_default ?? 4),
    seats_max: String(c?.seats_max ?? 4),
    cargo_volume_l_default: String(c?.cargo_volume_l_default ?? 400),
    passenger_capable: c?.passenger_capable ?? true,
    cargo_capable: c?.cargo_capable ?? true,
    icon: c?.icon ?? 'sedan',
    serves: (c?.serves ?? []).filter((s) => s !== c?.code),
    sort: String(c?.sort ?? 100),
    is_active: c?.is_active ?? true,
  }
}

const form = reactive<VehicleClassForm>(initial(null))
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)
const creating = computed(() => props.vehicleClass === null)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    Object.assign(form, initial(props.vehicleClass))
    fieldErrors.value = {}
  },
)

/** Classes this one may serve (every other class); the class itself is always served. */
const others = computed(() => props.all.filter((c) => c.code !== props.vehicleClass?.code))

function toggleServe(code: string, on: boolean): void {
  form.serves = on ? [...new Set([...form.serves, code])] : form.serves.filter((s) => s !== code)
}

/** API field paths such as "serves.2" are shown under their list. */
function collapse(f: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(f)) out[k.startsWith('serves.') ? 'serves' : k] = v
  return out
}

async function save(): Promise<void> {
  const v = validateVehicleClassForm(form, creating.value)
  fieldErrors.value = errors.translate(v.errors)
  if (!v.payload || saving.value) return
  saving.value = true
  try {
    const saved = creating.value ? await vehicleClassesApi.create(v.payload) : await vehicleClassesApi.update(props.vehicleClass!.code, v.payload)
    toast.success(t('classes.saved', { name: fmt.name(saved.name) }))
    emit('saved', saved)
    emit('update:open', false)
  } catch (err) {
    fieldErrors.value = collapse(errors.fields(err))
    if (Object.keys(fieldErrors.value).length === 0) errors.report(err)
  } finally {
    saving.value = false
  }
}

const numberFields = [
  ['seats_default', 'classes.seats_default', 'classes.seats_default_hint'],
  ['seats_max', 'classes.seats_max', 'classes.seats_max_hint'],
  ['cargo_volume_l_default', 'classes.cargo_default', 'classes.cargo_default_hint'],
  ['sort', 'classes.sort', 'classes.sort_hint'],
] as const
</script>

<template>
  <Drawer :open="open" :title="creating ? t('classes.new_title') : t('classes.edit_title', { name: fmt.name(vehicleClass?.name) })" width="620px" @update:open="(v) => emit('update:open', v)">
    <form class="jt-form" novalidate @submit.prevent="save">
      <FormField v-slot="f" :label="t('classes.code')" :error="fieldErrors.code" :hint="creating ? t('classes.code_hint') : t('classes.code_fixed')" :required="creating">
        <TextInput :id="f.id" v-model="form.code" :disabled="!creating" :invalid="f.invalid" autocomplete="off" spellcheck="false" />
      </FormField>

      <div class="grid grid--3">
        <FormField v-slot="f" :label="t('config.name_kk')" :error="fieldErrors['name.kk']" required>
          <TextInput :id="f.id" v-model="form.nameKk" :invalid="f.invalid" />
        </FormField>
        <FormField v-slot="f" :label="t('config.name_ru')" :error="fieldErrors['name.ru']" required>
          <TextInput :id="f.id" v-model="form.nameRu" :invalid="f.invalid" />
        </FormField>
        <FormField v-slot="f" :label="t('config.name_en')" :error="fieldErrors['name.en']" required>
          <TextInput :id="f.id" v-model="form.nameEn" :invalid="f.invalid" />
        </FormField>
      </div>

      <FormField v-slot="f" :label="t('classes.examples')" :error="fieldErrors.examples" :hint="t('classes.examples_hint')">
        <TextInput :id="f.id" v-model="form.examples" :invalid="f.invalid" />
      </FormField>

      <fieldset class="block">
        <legend class="block__title">{{ t('classes.icon') }}</legend>
        <div class="icons" role="radiogroup" :aria-label="t('classes.icon')">
          <label v-for="icon in VEHICLE_CLASS_ICONS" :key="icon" class="icons__item" :class="{ 'is-selected': form.icon === icon }">
            <input v-model="form.icon" type="radio" name="vc-icon" :value="icon" class="jt-visually-hidden" />
            <VehicleClassIcon :icon="icon" :size="22" />
            <span>{{ t(`classes.icon_${icon}`) }}</span>
          </label>
        </div>
        <p v-if="fieldErrors.icon" class="err" role="alert">{{ fieldErrors.icon }}</p>
      </fieldset>

      <div class="grid grid--2">
        <FormField v-for="[key, label, hint] in numberFields" :key="key" v-slot="f" :label="t(label)" :error="fieldErrors[key]" :hint="t(hint)" :required="key !== 'sort'">
          <TextInput :id="f.id" v-model="form[key]" inputmode="numeric" :invalid="f.invalid" />
        </FormField>
      </div>

      <fieldset class="block">
        <legend class="block__title">{{ t('classes.carries') }}</legend>
        <ToggleSwitch v-model="form.passenger_capable" :label="t('classes.carries_passengers')" />
        <ToggleSwitch v-model="form.cargo_capable" :label="t('classes.carries_cargo')" />
        <p class="jt-muted hint">{{ t('classes.carries_hint') }}</p>
        <p v-if="fieldErrors.passenger_capable || fieldErrors.cargo_capable" class="err" role="alert">{{ t('classes.carries_required') }}</p>
      </fieldset>

      <fieldset class="block">
        <legend class="block__title">{{ t('classes.serves') }}</legend>
        <p class="jt-muted hint">{{ t('classes.serves_hint') }}</p>
        <div class="serves">
          <label class="serves__item">
            <input type="checkbox" checked disabled />
            <span>{{ creating ? t('classes.serves_self') : fmt.name(vehicleClass?.name) }}</span>
          </label>
          <label v-for="c in others" :key="c.code" class="serves__item">
            <input type="checkbox" :checked="form.serves.includes(c.code)" @change="toggleServe(c.code, ($event.target as HTMLInputElement).checked)" />
            <VehicleClassIcon :icon="c.icon" :size="16" />
            <span>{{ fmt.name(c.name) }}</span>
            <span class="jt-mono jt-muted">{{ c.code }}</span>
          </label>
        </div>
        <p v-if="fieldErrors.serves" class="err" role="alert">{{ fieldErrors.serves }}</p>
      </fieldset>

      <fieldset class="block">
        <legend class="block__title">{{ t('common.status') }}</legend>
        <ToggleSwitch v-model="form.is_active" :label="form.is_active ? t('common.active') : t('common.inactive')" />
        <p class="jt-muted hint">{{ t('classes.active_hint') }}</p>
      </fieldset>
      <button type="submit" hidden />
    </form>
    <template #footer>
      <AppButton @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="primary" :loading="saving" @click="save">{{ creating ? t('common.create') : t('common.save') }}</AppButton>
    </template>
  </Drawer>
</template>

<style scoped>
.grid {
  display: grid;
  gap: var(--jt-space-3);
}

.grid--2 {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.grid--3 {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.block {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--jt-space-2);
  margin: 0;
  padding: 0;
  border: 0;
}

.block__title {
  margin-bottom: 4px;
  font: 500 13px/18px var(--jt-font-family);
}

.hint {
  font: var(--jt-font-small);
}

.icons {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--jt-space-2);
  align-self: stretch;
}

.icons__item {
  display: flex;
  align-items: center;
  gap: 8px;
  height: var(--jt-control-height-lg);
  padding: 0 12px;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  font: var(--jt-font-small);
  cursor: pointer;
}

.icons__item.is-selected {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}

.icons__item:focus-within {
  outline: 2px solid var(--jt-color-blue);
  outline-offset: 2px;
}

.serves {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 6px var(--jt-space-4);
  align-self: stretch;
}

.serves__item {
  display: flex;
  align-items: center;
  gap: 8px;
}

.serves__item input {
  accent-color: #000;
}

.err {
  font: var(--jt-font-small);
  color: var(--jt-color-red-text);
}
</style>
