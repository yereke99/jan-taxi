<!--
  New tariff version for a city × category × vehicle class (the API never
  edits a version in place). Prefilled from the current version of the chosen
  class; the same rules as the API: min/max are multiples of the step,
  max ≥ min, commission ≤ 50 %, and only classes that can carry the category.
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { tariffsApi, type Tariff } from '../../api/config'
import { classServesCategory } from '../../api/vehicleClasses'
import { AppButton, Drawer, FormField, SelectInput, TextInput, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useVehicleClasses } from '../../composables/useVehicleClasses'
import { bpsToPercentInput, parseInteger } from '../../forms/numbers'
import { DEFAULT_FARE_STEP, LIMITS, validateTariffForm, type FeeRow, type TariffForm } from '../../forms/validation'
import type { Category } from '../../utils/status'

const props = defineProps<{
  open: boolean
  cityId: string
  category: Category
  vehicleClass: string
  /** Every version of the city (current versions prefill the form). */
  tariffs: Tariff[]
  defaultCommissionBps?: number
}>()
const emit = defineEmits<{ 'update:open': [open: boolean]; saved: [tariff: Tariff] }>()

const { t } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const vc = useVehicleClasses()

function currentOf(cls: string): Tariff | null {
  return props.tariffs.find((x) => x.category === props.category && x.vehicle_class === cls && x.is_active) ?? null
}

function initial(cls: string): TariffForm {
  const c = currentOf(cls)
  return {
    city_id: props.cityId,
    category: props.category,
    vehicle_class: cls,
    min_fare: String(c?.min_fare ?? 500),
    max_fare: String(c?.max_fare ?? 1500),
    fare_step: String(c?.fare_step ?? DEFAULT_FARE_STEP),
    base_fare: String(c?.base_fare ?? 0),
    per_km: String(c?.per_km ?? 0),
    per_minute: String(c?.per_minute ?? 0),
    extra_stop_fee: String(c?.extra_stop_fee ?? 0),
    commission_percent: bpsToPercentInput(c?.commission_bps ?? props.defaultCommissionBps ?? 1000),
    extra_fees: (c?.extra_fees ?? []).map((f) => ({ code: f.code, nameKk: f.name.kk, nameRu: f.name.ru, nameEn: f.name.en, amount: String(f.amount) })),
  }
}

const form = reactive<TariffForm>(initial(props.vehicleClass))
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)
/** Snapshot of the last prefill: switching class re-prefills only an untouched form. */
let pristine = JSON.stringify(form)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    Object.assign(form, initial(props.vehicleClass))
    pristine = JSON.stringify(form)
    fieldErrors.value = {}
  },
)

watch(
  () => form.vehicle_class,
  (cls, prev) => {
    if (!cls || cls === prev) return
    const snapshot = JSON.stringify({ ...form, vehicle_class: prev })
    if (snapshot !== pristine) return
    Object.assign(form, initial(cls))
    pristine = JSON.stringify(form)
  },
)

/** Classes that can carry the category; a class already priced here stays selectable. */
const classOptions = computed(() =>
  vc.classes.value
    .filter((c) => classServesCategory(c, props.category) || c.code === form.vehicle_class)
    .map((c) => ({ value: c.code, label: c.is_active === false ? `${fmt.name(c.name)} · ${t('classes.inactive')}` : fmt.name(c.name) })),
)

const current = computed(() => currentOf(form.vehicle_class))
const title = computed(() => t('config.tariff_new_title', { category: `${t(`category.${props.category}`)} · ${vc.name(form.vehicle_class)}` }))

/** Fare options the passenger will see (min..max by step), or null when the inputs are not valid yet. */
const preview = computed(() => {
  const min = parseInteger(form.min_fare)
  const max = parseInteger(form.max_fare)
  const step = parseInteger(form.fare_step)
  if (min === null || max === null || step === null || step <= 0 || max < min) return null
  const count = Math.floor((max - min) / step) + 1
  return { count, sample: [min, Math.min(max, min + step), max].filter((v, i, a) => a.indexOf(v) === i) }
})

function addFee(): void {
  form.extra_fees.push({ code: '', nameKk: '', nameRu: '', nameEn: '', amount: '' })
}

function removeFee(i: number): void {
  form.extra_fees.splice(i, 1)
}

async function save(): Promise<void> {
  const v = validateTariffForm(form)
  fieldErrors.value = errors.translate(v.errors)
  if (!v.payload || saving.value) return
  saving.value = true
  try {
    const created = await tariffsApi.create(v.payload)
    toast.success(t('config.tariff_saved', { version: created.version }))
    emit('saved', created)
    emit('update:open', false)
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    if (Object.keys(fieldErrors.value).length === 0) errors.report(err)
  } finally {
    saving.value = false
  }
}

