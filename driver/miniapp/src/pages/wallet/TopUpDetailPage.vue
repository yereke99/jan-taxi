<!--
  One Kaspi top-up (§6.1). While `created`: the exact amount, recipient,
  payment code (copy), driver code and numbered steps, the Kaspi link, then
  "I paid" with an optional Kaspi receipt number and receipt photo. "I paid"
  only moves the top-up to verification: the balance changes after a finance
  admin confirms it against Kaspi records (realtime topup.confirmed).
-->
<script setup lang="ts">
import { CircleCheck, CircleX, Clock3, ExternalLink, ImagePlus, Info, RefreshCw } from '@lucide/vue'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import type { TopUp } from '../../api/types'
import CopyField from '../../components/driver/CopyField.vue'
import DriverPage from '../../components/driver/DriverPage.vue'
import { confirmDestructive, JBadge, JButton, JEmptyState, JListGroup, JListRow, JSkeleton, JSpinner, JTextArea, JTextField, toast } from '../../components/ui'
import { useDriverErrorText } from '../../composables/useDriverErrorText'
import { useFormat } from '../../composables/useFormat'
import { ImageError } from '../../features/media/image'
import { useUploadTask } from '../../features/media/useUploadTask'
import { ROUTE } from '../../router/names'
import { useWalletStore } from '../../stores/wallet'
import { haptics, openExternal } from '../../telegram'
import { formatKzPhone } from '../../utils/phone'
import { topUpStatusTone } from '../../utils/status'

const RECEIPT_REF_MAX = 100
const NOTE_MAX = 300

const { t } = useI18n()
const fmt = useFormat()
const errorText = useDriverErrorText()
const route = useRoute()
const router = useRouter()
const wallet = useWalletStore()

const id = computed(() => String(route.params.id ?? ''))
const topup = computed<TopUp | null>(() => wallet.byId[id.value] ?? null)
const loading = ref(false)
const loadError = ref<unknown>(null)
const paidMode = ref(false)
const receiptNumber = ref('')
const note = ref('')
const submitting = ref(false)
const cancelling = ref(false)
const receiptInput = ref<HTMLInputElement | null>(null)

const pay = computed(() => topup.value?.payment ?? null)

const receipt = useUploadTask<TopUp>((blob, onProgress, signal) => wallet.uploadReceipt(id.value, blob, onProgress, signal))
const receiptBusy = computed(() => receipt.state.value === 'preparing' || receipt.state.value === 'uploading')
const receiptError = computed(() => {
  const err = receipt.error.value
  if (!err) return ''
  return err instanceof ImageError ? t(`registration.upload.${err.code}`) : errorText(err)
})

async function load(): Promise<void> {
  loading.value = true
  try {
    await wallet.fetchTopUp(id.value)
    loadError.value = null
  } catch (err) {
    loadError.value = err
  } finally {
    loading.value = false
  }
}

function openKaspi(): void {
  const url = pay.value?.url
  if (!url) return
  haptics.impact('light')
  if (!openExternal(url)) toast.error(t('trip.open_failed'))
}

function onReceipt(e: Event): void {
  const el = e.target as HTMLInputElement
  const file = el.files?.[0]
  el.value = ''
  if (file) void receipt.run(file)
}

async function submit(): Promise<void> {
  if (!topup.value || submitting.value || receiptBusy.value) return
  submitting.value = true
  try {
    await wallet.submitTopUp(id.value, { payment_reference: receiptNumber.value, note: note.value })
    haptics.notification('success')
    paidMode.value = false
    void wallet.refreshWallet()
  } catch (err) {
    haptics.notification('error')
    toast.error(errorText(err))
  } finally {
    submitting.value = false
  }
}

async function cancel(): Promise<void> {
  if (!topup.value || cancelling.value) return
  const ok = await confirmDestructive({ title: t('topup.cancel_confirm'), confirmLabel: t('topup.cancel') })
  if (!ok) return
  cancelling.value = true
  try {
    await wallet.cancelTopUp(id.value)
    toast.show(t('topup.cancelled_toast'))
  } catch (err) {
    toast.error(errorText(err))
  } finally {
    cancelling.value = false
  }
}

watch(id, () => void load())
onMounted(() => {
  if (!wallet.wallet) void wallet.refreshWallet()
})

onMounted(() => void load())
</script>

