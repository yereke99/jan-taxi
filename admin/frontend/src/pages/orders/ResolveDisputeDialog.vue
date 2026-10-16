<!--
  Resolve a dispute: the order ends as completed or cancelled; a cancelled
  outcome may refund the commission already charged to the driver.
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { disputesApi, type Dispute, type DisputeOutcome } from '../../api/orders'
import { AppButton, FormField, Modal, TextArea, ToggleSwitch } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { LIMITS, validateReason } from '../../forms/validation'

const props = defineProps<{ open: boolean; dispute: Dispute | null; commission?: number | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; resolved: [dispute: Dispute] }>()

const { t } = useI18n()
const errors = useErrors()
const fmt = useFormat()
const outcome = ref<DisputeOutcome>('completed')
const resolution = ref('')
const refund = ref(false)
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const busy = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    outcome.value = 'completed'
    resolution.value = ''
    refund.value = false
    fieldErrors.value = {}
    formError.value = ''
  },
)

watch(outcome, (o) => {
  if (o === 'completed') refund.value = false
})

async function submit(): Promise<void> {
  const d = props.dispute
  if (!d || busy.value) return
  const problem = validateReason(resolution.value, LIMITS.disputeResolution)
  fieldErrors.value = problem ? { resolution: errors.reason(problem) } : {}
  formError.value = ''
  if (problem) return
  busy.value = true
  try {
    const res = await disputesApi.resolve(d.id, { outcome: outcome.value, resolution: resolution.value.trim(), refund_commission: refund.value })
    emit('resolved', res)
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
  <Modal :open="open" :title="t('orders.resolve_title', { n: dispute?.order_number ?? '' })" width="520px" :persistent="busy" @update:open="(v) => emit('update:open', v)">
    <form v-if="dispute" class="jt-form" novalidate @submit.prevent="submit">
      <div class="resolve__reason">
        <p class="jt-muted">{{ t('orders.dispute_reason') }}</p>
        <p>{{ dispute.reason }}</p>
      </div>
      <fieldset class="resolve__outcome">
        <legend class="resolve__legend">{{ t('orders.outcome') }}</legend>
        <label class="resolve__option">
          <input v-model="outcome" type="radio" value="completed" />
          <span>
            <strong>{{ t('orders.outcome_completed') }}</strong>
            <span class="jt-muted">{{ t('orders.outcome_completed_hint') }}</span>
          </span>
        </label>
        <label class="resolve__option">
          <input v-model="outcome" type="radio" value="cancelled" />
          <span>
            <strong>{{ t('orders.outcome_cancelled') }}</strong>
            <span class="jt-muted">{{ t('orders.outcome_cancelled_hint') }}</span>
          </span>
        </label>
      </fieldset>
      <ToggleSwitch v-model="refund" :disabled="outcome !== 'cancelled'">
        {{ t('orders.refund_commission') }}
        <template v-if="commission"> ({{ fmt.money(commission) }})</template>
      </ToggleSwitch>
      <p v-if="fieldErrors.refund_commission" class="resolve__error">{{ fieldErrors.refund_commission }}</p>
      <FormField v-slot="f" :label="t('orders.resolution')" :error="fieldErrors.resolution" required>
        <TextArea :id="f.id" v-model="resolution" :rows="4" :max-length="LIMITS.disputeResolution" :invalid="f.invalid" :aria-describedby="f.describedby" />
      </FormField>
      <p v-if="formError" class="resolve__error" role="alert">{{ formError }}</p>
    </form>
    <template #footer>
      <AppButton :disabled="busy" @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="primary" :loading="busy" @click="submit">{{ t('orders.resolve') }}</AppButton>
    </template>
  </Modal>
</template>

<style scoped>
.resolve__reason {
  padding: 10px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-fill-secondary);
  overflow-wrap: anywhere;
}

.resolve__outcome {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  margin: 0;
  padding: 0;
  border: 0;
}

.resolve__legend {
  margin-bottom: 4px;
  font: 500 13px/18px var(--jt-font-family);
}

.resolve__option {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  cursor: pointer;
}

.resolve__option:has(input:checked) {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}

.resolve__option input {
  margin-top: 3px;
  accent-color: #000;
}

.resolve__option > span {
  display: flex;
  flex-direction: column;
}

.resolve__error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
