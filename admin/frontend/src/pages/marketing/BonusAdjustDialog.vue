<!--
  Manual correction of a passenger's bonus balance (finance.manage): a signed
  amount with a reason. The idempotency key is made when the dialog opens,
  so a retried or double-clicked submit never applies it twice; the
  second step confirms the amount and the new balance (sent with confirm: true).
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { bonusApi, campaignsApi, type BonusAccount, type ManualRules } from '../../api/marketing'
import { AppButton, FormField, Modal, Tabs, TextArea, TextInput } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import { validateAdjustment, type AdjustmentForm } from '../../forms/validation'
import { isApiError } from '../../services/api'
import { uuidv4 } from '../../utils/id'

/** The API's range of one correction (either sign); credits are also capped by the manual campaign's max_amount. */
const MAX_ADJUSTMENT = 1_000_000
const MAX_REASON = 300

const props = defineProps<{ open: boolean; userId: string; userName: string; bonus: BonusAccount | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; done: [bonus: BonusAccount, amount: number] }>()

const { t } = useI18n()
const errors = useErrors()
const fmt = useFormat()
const form = reactive<AdjustmentForm>({ direction: 'credit', amount: '', reason: '' })
const key = ref('')
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const step = ref<'edit' | 'confirm'>('edit')
const busy = ref(false)

// The manual bonus bucket (manual_grant) caps one credit at its max_amount.
const manual = useResource((signal) => campaignsApi.list({ template: 'manual_grant' }, 1, 5, signal))
const maxCredit = computed<number | null>(() => {
  const c = manual.data.value?.items.find((x) => x.template === 'manual_grant' && x.status !== 'archived')
  const v = (c?.rules as Partial<ManualRules> | undefined)?.max_amount
  return typeof v === 'number' ? v : null
})

watch(
  () => props.open,
  (open) => {
    if (!open) return
    Object.assign(form, { direction: 'credit', amount: '', reason: '' })
    key.value = uuidv4()
    fieldErrors.value = {}
    formError.value = ''
    step.value = 'edit'
    if (!manual.data.value) void manual.reload()
  },
)

const directionTabs = computed(() => [
  { value: 'credit' as const, label: t('marketing.adjust_credit') },
  { value: 'debit' as const, label: t('marketing.adjust_debit') },
])

const checked = computed(() => validateAdjustment(form))
const balanceAfter = computed(() => (props.bonus && checked.value.amount !== null ? props.bonus.balance + checked.value.amount : null))

const amountHint = computed(() =>
  maxCredit.value === null ? t('marketing.adjust_amount_hint_plain') : t('marketing.adjust_amount_hint', { max: fmt.money(maxCredit.value) }),
)

function review(): void {
  const local = { ...checked.value.errors }
  const amount = checked.value.amount
  if (amount !== null && Math.abs(amount) > MAX_ADJUSTMENT) local.amount = 'out_of_range'
  else if (amount !== null && maxCredit.value !== null && amount > maxCredit.value) local.amount = 'above_max'
  if (!local.reason && [...checked.value.reason].length < 3) local.reason = 'too_short'
  if (!local.reason && [...checked.value.reason].length > MAX_REASON) local.reason = 'too_long'
  fieldErrors.value = errors.translate(local)
  formError.value = ''
  if (local.amount === 'above_max' && maxCredit.value !== null) fieldErrors.value.amount = t('marketing.adjust_above_max', { max: fmt.money(maxCredit.value) })
  if (Object.keys(local).length > 0 || amount === null) return
  if (props.bonus && amount < 0 && -amount > props.bonus.available) {
    formError.value = t('marketing.adjust_exceeds', { available: fmt.money(props.bonus.available) })
    return
  }
  step.value = 'confirm'
}

/** The bonus balance has its own words for a short balance; above_max names the cap the API reports. */
function errorText(err: unknown): string {
  if (isApiError(err) && err.code === 'insufficient_balance') return t('marketing.bonus_insufficient')
  if (isApiError(err) && err.fields.amount === 'above_max' && typeof err.details.max_amount === 'number') {
    return t('marketing.adjust_above_max', { max: fmt.money(err.details.max_amount) })
  }
  return errors.text(err)
}

async function submit(): Promise<void> {
  const amount = checked.value.amount
  if (amount === null || busy.value) return
  busy.value = true
  formError.value = ''
  try {
    const bonus = await bonusApi.adjust(props.userId, { amount, reason: checked.value.reason, idempotency_key: key.value, confirm: true })
    emit('done', bonus, amount)
    emit('update:open', false)
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    formError.value = errorText(err)
    step.value = 'edit'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :open="open" :title="t('marketing.adjust_title')" width="480px" :persistent="busy" @update:open="(v) => emit('update:open', v)">
    <form v-if="step === 'edit'" class="jt-form" novalidate @submit.prevent="review">
      <p v-if="bonus" class="jt-muted">
        {{ t('marketing.bonus_balance') }}: <strong class="jt-num">{{ fmt.money(bonus.balance) }}</strong> · {{ t('marketing.bonus_available') }}: <span class="jt-num">{{ fmt.money(bonus.available) }}</span>
      </p>
      <Tabs v-model="form.direction" :tabs="directionTabs" :aria-label="t('marketing.adjust_direction')" />
      <FormField v-slot="f" :label="t('marketing.adjust_amount')" :hint="amountHint" :error="fieldErrors.amount" required>
        <TextInput :id="f.id" v-model="form.amount" inputmode="numeric" :invalid="f.invalid" :placeholder="'0 ₸'" />
      </FormField>
      <FormField v-slot="f" :label="t('common.reason')" :error="fieldErrors.reason" :hint="t('marketing.adjust_reason_hint')" required>
        <TextArea :id="f.id" v-model="form.reason" :rows="3" :max-length="MAX_REASON" :invalid="f.invalid" />
      </FormField>
      <p class="jt-muted adj__key">{{ t('drivers.idempotency_key') }}: <span class="jt-mono">{{ key }}</span></p>
      <p v-if="formError" class="adj__error" role="alert">{{ formError }}</p>
    </form>
    <div v-else class="jt-form">
      <p>{{ t('marketing.adjust_confirm', { name: userName, amount: fmt.signedMoney(checked.amount) }) }}</p>
      <p v-if="balanceAfter !== null" class="jt-muted">{{ t('marketing.adjust_after', { amount: fmt.money(balanceAfter) }) }}</p>
      <p class="adj__reason">{{ checked.reason }}</p>
      <p v-if="(checked.amount ?? 0) > 0" class="jt-muted">{{ t('marketing.adjust_message_note') }}</p>
      <p v-if="formError" class="adj__error" role="alert">{{ formError }}</p>
    </div>
    <template #footer>
      <template v-if="step === 'edit'">
        <AppButton @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
        <AppButton variant="primary" @click="review">{{ t('common.next') }}</AppButton>
      </template>
      <template v-else>
        <AppButton :disabled="busy" @click="step = 'edit'">{{ t('common.back') }}</AppButton>
        <AppButton variant="primary" :loading="busy" @click="submit">{{ t('marketing.adjust_apply') }}</AppButton>
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
