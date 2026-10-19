<!--
  Passenger cancellation (ARCHITECTURE §22.3): an action-sheet style panel
  that says the cancellation is final, lists the five reasons (labels from
  the public config, the app's own as a fallback) and a comment of at most 120
  code points with a counter, required for "Other". Sends POST
  /orders/{id}/cancel {reason_code, comment?, expected_status} and emits the
  updated order; when the order changed meanwhile (409 order_changed) it emits
  "failed" so the screen shows the new state first.
-->
<script setup lang="ts">
import { Check } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { cancelOrder } from '../../api/orders'
import type { CancelReasonCode, Order } from '../../api/types'
import { CANCEL_COMMENT_MAX, cancelPayload, cancelReasonOptions, validateCancel, type CancelField, type CancelReasonOption } from '../../domain/cancel'
import { useErrorText, useFieldErrorText } from '../../i18n/core'
import { isApiError } from '../../services/http'
import { useSessionStore } from '../../stores/session'
import { haptics } from '../../telegram'
import ModalPanel from '../ModalPanel.vue'
import { JButton, JTextArea, toast } from '../ui'

const props = defineProps<{ open: boolean; order: Order }>()
const emit = defineEmits<{ close: []; cancelled: [order: Order]; failed: [] }>()

const { t } = useI18n()
const errorText = useErrorText()
const fieldErrorText = useFieldErrorText()
const session = useSessionStore()

const reasons = computed(() =>
  cancelReasonOptions(session.config.cancel_reasons, session.language, (code: CancelReasonCode) => t(`cancel.reason.${code}`)),
)
const selected = ref<CancelReasonOption | null>(null)
const comment = ref('')
const errors = ref<Partial<Record<CancelField, string>>>({})
const sending = ref(false)
const area = ref<InstanceType<typeof JTextArea> | null>(null)
const assigned = computed(() => props.order.driver !== null && props.order.status !== 'searching')

watch(
  () => props.open,
  (open) => {
    if (!open) return
    selected.value = null
    comment.value = ''
    errors.value = {}
  },
)

watch(comment, () => {
  if (errors.value.comment) errors.value = { ...errors.value, comment: undefined }
})

function choose(r: CancelReasonOption): void {
  if (sending.value) return
  haptics.selection()
  selected.value = r
  errors.value = {}
}

function close(): void {
  if (!sending.value) emit('close')
}

async function submit(): Promise<void> {
  const reason = selected.value
  const problems = validateCancel(reason, comment.value)
  if (!reason || Object.keys(problems).length > 0) {
    errors.value = problems
    haptics.notification('error')
    if (problems.comment) area.value?.focus()
    return
  }
  sending.value = true
  try {
    const order = await cancelOrder(props.order.id, cancelPayload(reason, comment.value, props.order.status))
    haptics.notification('success')
    emit('cancelled', order)
  } catch (err) {
    if (isApiError(err) && err.code === 'validation_failed' && (err.fields.comment || err.fields.reason_code)) {
      errors.value = { comment: err.fields.comment, reason_code: err.fields.reason_code }
      if (err.fields.comment) area.value?.focus()
    } else {
      toast.error(errorText(err))
      // The order changed meanwhile (a driver took it, arrived, or it was already
      // cancelled): the sheet closes and the screen re-syncs, so the passenger
      // sees the driver before cancelling again.
      if (isApiError(err) && (err.code === 'invalid_transition' || err.code === 'conflict' || err.status === 404)) emit('failed')
    }
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <ModalPanel :open="open" :label="t('cancel.title')" :locked="sending" @close="close">
    <div class="cancel">
      <header class="cancel__head">
        <h2 class="cancel__title">{{ t('cancel.title') }}</h2>
        <p class="cancel__text">{{ t('cancel.final') }}</p>
        <p v-if="assigned" class="cancel__text">{{ t('cancel.assigned_note') }}</p>
      </header>

      <div class="cancel__reasons" role="radiogroup" :aria-label="t('cancel.reasons')" :data-invalid="errors.reason_code ? 'true' : undefined">
        <button
          v-for="r in reasons"
          :key="r.code"
          type="button"
          role="radio"
          class="cancel__reason"
          :aria-checked="selected?.code === r.code"
          :disabled="sending"
          @click="choose(r)"
        >
          <span class="cancel__reason-label">{{ r.label }}</span>
          <Check v-if="selected?.code === r.code" :size="22" :stroke-width="2.5" class="cancel__check" aria-hidden="true" />
        </button>
      </div>
      <p v-if="errors.reason_code" class="cancel__error" role="alert">{{ fieldErrorText(errors.reason_code) }}</p>

      <JTextArea
        ref="area"
        v-model="comment"
        :label="selected?.commentRequired ? t('cancel.comment') : t('cancel.comment_optional')"
        :placeholder="t('cancel.comment_placeholder')"
        :max="CANCEL_COMMENT_MAX"
        :rows="2"
        :max-rows="4"
        :disabled="sending"
        enterkeyhint="done"
        :error="errors.comment ? fieldErrorText(errors.comment) : ''"
      />
    </div>

    <template #footer>
      <JButton block variant="destructive" :loading="sending" :disabled="!selected" @click="submit">{{ t('cancel.confirm') }}</JButton>
      <JButton block variant="plain" :disabled="sending" @click="close">{{ t('cancel.keep') }}</JButton>
    </template>
  </ModalPanel>
</template>

<style scoped>
.cancel {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
}

.cancel__head {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-1);
  text-align: center;
}

.cancel__title {
  font: var(--jt-font-title3);
}

.cancel__text {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.cancel__reasons {
  display: flex;
  flex-direction: column;
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
  overflow: hidden;
}

.cancel__reason {
  position: relative;
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-height: var(--jt-row-min-height);
  padding: 0 var(--jt-space-4);
  font: var(--jt-font-body);
  text-align: left;
}

.cancel__reason + .cancel__reason::before {
  content: '';
  position: absolute;
  top: 0;
  left: var(--jt-space-4);
  right: 0;
  border-top: var(--jt-hairline) solid var(--jt-color-separator);
}

.cancel__reason:active:not(:disabled) {
  background: var(--jt-color-fill-tertiary);
}

.cancel__reason-label {
  flex: 1;
  min-width: 0;
}

.cancel__reason[aria-checked='true'] .cancel__reason-label {
  font-weight: 600;
}

.cancel__check {
  flex: none;
  color: var(--jt-color-label);
}

.cancel__error {
  margin-top: calc(-1 * var(--jt-space-2));
  padding: 0 var(--jt-space-1);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}
</style>
