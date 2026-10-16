<!--
  Manual wallet adjustment (finance.manage): a signed amount with a reason.
  The idempotency key is generated when the dialog opens, so a retried or
  double-clicked submit can never apply the adjustment twice (§6).
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ledgerApi, type WalletAdjustmentResult } from '../../api/finance'
import type { Wallet } from '../../api/drivers'
import { AppButton, FormField, Modal, Tabs, TextArea, TextInput } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { LIMITS, validateAdjustment, type AdjustmentForm } from '../../forms/validation'
import { uuidv4 } from '../../utils/id'

const props = defineProps<{ open: boolean; driverId: string; driverCode: string; wallet: Wallet | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; done: [result: WalletAdjustmentResult] }>()

const { t } = useI18n()
const errors = useErrors()
const fmt = useFormat()
const form = reactive<AdjustmentForm>({ direction: 'credit', amount: '', reason: '' })
const key = ref('')
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const step = ref<'edit' | 'confirm'>('edit')
const busy = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    Object.assign(form, { direction: 'credit', amount: '', reason: '' })
    key.value = uuidv4()
    fieldErrors.value = {}
    formError.value = ''
    step.value = 'edit'
  },
)

const directionTabs = computed(() => [
  { value: 'credit' as const, label: t('drivers.adjust_credit') },
  { value: 'debit' as const, label: t('drivers.adjust_debit') },
])

const checked = computed(() => validateAdjustment(form))
const balanceAfter = computed(() => (props.wallet && checked.value.amount !== null ? props.wallet.balance + checked.value.amount : null))

function review(): void {
  fieldErrors.value = errors.translate(checked.value.errors)
  formError.value = ''
  if (checked.value.amount === null) return
  if (props.wallet && checked.value.amount < 0 && -checked.value.amount > props.wallet.available) {
    formError.value = t('drivers.adjust_exceeds_available', { available: fmt.money(props.wallet.available) })
    return
  }
  step.value = 'confirm'
}

async function submit(): Promise<void> {
  const amount = checked.value.amount
  if (amount === null || busy.value) return
  busy.value = true
  formError.value = ''
  try {
    const res = await ledgerApi.adjust(props.driverId, { amount, reason: checked.value.reason, idempotency_key: key.value })
    emit('done', res)
    emit('update:open', false)
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    formError.value = errors.text(err)
    step.value = 'edit'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :open="open" :title="t('drivers.adjust_title', { code: driverCode })" width="480px" :persistent="busy" @update:open="(v) => emit('update:open', v)">
    <form v-if="step === 'edit'" class="jt-form" novalidate @submit.prevent="review">
      <Tabs v-model="form.direction" :tabs="directionTabs" :aria-label="t('drivers.adjust_direction')" />
      <FormField v-slot="f" :label="t('drivers.adjust_amount')" :error="fieldErrors.amount" required>
        <TextInput :id="f.id" v-model="form.amount" inputmode="numeric" :invalid="f.invalid" :placeholder="'0 ₸'" />
      </FormField>
      <FormField v-slot="f" :label="t('common.reason')" :error="fieldErrors.reason" :hint="t('drivers.adjust_reason_hint')" required>
        <TextArea :id="f.id" v-model="form.reason" :rows="3" :max-length="LIMITS.adjustmentReason" :invalid="f.invalid" />
      </FormField>
      <p class="jt-muted adj__key">{{ t('drivers.idempotency_key') }}: <span class="jt-mono">{{ key }}</span></p>
      <p v-if="formError" class="adj__error" role="alert">{{ formError }}</p>
    </form>
    <div v-else class="jt-form">
      <p>{{ t('drivers.adjust_confirm', { amount: fmt.signedMoney(checked.amount), code: driverCode }) }}</p>
      <p v-if="balanceAfter !== null" class="jt-muted">{{ t('drivers.adjust_balance_after', { amount: fmt.money(balanceAfter) }) }}</p>
      <p class="adj__reason">{{ checked.reason }}</p>
      <p v-if="formError" class="adj__error" role="alert">{{ formError }}</p>
    </div>
    <template #footer>
      <template v-if="step === 'edit'">
        <AppButton @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
        <AppButton variant="primary" @click="review">{{ t('common.next') }}</AppButton>
      </template>
      <template v-else>
        <AppButton :disabled="busy" @click="step = 'edit'">{{ t('common.back') }}</AppButton>
        <AppButton variant="primary" :loading="busy" @click="submit">{{ t('drivers.adjust_apply') }}</AppButton>
      </template>
    </template>
  </Modal>
</template>

<style scoped>
.adj__key {
  font: var(--jt-font-caption);
}

.adj__reason {
  padding: 8px 10px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-fill-secondary);
  overflow-wrap: anywhere;
}

.adj__error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
