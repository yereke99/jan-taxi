<!--
  Confirmation of a marketing action that may need a time (schedule) and a
  reason or an optional note (approval note of a broadcast). The local time is
  sent as an RFC 3339 instant; the parent's `submit` performs the request and
  API errors stay inside the dialog.
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppButton, FormField, Modal, TextArea } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { parseLocalDateTime, toLocalDateTimeInput } from '../../forms/validation'
import { codePoints } from '../../utils/text'
import DateTimeField from './DateTimeField.vue'

const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    message?: string
    /** Ask for a future date and time. */
    withTime?: boolean
    /** Reason required (approver actions); otherwise an optional note. */
    reasonRequired?: boolean
    confirmLabel?: string
    destructive?: boolean
    submit: (input: { at: string | null; reason: string }) => Promise<void>
    /** Text for an API error the page explains better (e.g. approval_required with its numbers). */
    errorText?: (err: unknown) => string | null
  }>(),
  { message: '', withTime: false, reasonRequired: false, confirmLabel: '', destructive: false, errorText: undefined },
)
const emit = defineEmits<{ 'update:open': [open: boolean]; done: [] }>()

const MAX_REASON = 300
const { t } = useI18n()
const errors = useErrors()
const at = ref('')
const reason = ref('')
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const busy = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    // Suggest the next full hour.
    const next = new Date()
    next.setHours(next.getHours() + 1, 0, 0, 0)
    at.value = props.withTime ? toLocalDateTimeInput(next.toISOString()) : ''
    reason.value = ''
    fieldErrors.value = {}
    formError.value = ''
  },
)

const minAt = computed(() => toLocalDateTimeInput(new Date().toISOString()))

async function submitAction(): Promise<void> {
  const local: Record<string, string> = {}
  const when = props.withTime ? parseLocalDateTime(at.value) : null
  if (props.withTime) {
    if (!at.value) local.scheduled_at = 'required'
    else if (!when) local.scheduled_at = 'invalid'
    else if (when.getTime() <= Date.now()) local.scheduled_at = 'must_be_future'
  }
  const text = reason.value.replace(/\s+/g, ' ').trim()
  if (props.reasonRequired && !text) local.reason = 'required'
  else if (props.reasonRequired && codePoints(text) < 3) local.reason = 'too_short'
  else if (codePoints(text) > MAX_REASON) local.reason = 'too_long'
  fieldErrors.value = errors.translate(local)
  formError.value = ''
  if (Object.keys(local).length > 0 || busy.value) return
  busy.value = true
  try {
    await props.submit({ at: when ? when.toISOString() : null, reason: text })
    emit('update:open', false)
    emit('done')
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    formError.value = props.errorText?.(err) ?? errors.text(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :open="open" :title="title" width="480px" :persistent="busy" @update:open="(v) => emit('update:open', v)">
    <form class="jt-form" novalidate @submit.prevent="submitAction">
      <p v-if="message" class="jt-muted action__message">{{ message }}</p>
      <FormField v-if="withTime" v-slot="f" :label="t('marketing.schedule_at')" :hint="t('marketing.schedule_at_hint')" :error="fieldErrors.scheduled_at" required>
        <DateTimeField :id="f.id" v-model="at" :min="minAt" :invalid="f.invalid" />
      </FormField>
      <FormField v-slot="f" :label="reasonRequired ? t('common.reason') : t('marketing.note_optional')" :error="fieldErrors.reason" :required="reasonRequired">
        <TextArea :id="f.id" v-model="reason" :rows="3" :max-length="MAX_REASON" :invalid="f.invalid" :placeholder="reasonRequired ? t('marketing.reason_placeholder') : ''" />
      </FormField>
      <p v-if="formError" class="action__error" role="alert">{{ formError }}</p>
    </form>
    <template #footer>
      <AppButton :disabled="busy" @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton :variant="destructive ? 'danger' : 'primary'" :loading="busy" @click="submitAction">{{ confirmLabel || t('common.confirm') }}</AppButton>
    </template>
  </Modal>
</template>

<style scoped>
.action__message {
  white-space: pre-line;
}

.action__error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
  white-space: pre-line;
}
</style>
