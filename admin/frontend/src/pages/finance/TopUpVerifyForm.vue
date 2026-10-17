<!--
  Kaspi verification (§6.1): the operator checks the payment in the Kaspi Pay
  merchant history and records the verified amount, the transaction/receipt
  number and the payment time. With a parsed Kaspi receipt the form starts
  from the receipt's values (confirm_prefill). Confirming credits the
  driver's wallet once; the same Kaspi transaction can never be recorded twice.
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { topUpsApi, type AdminTopUpDetail } from '../../api/finance'
import { AppButton, ConfirmDialog, FormField, TextArea, TextInput } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { LIMITS, toLocalDateTimeInput, validateTopUpConfirm, type TopUpConfirmForm, type TopUpConfirmPayload } from '../../forms/validation'

const props = defineProps<{ topup: AdminTopUpDetail }>()
const emit = defineEmits<{ confirmed: [topup: AdminTopUpDetail] }>()

const { t } = useI18n()
const fmt = useFormat()
const errors = useErrors()

const form = reactive<TopUpConfirmForm>({ verifiedAmount: '', transactionId: '', paidAt: '', payerInfo: '', note: '' })
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const pending = ref<TopUpConfirmPayload | null>(null)
const busy = ref(false)

const prefill = computed(() => props.topup.confirm_prefill)
const prefilled = computed(() => Boolean(prefill.value?.provider_transaction_id))

// Reset when another top-up is shown or a new receipt brings other values.
watch(
  () => `${props.topup.id}|${prefill.value?.provider_transaction_id ?? ''}`,
  () => {
    const p = prefill.value
    Object.assign(form, {
      verifiedAmount: String(p?.verified_amount ?? props.topup.amount),
      transactionId: p?.provider_transaction_id ?? '',
      paidAt: toLocalDateTimeInput(p?.paid_at),
      payerInfo: p?.payer_info ?? '',
      note: '',
    })
    fieldErrors.value = {}
    formError.value = ''
  },
  { immediate: true },
)

const differs = computed(() => {
  const n = Number(form.verifiedAmount.replace(/\s/g, ''))
  return Number.isFinite(n) && n > 0 && n !== props.topup.amount
})

function review(): void {
  formError.value = ''
  const v = validateTopUpConfirm(form)
  fieldErrors.value = errors.translate(v.errors)
  pending.value = v.payload
}

async function credit(): Promise<void> {
  const body = pending.value
  if (!body || busy.value) return
  busy.value = true
  try {
    const updated = await topUpsApi.confirmPayment(props.topup.id, body)
    pending.value = null
    emit('confirmed', updated)
  } catch (err) {
    pending.value = null
    fieldErrors.value = errors.fields(err)
    formError.value = errors.text(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="verify" novalidate @submit.prevent="review">
    <div class="verify__notice" role="note">
      <strong>{{ t('finance.verify_notice_title') }}</strong>
      <p>{{ t('finance.verify_notice') }}</p>
    </div>
    <p v-if="prefilled" class="verify__prefill">{{ t('finance.prefill_note') }}</p>
    <FormField v-slot="f" :label="t('finance.verified_amount')" :error="fieldErrors.verified_amount" :hint="differs ? t('finance.amount_differs', { amount: fmt.money(topup.amount) }) : ''" required>
      <TextInput :id="f.id" v-model="form.verifiedAmount" inputmode="numeric" :invalid="f.invalid" :aria-describedby="f.describedby" />
    </FormField>
    <FormField v-slot="f" :label="t('finance.transaction_id')" :error="fieldErrors.provider_transaction_id" :hint="t('finance.transaction_id_hint')" required>
      <TextInput :id="f.id" v-model="form.transactionId" class="jt-mono" :invalid="f.invalid" :aria-describedby="f.describedby" />
    </FormField>
    <FormField v-slot="f" :label="t('finance.paid_at')" :error="fieldErrors.paid_at" required>
      <input :id="f.id" v-model="form.paidAt" type="datetime-local" class="verify__datetime" :aria-invalid="f.invalid || undefined" />
    </FormField>
    <FormField v-slot="f" :label="t('finance.payer_info')" :error="fieldErrors.payer_info" :hint="t('finance.payer_info_hint')">
      <TextInput :id="f.id" v-model="form.payerInfo" :invalid="f.invalid" />
    </FormField>
    <FormField v-slot="f" :label="t('common.note')" :error="fieldErrors.note">
      <TextArea :id="f.id" v-model="form.note" :rows="2" :max-length="LIMITS.topUpNote" :invalid="f.invalid" />
    </FormField>
    <p v-if="formError" class="verify__error" role="alert">{{ formError }}</p>
    <AppButton type="submit" variant="primary" size="lg" block>{{ t('finance.confirm_topup') }}</AppButton>

    <ConfirmDialog
      :open="pending !== null"
      :title="t('finance.confirm_title', { reference: topup.reference })"
      :message="
        pending
          ? t('finance.confirm_message', {
              amount: fmt.money(pending.verified_amount),
              code: topup.driver.code,
              txn: pending.provider_transaction_id,
              time: fmt.dateTime(pending.paid_at),
            })
          : ''
      "
      :confirm-label="t('finance.credit_wallet', { amount: pending ? fmt.money(pending.verified_amount) : '' })"
      :loading="busy"
      @confirm="credit"
      @cancel="pending = null"
    />
  </form>
</template>

<style scoped>
.verify {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
}

.verify__notice {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-orange-tint);
  color: var(--jt-color-orange-text);
}

.verify__prefill {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-blue-tint);
  color: var(--jt-color-blue-text);
  font: var(--jt-font-small);
}

.verify__datetime {
  height: var(--jt-control-height-lg);
  padding: 0 10px;
  border: 0;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  font: var(--jt-font-body);
  outline: none;
}

.verify__datetime:focus {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}

.verify__datetime[aria-invalid='true'] {
  box-shadow: inset 0 0 0 1px var(--jt-color-red);
}

.verify__error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
