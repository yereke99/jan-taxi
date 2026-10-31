<!--
  Request corrections: tick the registration fields to fix, choose a reason
  for each (reason "other" needs a comment), add optional per-field and
  general comments. The driver sees these in the Mini App and the bot. Only
  fields the driver can still change are offered: ID scans and vehicle
  photos are no longer collected, so they (and the reasons about them)
  cannot be asked for again.
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { CORRECTABLE_FIELDS, CORRECTION_REASONS, driversApi, type AdminDriverDetail, type CorrectableField, type CorrectionReason } from '../../api/drivers'
import { AppButton, FormField, Modal, SelectInput, TextArea, TextInput } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { LIMITS, validateCorrectionForm, type CorrectionRow } from '../../forms/validation'

const props = defineProps<{ open: boolean; driver: AdminDriverDetail }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; done: [driver: AdminDriverDetail] }>()

const { t } = useI18n()
const errors = useErrors()

interface FieldState {
  checked: boolean
  reason: CorrectionReason | null
  comment: string
}

const state = reactive<Record<CorrectableField, FieldState>>(
  Object.fromEntries(CORRECTABLE_FIELDS.map((f) => [f, { checked: false, reason: null, comment: '' }])) as Record<CorrectableField, FieldState>,
)
const comment = ref('')
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const busy = ref(false)

/** Reasons about the retired ID scans and vehicle photos. */
const PHOTO_REASONS: readonly CorrectionReason[] = ['id_unreadable', 'vehicle_photo_unclear']

const reasonOptions = computed(() =>
  CORRECTION_REASONS.filter((r) => !PHOTO_REASONS.includes(r)).map((r) => ({ value: r, label: t(`correction_reason.${r}`) })),
)

/** Reasons that make sense for a field come first in the list. */
const SUGGESTED: Partial<Record<CorrectableField, CorrectionReason>> = {
  vehicle_plate: 'invalid_plate',
  phone: 'duplicate_phone',
}

watch(
  () => props.open,
  (open) => {
    if (!open) return
    for (const f of CORRECTABLE_FIELDS) Object.assign(state[f], { checked: false, reason: null, comment: '' })
    comment.value = ''
    fieldErrors.value = {}
    formError.value = ''
  },
)

function toggle(f: CorrectableField, checked: boolean): void {
  state[f].checked = checked
  if (checked && !state[f].reason) state[f].reason = SUGGESTED[f] ?? null
}

const rows = computed<CorrectionRow[]>(() =>
  CORRECTABLE_FIELDS.filter((f) => state[f].checked).map((f) => ({ field: f, reason: state[f].reason, comment: state[f].comment })),
)

/** Maps "items.<i>.<x>" errors back to the field they belong to. */
function errorFor(field: CorrectableField, part: 'reason' | 'comment' | 'field'): string {
  const i = rows.value.findIndex((r) => r.field === field)
  return i >= 0 ? (fieldErrors.value[`items.${i}.${part}`] ?? '') : ''
}

async function submit(): Promise<void> {
  if (busy.value) return
  const v = validateCorrectionForm({ items: rows.value, comment: comment.value })
  fieldErrors.value = errors.translate(v.errors)
  formError.value = v.errors.items ? t('drivers.corrections_pick_field') : ''
  if (Object.keys(v.errors).length > 0) return
  busy.value = true
  try {
    const updated = await driversApi.requestCorrections(props.driver.id, v.items, v.comment ?? '')
    emit('done', updated)
    emit('update:open', false)
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    formError.value = errors.text(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :open="open" :title="t('drivers.corrections_title', { code: driver.code })" width="720px" :persistent="busy" @update:open="(v) => emit('update:open', v)">
    <form class="corr" novalidate @submit.prevent="submit">
      <p class="jt-muted">{{ t('drivers.corrections_hint') }}</p>
      <ul class="corr__list">
        <li v-for="f in CORRECTABLE_FIELDS" :key="f" class="corr__row" :class="{ 'is-checked': state[f].checked }">
          <label class="corr__check">
            <input type="checkbox" :checked="state[f].checked" @change="toggle(f, ($event.target as HTMLInputElement).checked)" />
            <span>{{ t(`correction_field.${f}`) }}</span>
          </label>
          <template v-if="state[f].checked">
            <div class="corr__reason">
              <SelectInput
                v-model="state[f].reason"
                :options="reasonOptions"
                :placeholder="t('drivers.choose_reason')"
                :invalid="Boolean(errorFor(f, 'reason') || errorFor(f, 'field'))"
                :aria-label="t('common.reason')"
              />
              <span v-if="errorFor(f, 'reason') || errorFor(f, 'field')" class="corr__err">{{ errorFor(f, 'reason') || errorFor(f, 'field') }}</span>
            </div>
            <div class="corr__comment">
              <TextInput
                v-model="state[f].comment"
                :placeholder="state[f].reason === 'other' ? t('drivers.comment_required') : t('drivers.comment_optional')"
                :invalid="Boolean(errorFor(f, 'comment'))"
                :aria-label="t('common.comment')"
              />
              <span v-if="errorFor(f, 'comment')" class="corr__err">{{ errorFor(f, 'comment') }}</span>
            </div>
          </template>
        </li>
      </ul>
      <FormField v-slot="ff" :label="t('drivers.general_comment')" :error="fieldErrors.comment">
        <TextArea :id="ff.id" v-model="comment" :rows="2" :max-length="LIMITS.decisionComment" :invalid="ff.invalid" />
      </FormField>
      <p v-if="formError" class="corr__error" role="alert">{{ formError }}</p>
    </form>
    <template #footer>
      <AppButton :disabled="busy" @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="primary" :loading="busy" :disabled="rows.length === 0" @click="submit">
        {{ t('drivers.send_corrections', { n: rows.length }) }}
      </AppButton>
    </template>
  </Modal>
</template>

<style scoped>
.corr {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.corr__list {
  display: flex;
  flex-direction: column;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
}

.corr__row {
  display: grid;
  grid-template-columns: 230px 220px 1fr;
  align-items: start;
  gap: var(--jt-space-2);
  min-height: 44px;
  padding: 6px 12px;
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.corr__row:last-child {
  border-bottom: 0;
}

.corr__row.is-checked {
  background: var(--jt-color-fill-secondary);
}

.corr__check {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 32px;
  cursor: pointer;
}

.corr__check input {
  flex: none;
  width: 16px;
  height: 16px;
  accent-color: #000;
}

.corr__reason,
.corr__comment {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.corr__reason :deep(.select) {
  min-width: 0;
  width: 100%;
}

.corr__comment :deep(.input) {
  height: var(--jt-control-height);
}

.corr__err {
  font: var(--jt-font-caption);
  color: var(--jt-color-red-text);
}

.corr__error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