<template>
  <DriverPage :title="topup && topup.status !== 'created' ? t('topup.number', { number: topup.number }) : t('topup.title')">
    <div v-if="!topup && loading" class="td__skeleton" aria-busy="true">
      <JSkeleton height="160px" radius="22px" />
      <JSkeleton height="220px" radius="22px" />
    </div>

    <div v-else-if="!topup" class="td__state">
      <JEmptyState :title="t('app.load_failed_title')" :description="loadError ? errorText(loadError) : ''">
        <template #action><JButton block @click="load">{{ t('common.retry') }}</JButton></template>
      </JEmptyState>
    </div>

    <template v-else>
      <!-- Waiting for the payment: instructions. -->
      <template v-if="topup.status === 'created' && pay">
        <section v-if="wallet.wallet" class="td__balance">
          <span class="td__balance-label">{{ t('wallet.available') }}</span>
          <strong class="td__balance-value jt-tabular">{{ fmt.money(wallet.wallet.available) }}</strong>
          <span v-if="wallet.wallet.reserved > 0" class="td__balance-meta jt-tabular">
            {{ t('topup.reserved_short', { amount: fmt.money(wallet.wallet.reserved) }) }}
          </span>
        </section>

        <h2 class="td__section">{{ t('topup.details_title') }}</h2>
        <section class="td__details">
          <div class="td__row">
            <span>{{ t('topup.amount') }}</span>
            <strong class="jt-tabular">{{ fmt.money(pay.amount) }}</strong>
          </div>
          <div class="td__row is-stacked">
            <span class="td__row-label">{{ t('topup.recipient') }}</span>
            <span class="td__row-value">{{ pay.recipient_name }}</span>
          </div>
          <div v-if="pay.recipient_phone" class="td__row">
            <span>{{ t('topup.recipient_phone') }}</span>
            <span class="td__row-value jt-tabular">{{ formatKzPhone(pay.recipient_phone) }}</span>
          </div>
          <div class="td__row is-copy">
            <CopyField :label="t('topup.reference')" :value="pay.reference" />
          </div>
        </section>

        <section class="td__steps">
          <ol class="td__list">
            <li><span class="td__num">1</span>{{ t('topup.step1') }}</li>
            <li><span class="td__num">2</span>{{ t('topup.step2', { amount: fmt.money(pay.amount) }) }}</li>
            <li><span class="td__num">3</span>{{ t('topup.step3', { reference: pay.reference }) }}</li>
            <li><span class="td__num">4</span>{{ t('topup.step4') }}</li>
          </ol>
          <p v-if="pay.instructions" class="td__server">{{ pay.instructions }}</p>
        </section>

        <p class="td__note">
          <Info :size="18" :stroke-width="2" aria-hidden="true" />
          <span>{{ t('topup.verification_note') }} {{ t('topup.expires_at', { time: fmt.dateTime(topup.expires_at) }) }}.</span>
        </p>

        <section v-if="paidMode" class="td__paid">
          <h2 class="td__title">{{ t('topup.paid_title') }}</h2>
          <p class="td__muted">{{ t('topup.paid_text') }}</p>
          <JTextField
            v-model="receiptNumber"
            :label="t('topup.receipt_number')"
            :placeholder="t('topup.receipt_number_placeholder')"
            :max="RECEIPT_REF_MAX"
            enterkeyhint="done"
          />
          <JTextArea v-model="note" :label="t('topup.note')" :placeholder="t('topup.note_placeholder')" :max="NOTE_MAX" :rows="2" />
          <div class="td__receipt">
            <img v-if="receipt.previewUrl.value" :src="receipt.previewUrl.value" alt="" class="td__receipt-img" />
            <button type="button" class="td__receipt-btn" :disabled="receiptBusy" @click="receiptInput?.click()">
              <JSpinner v-if="receiptBusy" size="small" />
              <ImagePlus v-else :size="18" :stroke-width="2" aria-hidden="true" />
              <span v-if="receipt.state.value === 'uploading'" class="jt-tabular">{{ Math.round(receipt.progress.value * 100) }}%</span>
              <span v-else>{{ receipt.state.value === 'done' || topup.has_receipt ? t('topup.replace_receipt') : t('topup.add_receipt') }}</span>
            </button>
            <p v-if="receipt.state.value === 'done' || (topup.has_receipt && receipt.state.value === 'idle')" class="td__ok">
              {{ t('topup.receipt_attached') }}
            </p>
            <p v-if="receipt.state.value === 'error'" class="td__err" role="alert">
              {{ receiptError || t('registration.upload.failed') }}
              <button type="button" class="td__retry" @click="receipt.retry()"><RefreshCw :size="14" :stroke-width="2.5" aria-hidden="true" /> {{ t('common.retry') }}</button>
            </p>
          </div>
          <input ref="receiptInput" class="jt-visually-hidden" type="file" accept="image/*" tabindex="-1" @change="onReceipt" />
        </section>

        <div v-if="!paidMode && topup.can_cancel" class="td__cancel">
          <JButton variant="destructive" block :loading="cancelling" @click="cancel">{{ t('topup.cancel') }}</JButton>
        </div>
      </template>

      <!-- Every other state: what happened and what the driver can do. -->
      <template v-else>
        <section class="td__status">
          <Clock3 v-if="topup.status === 'pending_verification'" :size="48" :stroke-width="1.5" class="td__icon" aria-hidden="true" />
          <CircleCheck v-else-if="topup.status === 'confirmed'" :size="48" :stroke-width="1.5" class="td__icon is-ok" aria-hidden="true" />
          <CircleX v-else :size="48" :stroke-width="1.5" class="td__icon" aria-hidden="true" />
          <template v-if="topup.status === 'pending_verification'">
            <h2 class="td__status-title">{{ t('topup.pending_title') }}</h2>
            <p class="td__muted">{{ t('topup.pending_text') }}</p>
          </template>
          <template v-else-if="topup.status === 'confirmed'">
            <h2 class="td__status-title">{{ t('topup.confirmed_title') }}</h2>
            <p class="td__muted">{{ t('topup.confirmed_text', { amount: fmt.money(topup.verified_amount ?? topup.amount) }) }}</p>
          </template>
          <template v-else-if="topup.status === 'rejected'">
            <h2 class="td__status-title">{{ t('topup.rejected_title') }}</h2>
            <p class="td__muted">{{ topup.rejected_reason ? t('topup.rejected_text', { reason: topup.rejected_reason }) : t('topup.rejected_no_reason') }}</p>
          </template>
          <template v-else-if="topup.status === 'expired'">
            <h2 class="td__status-title">{{ t('topup.expired_title') }}</h2>
            <p class="td__muted">{{ t('topup.expired_text') }}</p>
          </template>
          <template v-else>
            <h2 class="td__status-title">{{ t('topup.refunded_title') }}</h2>
          </template>
        </section>

        <JListGroup>
          <JListRow :title="t('topup.amount')" :value="fmt.money(topup.amount)" />
          <JListRow v-if="topup.verified_amount !== null" :title="t('topup.verified_amount')" :value="fmt.money(topup.verified_amount)" />
          <JListRow :title="t('topup.reference')" :subtitle="topup.reference" />
          <JListRow v-if="topup.driver_payment_reference" :title="t('topup.receipt_number')" :subtitle="topup.driver_payment_reference" />
          <JListRow :title="t('topup.receipt_photo')" :value="topup.has_receipt ? t('topup.receipt_attached') : t('topup.receipt_none')" />
          <JListRow :title="t('topup.created_at')" :value="fmt.dateTime(topup.created_at)" />
          <JListRow v-if="topup.driver_reported_paid_at" :title="t('topup.paid_at')" :value="fmt.dateTime(topup.driver_reported_paid_at)" />
          <JListRow :title="t('topup.status')">
            <template #accessory><JBadge :tone="topUpStatusTone(topup.status)" :label="t(`topup_status.${topup.status}`)" /></template>
          </JListRow>
        </JListGroup>

        <div v-if="topup.can_upload_receipt" class="td__more">
          <JButton variant="secondary" block :loading="receiptBusy" @click="receiptInput?.click()">
            {{ topup.has_receipt ? t('topup.replace_receipt') : t('topup.add_receipt') }}
          </JButton>
          <p v-if="receipt.state.value === 'done'" class="td__ok">{{ t('topup.receipt_attached') }}</p>
          <p v-if="receipt.state.value === 'error'" class="td__err" role="alert">{{ receiptError || t('registration.upload.failed') }}</p>
          <input ref="receiptInput" class="jt-visually-hidden" type="file" accept="image/*" tabindex="-1" @change="onReceipt" />
        </div>
        <div v-if="topup.status !== 'pending_verification'" class="td__more">
          <JButton variant="secondary" block @click="router.replace({ name: ROUTE.topup })">{{ t('topup.new_topup') }}</JButton>
        </div>
      </template>
    </template>

    <template v-if="topup && topup.status === 'created' && pay" #footer>
      <template v-if="!paidMode">
        <JButton v-if="pay.url" block @click="openKaspi">
          <template #icon><ExternalLink :size="18" :stroke-width="2.25" aria-hidden="true" /></template>
          {{ t('topup.pay_kaspi') }}
        </JButton>
        <p v-else class="td__muted td__center">{{ t('topup.no_payment_link') }}</p>
        <JButton variant="secondary" block @click="paidMode = true">{{ t('topup.paid') }}</JButton>
      </template>
      <template v-else>
        <JButton block :loading="submitting" :disabled="receiptBusy" @click="submit">{{ t('topup.submit') }}</JButton>
        <JButton variant="plain" block @click="paidMode = false">{{ t('common.back') }}</JButton>
      </template>
    </template>
  </DriverPage>