const moneyFields = [
  ['min_fare', 'config.min_fare'],
  ['max_fare', 'config.max_fare'],
  ['fare_step', 'config.fare_step'],
  ['base_fare', 'config.base_fare'],
  ['per_km', 'config.per_km'],
  ['per_minute', 'config.per_minute'],
  ['extra_stop_fee', 'config.extra_stop_fee'],
] as const

function feeError(i: number, part: string): string {
  return fieldErrors.value[`extra_fees.${i}.${part}`] ?? ''
}

function feeKey(_: FeeRow, i: number): number {
  return i
}
</script>

<template>
  <Drawer :open="open" :title="title" width="620px" @update:open="(v) => emit('update:open', v)">
    <form class="jt-form" novalidate @submit.prevent="save">
      <FormField v-slot="f" :label="t('classes.class')" :error="fieldErrors.vehicle_class" :hint="t('config.tariff_class_hint')" required>
        <SelectInput :id="f.id" :model-value="form.vehicle_class || null" :options="classOptions" :invalid="f.invalid" @update:model-value="(v) => (form.vehicle_class = v ?? '')" />
      </FormField>
      <p class="jt-muted">{{ current ? t('config.tariff_new_hint', { version: current.version }) : t('config.tariff_first_hint') }} {{ t('config.amounts_in_kzt') }}</p>
      <div class="grid">
        <FormField v-for="[key, label] in moneyFields" :key="key" v-slot="f" :label="t(label)" :error="fieldErrors[key]" :required="key === 'min_fare' || key === 'max_fare' || key === 'fare_step'">
          <TextInput :id="f.id" v-model="form[key]" inputmode="numeric" :invalid="f.invalid" />
        </FormField>
        <FormField v-slot="f" :label="t('config.commission_percent')" :error="fieldErrors.commission_bps" :hint="t('config.commission_hint', { max: LIMITS.maxCommissionBps / 100 })" required>
          <TextInput :id="f.id" v-model="form.commission_percent" inputmode="decimal" :invalid="f.invalid" />
        </FormField>
      </div>
      <p class="jt-muted note">{{ t('config.tariff_commission_note') }}</p>
      <p v-if="preview" class="preview">
        {{ t('config.fare_preview', { n: preview.count, values: preview.sample.map((v) => fmt.money(v)).join(', ') }) }}
      </p>

      <div class="fees">
        <div class="fees__head">
          <h3 class="jt-section-title">{{ t('config.extra_fees') }}</h3>
          <AppButton size="sm" :disabled="form.extra_fees.length >= LIMITS.tariffExtraFees" @click="addFee">{{ t('common.add') }}</AppButton>
        </div>
        <p v-if="fieldErrors.extra_fees" class="fees__err">{{ fieldErrors.extra_fees }}</p>
        <p v-if="form.extra_fees.length === 0" class="jt-muted">{{ t('config.no_extra_fees') }}</p>
        <div v-for="(fee, i) in form.extra_fees" :key="feeKey(fee, i)" class="fee">
          <div class="fee__row">
            <FormField v-slot="f" :label="t('config.fee_code')" :error="feeError(i, 'code')" :hint="t('config.fee_code_hint')">
              <TextInput :id="f.id" v-model="fee.code" :invalid="f.invalid" />
            </FormField>
            <FormField v-slot="f" :label="t('config.fee_amount')" :error="feeError(i, 'amount')">
              <TextInput :id="f.id" v-model="fee.amount" inputmode="numeric" :invalid="f.invalid" />
            </FormField>
            <button type="button" class="jt-link-button danger fee__remove" @click="removeFee(i)">{{ t('common.delete') }}</button>
          </div>
          <div class="fee__row fee__row--names">
            <FormField v-slot="f" :label="t('config.name_kk')" :error="feeError(i, 'name.kk')">
              <TextInput :id="f.id" v-model="fee.nameKk" :invalid="f.invalid" />
            </FormField>
            <FormField v-slot="f" :label="t('config.name_ru')" :error="feeError(i, 'name.ru')">
              <TextInput :id="f.id" v-model="fee.nameRu" :invalid="f.invalid" />
            </FormField>
            <FormField v-slot="f" :label="t('config.name_en')" :error="feeError(i, 'name.en')">
              <TextInput :id="f.id" v-model="fee.nameEn" :invalid="f.invalid" />
            </FormField>
          </div>
        </div>
      </div>
      <button type="submit" hidden />
    </form>
    <template #footer>
      <AppButton @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="primary" :loading="saving" @click="save">{{ t('config.publish_version') }}</AppButton>
    </template>
  </Drawer>
</template>

<style scoped>
.grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--jt-space-3);
}

.note {
  font: var(--jt-font-small);
}

.preview {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-small);
}

.fees {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.fees__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.fees__err,
.danger {
  color: var(--jt-color-red-text);
}

.fee {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  padding: 12px;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
}

.fee__row {
  display: grid;
  grid-template-columns: 1fr 1fr auto;
  gap: var(--jt-space-2);
  align-items: end;
}

.fee__row--names {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.fee__remove {
  height: var(--jt-control-height-lg);
}
</style>
