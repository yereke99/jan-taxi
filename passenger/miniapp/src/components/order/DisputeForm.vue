<!-- "Report a problem": a reason (≤500 code points) sent as a dispute (POST /orders/{id}/dispute). -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { disputeOrder } from '../../api/orders'
import type { Order } from '../../api/types'
import { useErrorText, useFieldErrorText } from '../../i18n/core'
import { isApiError } from '../../services/http'
import { haptics } from '../../telegram'
import { cleanedLength, cleanText } from '../../utils/text'
import { JButton, JTextArea, toast } from '../ui'

/** disputes.reason limit (API maxDisputeReason). */
const DISPUTE_REASON_MAX = 500

const props = defineProps<{ orderId: string }>()
const emit = defineEmits<{ sent: [order: Order]; cancel: [] }>()

const { t } = useI18n()
const errorText = useErrorText()
const fieldErrorText = useFieldErrorText()

const reason = ref('')
const sending = ref(false)
const error = ref('')
const length = computed(() => cleanedLength(reason.value))
const canSend = computed(() => length.value > 0 && length.value <= DISPUTE_REASON_MAX && !sending.value)

async function send(): Promise<void> {
  if (!canSend.value) {
    error.value = fieldErrorText(length.value === 0 ? 'required' : 'too_long')
    return
  }
  sending.value = true
  error.value = ''
  try {
    const order = await disputeOrder(props.orderId, cleanText(reason.value))
    haptics.notification('success')
    toast.success(t('dispute.sent'))
    emit('sent', order)
  } catch (err) {
    if (isApiError(err) && err.fields.reason) error.value = fieldErrorText(err.fields.reason)
    else toast.error(errorText(err))
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <section class="dispute">
    <h2 class="dispute__title">{{ t('dispute.title') }}</h2>
    <JTextArea
      v-model="reason"
      :label="t('dispute.reason')"
      :placeholder="t('dispute.reason_placeholder')"
      :max="DISPUTE_REASON_MAX"
      :rows="3"
      :error="error"
    />
    <div class="dispute__actions">
      <JButton block :loading="sending" :disabled="!canSend" @click="send">{{ t('dispute.submit') }}</JButton>
      <JButton block variant="plain" size="medium" :disabled="sending" @click="emit('cancel')">{{ t('common.cancel') }}</JButton>
    </div>
  </section>
</template>

<style scoped>
.dispute {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.dispute__title {
  font: var(--jt-font-headline);
}

.dispute__actions {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-1);
}
</style>