</template>

<style scoped>
.td__balance {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0 var(--jt-side-margin) var(--jt-space-5);
  padding: var(--jt-space-4) var(--jt-space-4) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.td__balance-label,
.td__balance-meta {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.td__balance-value {
  font: 700 34px/41px var(--jt-font-family);
  letter-spacing: -0.4px;
}

.td__section {
  margin: 0 calc(var(--jt-side-margin) + var(--jt-space-4)) var(--jt-space-2);
  font: var(--jt-font-subheadline);
  font-weight: 600;
  color: var(--jt-color-label-secondary);
}

.td__details {
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
  padding: 0 0 0 var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  overflow: hidden;
}

.td__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
  min-height: 52px;
  padding: var(--jt-space-3) var(--jt-space-4) var(--jt-space-3) 0;
  box-sizing: border-box;
  font: var(--jt-font-body);
}

.td__row + .td__row {
  border-top: var(--jt-hairline) solid var(--jt-color-separator);
}

.td__row strong {
  font-weight: 700;
}

.td__row.is-stacked {
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
}

.td__row-label {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.td__row-value {
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.td__row.is-stacked .td__row-value {
  color: var(--jt-color-label);
}

.td__row.is-copy {
  display: block;
  padding-top: 0;
  padding-bottom: 0;
}

.td__row.is-copy :deep(.cfield) {
  padding-left: 0;
  padding-right: 0;
}

.td__note {
  display: flex;
  gap: var(--jt-space-2);
  margin: 0 calc(var(--jt-side-margin) + var(--jt-space-1)) var(--jt-space-4);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.td__note svg {
  flex: none;
  margin-top: 1px;
}

.td__skeleton {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  padding: 0 var(--jt-side-margin);
}

.td__state {
  padding: var(--jt-space-8) var(--jt-side-margin);
}

.td__card {
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  overflow: hidden;
}

.td__hint {
  padding: 0 var(--jt-space-4) var(--jt-space-4);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.td__steps,
.td__paid,
.td__status {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.td__title {
  font: var(--jt-font-headline);
}

.td__list {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.td__list li {
  display: flex;
  gap: var(--jt-space-3);
  font: var(--jt-font-body);
}

.td__num {
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font: var(--jt-font-footnote);
  font-weight: 700;
}

.td__server {
  padding: var(--jt-space-3);
  border-radius: 12px;
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-subheadline);
  white-space: pre-line;
  overflow-wrap: anywhere;
}

.td__expires,
.td__muted {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.td__center {
  text-align: center;
}

.td__receipt {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

.td__receipt-img {
  width: 96px;
  height: 96px;
  object-fit: cover;
  border-radius: 14px;
}

.td__receipt-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--jt-space-2);
  height: var(--jt-button-height-medium);
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-subheadline);
  font-weight: 600;
}

.td__receipt-btn:disabled {
  opacity: var(--jt-opacity-disabled);
}

.td__ok {
  font: var(--jt-font-footnote);
  color: var(--jt-color-green-text);
}

.td__err {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-2);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}

.td__retry {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font: var(--jt-font-footnote);
  font-weight: 600;
  color: var(--jt-color-label);
}

.td__cancel,
.td__more {
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
}

.td__status {
  align-items: center;
  text-align: center;
}

.td__icon {
  color: var(--jt-color-label-secondary);
}

.td__icon.is-ok {
  color: var(--jt-color-green);
}

.td__status-title {
  font: var(--jt-font-title2);
}
</style>
