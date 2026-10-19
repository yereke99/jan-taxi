<!--
  Operator cancel with a written reason. The commission the driver paid when
  taking the order is kept unless the operator ticks the refund (§22.2); the
  dialog shows the amount from the order's ledger.
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ordersApi, type AdminOrder } from '../../api/orders'
import { AppButton, FormField, Modal, TextArea, ToggleSwitch } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { LIMITS, validateReason } from '../../forms/validation'
import { chargedCommission } from './commission'

const props = defineProps<{ open: boolean; order: AdminOrder }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; done: [order: AdminOrder] }>()

const { t } = useI18n()
const errors = useErrors()
const fmt = useFormat()
const reason = ref('')
const refund = ref(false)
const fieldError = ref('')
const formError = ref('')
const busy = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    reason.value = ''
    refund.value = false
    fieldError.value = ''
    formError.value = ''
  },
)

const charged = computed(() => chargedCommission(props.order.ledger, props.order.driver?.id))

async function submit(): Promise<void> {
  formError.value = ''
  const problem = validateReason(reason.value, LIMITS.orderCancelReason)
  fieldError.value = problem ? errors.reason(problem) : ''
  if (problem || busy.value) return
  busy.value = true
  try {
    const updated = await ordersApi.cancel(props.order.id, reason.value.trim(), refund.value && charged.value > 0)
    emit('done', updated)
    emit('update:open', false)
  } catch (err) {
    fieldError.value = errors.fields(err).reason ?? ''
    formError.value = errors.text(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :open="open" :title="t('orders.cancel_title', { n: order.number })" width="500px" :persistent="busy" @update:open="(v) => emit('update:open', v)">
    <form class="jt-form" novalidate @submit.prevent="submit">
      <p class="jt-muted">{{ order.driver ? t('orders.cancel_message_driver') : t('orders.cancel_message') }}</p>
      <FormField v-slot="f" :label="t('common.reason')" :error="fieldError" required>
        <TextArea :id="f.id" v-model="reason" :rows="3" :max-length="LIMITS.orderCancelReason" :invalid="f.invalid" :aria-describedby="f.describedby" />
      </FormField>
      <div v-if="order.driver" class="refund">
        <ToggleSwitch v-model="refund" :disabled="charged === 0">
          {{ t('orders.refund_commission_to', { code: order.driver.code }) }}
          <template v-if="charged > 0"> ({{ fmt.money(charged) }})</template>
        </ToggleSwitch>
        <p class="jt-muted refund__note">{{ charged > 0 ? t('orders.refund_commission_note') : t('orders.refund_nothing') }}</p>
      </div>
      <p v-if="formError" class="error" role="alert">{{ formError }}</p>
    </form>
    <template #footer>
      <AppButton :disabled="busy" @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="danger" :loading="busy" :disabled="!reason.trim()" @click="submit">{{ t('orders.cancel_order') }}</AppButton>
    </template>
  </Modal>
</template>

<style scoped>
.refund {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 12px;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
}

.refund__note {
  font: var(--jt-font-small);
}

.error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
