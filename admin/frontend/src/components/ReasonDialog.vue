<!--
  Confirmation that needs a written reason (cancel order, reject, suspend,
  refund …). The parent's `submit` performs the request; API field errors and
  error codes are shown inside the dialog, which stays open until it succeeds.
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useErrors } from '../composables/useErrors'
import { cleanText, validateReason } from '../forms/validation'
import { codePoints } from '../utils/text'
import AppButton from './AppButton.vue'
import FormField from './FormField.vue'
import Modal from './Modal.vue'
import TextArea from './TextArea.vue'

const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    message?: string
    label?: string
    placeholder?: string
    maxLength: number
    /** Shortest reason the API accepts (marketing decisions need 3 characters); 0 = any. */
    minLength?: number
    confirmLabel?: string
    destructive?: boolean
    /** Suggested reasons the operator can pick with one click. */
    presets?: string[]
    submit: (reason: string) => Promise<void>
    /** Text for an API error the page explains better than the error code does. */
    errorText?: (err: unknown) => string | null
  }>(),
  { message: '', label: '', placeholder: '', minLength: 0, confirmLabel: '', destructive: false, presets: () => [], errorText: undefined },
)
const emit = defineEmits<{ 'update:open': [open: boolean]; done: [] }>()

const { t } = useI18n()
const errors = useErrors()
const reason = ref('')
const fieldError = ref('')
const formError = ref('')
const busy = ref(false)

watch(
  () => props.open,
  (open) => {
    if (open) {
      reason.value = ''
      fieldError.value = ''
      formError.value = ''
    }
  },
)

const canSubmit = computed(() => reason.value.trim().length > 0 && !busy.value)

async function submitReason(): Promise<void> {
  formError.value = ''
  let problem = validateReason(reason.value, props.maxLength)
  if (!problem && codePoints(cleanText(reason.value)) < props.minLength) problem = 'too_short'
  fieldError.value = problem ? errors.reason(problem) : ''
  if (problem || busy.value) return
  busy.value = true
  try {
    await props.submit(reason.value.trim())
    emit('update:open', false)
    emit('done')
  } catch (err) {
    const f = errors.fields(err)
    fieldError.value = f.reason ?? ''
    formError.value = props.errorText?.(err) ?? errors.text(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :open="open" :title="title" width="480px" :persistent="busy" @update:open="(v) => emit('update:open', v)">
    <form class="jt-form" novalidate @submit.prevent="submitReason">
      <p v-if="message" class="jt-muted reason__message">{{ message }}</p>
      <div v-if="presets.length" class="reason__presets">
        <button v-for="p in presets" :key="p" type="button" class="reason__preset" @click="reason = p">{{ p }}</button>
      </div>
      <FormField v-slot="f" :label="label || t('common.reason')" :error="fieldError" required>
        <TextArea
          :id="f.id"
          v-model="reason"
          :rows="3"
          :max-length="maxLength"
          :placeholder="placeholder"
          :invalid="f.invalid"
          :aria-describedby="f.describedby"
        />
      </FormField>
      <p v-if="formError" class="reason__error" role="alert">{{ formError }}</p>
    </form>
    <template #footer>
      <AppButton :disabled="busy" @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton :variant="destructive ? 'danger' : 'primary'" :loading="busy" :disabled="!canSubmit" @click="submitReason">
        {{ confirmLabel || t('common.confirm') }}
      </AppButton>
    </template>
  </Modal>
</template>

<style scoped>
.reason__message {
  white-space: pre-line;
}

.reason__presets {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.reason__preset {
  height: 26px;
  padding: 0 10px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  font: 500 12px/16px var(--jt-font-family);
}

.reason__preset:hover {
  background: var(--jt-color-fill-tertiary);
}

.reason__error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
