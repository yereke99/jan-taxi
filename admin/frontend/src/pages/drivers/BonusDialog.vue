<!--
  Company bonus to one driver (finance.manage, §22.7). Without a preset
  driver the operator first finds one by driver code (EMB001) or phone in any
  format; then enters the amount and an optional comment, and confirms. The
  idempotency key is made when the dialog opens, so a double click or a retry
  never pays twice. The driver gets a bot message from Jan Taxi.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { driversApi, type AdminDriverListItem, type BonusResult, type BonusTarget } from '../../api/drivers'
import { AppButton, AppSpinner, FormField, Modal, StatusBadge, TextArea, TextInput, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { LIMITS, validateBonus, type BonusForm } from '../../forms/validation'
import { isAbortError } from '../../services/api'
import { uuidv4 } from '../../utils/id'

const props = withDefaults(defineProps<{ open: boolean; driver?: BonusTarget | null }>(), { driver: null })
const emit = defineEmits<{ 'update:open': [open: boolean]; done: [result: BonusResult, driver: BonusTarget] }>()

const { t } = useI18n()
const fmt = useFormat()
const errors = useErrors()

const PRESETS = [500, 1000, 2000, 5000]

type Step = 'find' | 'edit' | 'confirm'
const step = ref<Step>('find')
const target = ref<BonusTarget | null>(null)
const form = reactive<BonusForm>({ amount: '', comment: '' })
const key = ref('')
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const busy = ref(false)

const q = ref('')
const results = ref<AdminDriverListItem[]>([])
const searching = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
let ctrl: AbortController | null = null

watch(
  () => props.open,
  (open) => {
    if (!open) return
    target.value = props.driver
    step.value = props.driver ? 'edit' : 'find'
    Object.assign(form, { amount: '', comment: '' })
    key.value = uuidv4()
    fieldErrors.value = {}
    formError.value = ''
    q.value = ''
    results.value = []
  },
)

watch(q, () => {
  clearTimeout(timer)
  timer = setTimeout(() => void search(), 250)
})

async function search(): Promise<void> {
  ctrl?.abort()
  const text = q.value.trim()
  if (text.length < 2) {
    results.value = []
    searching.value = false
    return
  }
  const mine = new AbortController()
  ctrl = mine
  searching.value = true
  formError.value = ''
  try {
    const page = await driversApi.list({ q: text }, 1, 8, mine.signal)
    if (ctrl === mine) results.value = page.items
  } catch (err) {
    if (!isAbortError(err) && ctrl === mine) formError.value = errors.text(err)
  } finally {
    if (ctrl === mine) searching.value = false
  }
}

function choose(d: AdminDriverListItem): void {
  target.value = {
    id: d.id,
    code: d.code,
    first_name: d.first_name,
    last_name: d.last_name,
    phone: d.phone,
    status: d.status,
    balance: null,
  }
  step.value = 'edit'
}

function changeDriver(): void {
  target.value = null
  step.value = 'find'
}

const checked = computed(() => validateBonus(form))
const balanceAfter = computed(() => {
  const b = target.value?.balance
  return typeof b === 'number' && checked.value.amount !== null ? b + checked.value.amount : null
})

function fullName(d: { first_name: string; last_name: string }): string {
  return `${d.first_name} ${d.last_name}`.trim()
}

function review(): void {
  fieldErrors.value = errors.translate(checked.value.errors)
  formError.value = ''
  if (checked.value.amount !== null) step.value = 'confirm'
}

async function submit(): Promise<void> {
  const d = target.value
  const amount = checked.value.amount
  if (!d || amount === null || busy.value) return
  busy.value = true
  formError.value = ''
  try {
    const comment = checked.value.comment
    const res = await driversApi.grantBonus(d.id, comment ? { amount, comment, idempotency_key: key.value } : { amount, idempotency_key: key.value })
    toast.success(t('bonus.granted_ok', { amount: fmt.money(res.entry.balance_delta), code: d.code }))
    emit('done', res, d)
    emit('update:open', false)
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    formError.value = errors.text(err)
    step.value = 'edit'
  } finally {
    busy.value = false
  }
}

onBeforeUnmount(() => {
  clearTimeout(timer)
  ctrl?.abort()
})
</script>

<template>
  <Modal :open="open" :title="target ? t('bonus.title_for', { code: target.code }) : t('bonus.title')" width="560px" :persistent="busy" @update:open="(v) => emit('update:open', v)">
    <div v-if="step === 'find'" class="bonus">
      <p class="jt-muted">{{ t('bonus.find_hint') }}</p>
      <TextInput v-model="q" type="search" :placeholder="t('bonus.find_placeholder')" :aria-label="t('common.search')" />
      <div class="bonus__list" role="listbox" :aria-label="t('orders.drivers_found')" :aria-busy="searching">
        <AppSpinner v-if="searching && results.length === 0" :size="18" />
        <p v-else-if="results.length === 0" class="jt-muted bonus__empty">{{ q.trim().length < 2 ? t('bonus.find_start') : t('orders.no_drivers_found') }}</p>
        <button v-for="d in results" :key="d.id" type="button" role="option" class="bonus__row" :aria-selected="false" @click="choose(d)">
          <span class="jt-mono bonus__code">{{ d.code }}</span>
          <span class="bonus__name">
            {{ fullName(d) || '—' }}
            <span class="jt-muted jt-mono">{{ d.phone ?? '' }}</span>
          </span>
          <StatusBadge kind="moderation" :status="d.status" />
        </button>
      </div>
      <p v-if="formError" class="bonus__error" role="alert">{{ formError }}</p>
    </div>

    <form v-else-if="step === 'edit' && target" class="jt-form" novalidate @submit.prevent="review">
      <div class="bonus__who">
        <span>
          <strong class="jt-mono">{{ target.code }}</strong> · {{ fullName(target) || '—' }}
          <span v-if="target.phone" class="jt-muted jt-mono"> · {{ target.phone }}</span>
        </span>
        <button v-if="!driver" type="button" class="jt-link-button" @click="changeDriver">{{ t('bonus.change_driver') }}</button>
      </div>
      <FormField v-slot="f" :label="t('bonus.amount')" :error="fieldErrors.amount" :hint="t('bonus.amount_hint', { max: fmt.money(LIMITS.maxBonus) })" required>
        <TextInput :id="f.id" v-model="form.amount" inputmode="numeric" :invalid="f.invalid" placeholder="0 ₸" />
      </FormField>
      <div class="bonus__presets">
        <button v-for="p in PRESETS" :key="p" type="button" class="bonus__preset" :class="{ 'is-on': checked.amount === p }" @click="form.amount = String(p)">{{ fmt.money(p) }}</button>
      </div>
      <FormField v-slot="f" :label="t('common.comment')" :error="fieldErrors.comment" :hint="t('bonus.comment_hint')">
        <TextArea :id="f.id" v-model="form.comment" :rows="2" :max-length="LIMITS.bonusComment" :invalid="f.invalid" />
      </FormField>
      <p v-if="formError" class="bonus__error" role="alert">{{ formError }}</p>
    </form>

    <div v-else-if="step === 'confirm' && target" class="jt-form">
      <p>{{ t('bonus.confirm', { amount: fmt.money(checked.amount), code: target.code, name: fullName(target) || target.code }) }}</p>
      <p v-if="balanceAfter !== null" class="jt-muted">{{ t('drivers.adjust_balance_after', { amount: fmt.money(balanceAfter) }) }}</p>
      <div class="bonus__message">
        <p class="jt-muted">{{ t('bonus.bot_message_label') }}</p>
        <p>{{ t('bonus.bot_message', { amount: fmt.money(checked.amount) }) }}</p>
        <p v-if="checked.comment">{{ checked.comment }}</p>
      </div>
      <p class="jt-muted bonus__key">{{ t('drivers.idempotency_key') }}: <span class="jt-mono">{{ key }}</span></p>
      <p v-if="formError" class="bonus__error" role="alert">{{ formError }}</p>
    </div>

    <template #footer>
      <template v-if="step === 'find'">
        <AppButton @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      </template>
      <template v-else-if="step === 'edit'">
        <AppButton @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
        <AppButton variant="primary" @click="review">{{ t('common.next') }}</AppButton>
      </template>
      <template v-else>
        <AppButton :disabled="busy" @click="step = 'edit'">{{ t('common.back') }}</AppButton>
        <AppButton variant="primary" :loading="busy" @click="submit">{{ t('bonus.grant') }}</AppButton>
      </template>
    </template>
  </Modal>
</template>

<style scoped>
.bonus {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.bonus__list {
  display: flex;
  flex-direction: column;
  max-height: 300px;
  min-height: 96px;
  overflow-y: auto;
  padding: 4px;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
}

.bonus__empty {
  padding: var(--jt-space-4);
  text-align: center;
}

.bonus__row {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--jt-space-2);
  padding: 8px 10px;
  border-radius: 10px;
  text-align: left;
}

.bonus__row:hover,
.bonus__row:focus-visible {
  background: var(--jt-color-hover);
}

.bonus__name {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.bonus__name .jt-muted {
  font-size: 12px;
}

.bonus__who {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
  padding: 10px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-fill-secondary);
}

.bonus__presets {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: calc(-1 * var(--jt-space-2));
}

.bonus__preset {
  height: 28px;
  padding: 0 12px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  font: 500 13px/18px var(--jt-font-family);
}

.bonus__preset:hover {
  background: var(--jt-color-fill-tertiary);
}

.bonus__preset.is-on {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.bonus__message {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-fill-secondary);
  overflow-wrap: anywhere;
}

.bonus__key {
  font: var(--jt-font-caption);
}

.bonus__error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
