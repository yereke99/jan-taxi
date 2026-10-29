<!--
  One Kaspi top-up (§6.1). While it waits for the payment: the exact amount,
  the payee (ИП "JAN-TAXI", BIN), numbered steps — pay in Kaspi, save the
  receipt PDF, upload it here or send it to the driver bot — and the PDF
  upload with progress. The server checks the receipt at once: credited,
  left for an administrator (with the reasons) or rejected as already used.
  A screenshot is attached as evidence and sent for review.
-->
<script setup lang="ts">
import { CircleCheck, CircleX, Clock3, ExternalLink, FileText, RefreshCw, Send } from '@lucide/vue'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import type { TopUp } from '../../api/types'
import CopyButton from '../../components/driver/CopyButton.vue'
import DriverPage from '../../components/driver/DriverPage.vue'
import { confirmDestructive, JBadge, JButton, JEmptyState, JListGroup, JListRow, JSkeleton, JTextArea, JTextField, toast } from '../../components/ui'
import { useDriverErrorText } from '../../composables/useDriverErrorText'
import { useFormat } from '../../composables/useFormat'
import { formatBin, loadPayeeBin, resolvePayee } from '../../features/topup/payee'
import { knownReasons, RECEIPT_ACCEPT, receiptOutcome, receiptUploadError } from '../../features/topup/receipt'
import { ReceiptFileError, useReceiptUpload } from '../../features/topup/useReceiptUpload'
import { ROUTE } from '../../router/names'
import { useSessionStore } from '../../stores/session'
import { useWalletStore } from '../../stores/wallet'
import { haptics, openExternal, openTelegram } from '../../telegram'
import { topUpStatusTone } from '../../utils/status'

const RECEIPT_REF_MAX = 100
const NOTE_MAX = 300

const { t } = useI18n()
const fmt = useFormat()
const errorText = useDriverErrorText()
const route = useRoute()
const router = useRouter()
const wallet = useWalletStore()
const session = useSessionStore()

const id = computed(() => String(route.params.id ?? ''))
const topup = computed<TopUp | null>(() => wallet.byId[id.value] ?? null)
const loading = ref(false)
const loadError = ref<unknown>(null)
const paidMode = ref(false)
const receiptNumber = ref('')
const note = ref('')
const submitting = ref(false)
const cancelling = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)
const payeeBin = ref<string | null>(null)
const kaspiOpened = ref(false)

const outcome = computed(() => (topup.value ? receiptOutcome(topup.value) : null))
const waiting = computed(() => outcome.value === 'waiting')
const payee = computed(() => resolvePayee(session.config, topup.value, payeeBin.value))
const amount = computed(() => topup.value?.payment.amount ?? topup.value?.amount ?? 0)
const reasons = computed(() => knownReasons(topup.value?.receipt?.reasons))
const botName = computed(() => session.config.bots.driver_username)

const upload = useReceiptUpload({
  upload: (blob, name, onProgress, signal) => wallet.uploadReceipt(id.value, blob, name, (p) => onProgress(p.fraction), signal),
  submit: (tp) => wallet.submitTopUp(tp.id, {}),
})
const uploadBusy = computed(() => ['preparing', 'uploading', 'checking'].includes(upload.state.value))
const percent = computed(() => Math.round(upload.progress.value * 100))
const uploadStatus = computed(() => {
  switch (upload.state.value) {
    case 'preparing':
      return t('topup.upload_preparing')
    case 'uploading':
      return t('topup.upload_progress', { percent: percent.value })
    case 'checking':
      return t('topup.upload_checking')
    default:
      return ''
  }
})
const uploadError = computed(() => {
  const err = upload.error.value
  if (!err || upload.state.value !== 'error') return ''
  if (err instanceof ReceiptFileError) return t(`topup.upload_errors.${err.code}`)
  const kind = receiptUploadError(err)
  // A reused receipt rejects the top-up: the outcome screen explains it.
  if (kind === 'duplicate' && outcome.value === 'duplicate') return ''
  return kind === 'other' ? errorText(err) : t(`topup.upload_errors.${kind}`)
})

const title = computed(() => (topup.value && !waiting.value ? t('topup.number', { number: topup.value.number }) : t('topup.title')))

const KASPI_KEY = 'jt.driver.kaspi_opened.'

function rememberKaspi(): void {
  kaspiOpened.value = true
  try {
    sessionStorage.setItem(KASPI_KEY + id.value, '1')
  } catch {
    // Storage unavailable: the emphasis simply resets on reload.
  }
}

function readKaspi(): void {
  try {
    kaspiOpened.value = sessionStorage.getItem(KASPI_KEY + id.value) === '1'
  } catch {
    kaspiOpened.value = false
  }
}

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
  const url = payee.value.paymentUrl
  if (!url) return
  haptics.impact('light')
  if (openExternal(url)) rememberKaspi()
  else toast.error(t('trip.open_failed'))
}

function openBot(): void {
  const u = botName.value
  if (u) openTelegram(`https://t.me/${u}`)
}

function pickFile(): void {
  if (uploadBusy.value) return
  haptics.impact('light')
  fileInput.value?.click()
}

async function onFile(e: Event): Promise<void> {
  const el = e.target as HTMLInputElement
  const file = el.files?.[0]
  el.value = ''
  if (!file) return
  paidMode.value = false
  const result = await upload.send(file)
  if (!result) {
    haptics.notification('error')
    return
  }
  const o = receiptOutcome(result)
  haptics.notification(o === 'credited' || o === 'confirmed' ? 'success' : 'warning')
}

async function submitPaid(): Promise<void> {
  if (!topup.value || submitting.value) return
  submitting.value = true
  try {
    await wallet.submitTopUp(id.value, { payment_reference: receiptNumber.value, note: note.value })
    haptics.notification('success')
    paidMode.value = false
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

function finish(): void {
  void router.replace({ name: session.isApproved ? ROUTE.home : ROUTE.wallet })
}

watch(id, () => {
  upload.reset()
  readKaspi()
  void load()
})

onMounted(() => {
  readKaspi()
  void load()
  if (!wallet.wallet) void wallet.refreshWallet()
  void loadPayeeBin().then((bin) => (payeeBin.value = bin))
})
</script>

<template>
  <DriverPage :title="title">
    <div v-if="!topup && loading" class="tu__skeleton" aria-busy="true">
      <JSkeleton height="150px" radius="22px" />
      <JSkeleton height="110px" radius="22px" />
      <JSkeleton height="220px" radius="22px" />
    </div>

    <div v-else-if="!topup" class="tu__state">
      <JEmptyState :title="t('app.load_failed_title')" :description="loadError ? errorText(loadError) : ''">
        <template #action><JButton block @click="load">{{ t('common.retry') }}</JButton></template>
      </JEmptyState>
    </div>

    <!-- Waiting for the payment and the receipt. -->
    <template v-else-if="waiting">
      <section class="tu__amount">
        <p class="tu__amount-label">{{ t('topup.pay_title') }}</p>
        <div class="tu__amount-row">
          <strong class="tu__amount-value jt-tabular">{{ fmt.money(amount) }}</strong>
          <CopyButton :value="String(amount)" :label="t('topup.pay_title')" />
        </div>
        <p class="tu__amount-hint">{{ t('topup.pay_exact') }}</p>
      </section>

      <JListGroup :header="t('topup.payee_title')" :footer="t('topup.payee_via')">
        <JListRow :title="t('topup.payee_name')" :value="payee.name" />
        <JListRow :title="t('topup.payee_bin')" :subtitle="formatBin(payee.bin)">
          <template #accessory><CopyButton class="tu__row-copy" :value="payee.bin" :label="t('topup.payee_bin')" /></template>
        </JListRow>
      </JListGroup>

      <h2 class="tu__section">{{ t('topup.how_title') }}</h2>
      <section class="tu__steps">
        <ol class="tu__list">
          <li :class="{ 'is-done': kaspiOpened }">
            <span class="tu__num">1</span>
            <span class="tu__step">
              <span>{{ t('topup.how_step1', { amount: fmt.money(amount) }) }}</span>
              <span class="tu__hint">{{ payee.paymentUrl ? t('topup.how_step1_hint') : t('topup.no_payment_link') }}</span>
            </span>
          </li>
          <li>
            <span class="tu__num">2</span>
            <span class="tu__step">{{ t('topup.how_step2') }}</span>
          </li>
          <li>
            <span class="tu__num">3</span>
            <span class="tu__step">
				<strong>{{ t('topup.how_step3') }}</strong>
              <span class="tu__hint">{{ t('topup.how_step3_hint') }}</span>
            </span>
          </li>
        </ol>

        <div v-if="uploadBusy" class="tu__drop" role="status">
          <FileText :size="22" :stroke-width="2" aria-hidden="true" />
          <span class="tu__drop-text">
            <span class="tu__drop-title">{{ upload.fileName.value }}</span>
            <span class="tu__drop-sub jt-tabular" aria-live="polite">{{ uploadStatus }}</span>
          </span>
          <span class="tu__bar" :class="{ 'is-indeterminate': upload.state.value !== 'uploading' }">
            <span class="tu__bar-fill" :style="{ transform: `scaleX(${upload.state.value === 'uploading' ? upload.progress.value : 1})` }" />
          </span>
        </div>

        <p v-if="uploadError" class="tu__err" role="alert">
          <span>{{ uploadError }}</span>
          <button v-if="upload.error.value && !(upload.error.value instanceof ReceiptFileError)" type="button" class="tu__retry" @click="upload.retry()">
            <RefreshCw :size="14" :stroke-width="2.5" aria-hidden="true" /> {{ t('common.retry') }}
          </button>
        </p>

        <div v-if="botName" class="tu__bot">
          <p class="tu__hint">{{ t('topup.bot_hint', { bot: `@${botName}` }) }}</p>
          <button type="button" class="tu__link" @click="openBot">
            <Send :size="15" :stroke-width="2.25" aria-hidden="true" /> {{ t('topup.send_to_bot') }}
          </button>
        </div>
      </section>

      <p class="tu__note">{{ t('topup.expires_hint', { time: fmt.dateTime(topup.expires_at) }) }}</p>

      <section v-if="paidMode" class="tu__paid">
        <h2 class="tu__title">{{ t('topup.paid_title') }}</h2>
        <p class="tu__muted">{{ t('topup.paid_text') }}</p>
        <JTextField
          v-model="receiptNumber"
          :label="t('topup.receipt_number')"
          :placeholder="t('topup.receipt_number_placeholder')"
          :max="RECEIPT_REF_MAX"
          enterkeyhint="done"
        />
        <JTextArea v-model="note" :label="t('topup.note')" :placeholder="t('topup.note_placeholder')" :max="NOTE_MAX" :rows="2" />
      </section>

      <div v-else class="tu__more">
        <JButton variant="plain" block @click="paidMode = true">{{ t('topup.no_pdf') }}</JButton>
        <JButton v-if="topup.can_cancel" variant="destructive" block :loading="cancelling" @click="cancel">{{ t('topup.cancel') }}</JButton>
      </div>
    </template>

    <!-- What happened. -->
    <template v-else>
      <section class="tu__status" :class="`is-${outcome}`">
        <CircleCheck v-if="outcome === 'credited' || outcome === 'confirmed'" :size="52" :stroke-width="1.5" class="tu__icon is-ok" aria-hidden="true" />
        <Clock3 v-else-if="outcome === 'review'" :size="52" :stroke-width="1.5" class="tu__icon" aria-hidden="true" />
        <CircleX v-else :size="52" :stroke-width="1.5" class="tu__icon" :class="{ 'is-bad': outcome === 'duplicate' || outcome === 'rejected' }" aria-hidden="true" />

        <template v-if="outcome === 'credited'">
          <h2 class="tu__status-title">{{ t('topup.credited_title') }}</h2>
          <p class="tu__muted">{{ t('topup.credited_text', { amount: fmt.money(topup.verified_amount ?? topup.amount) }) }}</p>
          <p v-if="wallet.wallet" class="tu__balance jt-tabular">{{ t('topup.balance_now', { amount: fmt.money(wallet.wallet.available) }) }}</p>
        </template>
        <template v-else-if="outcome === 'confirmed'">
          <h2 class="tu__status-title">{{ t('topup.confirmed_title') }}</h2>
          <p class="tu__muted">{{ t('topup.confirmed_text', { amount: fmt.money(topup.verified_amount ?? topup.amount) }) }}</p>
          <p v-if="wallet.wallet" class="tu__balance jt-tabular">{{ t('topup.balance_now', { amount: fmt.money(wallet.wallet.available) }) }}</p>
        </template>
        <template v-else-if="outcome === 'review'">
          <h2 class="tu__status-title">{{ t('topup.review_title') }}</h2>
          <p class="tu__muted">{{ t('topup.review_text') }}</p>
          <div v-if="reasons.length > 0" class="tu__reasons">
            <p class="tu__reasons-title">{{ t('topup.review_reasons') }}</p>
            <ul>
              <li v-for="r in reasons" :key="r">{{ t(`topup.reasons.${r}`) }}</li>
            </ul>
          </div>
        </template>
        <template v-else-if="outcome === 'duplicate'">
          <h2 class="tu__status-title">{{ t('topup.duplicate_title') }}</h2>
          <p class="tu__muted">{{ t('topup.duplicate_text') }}</p>
        </template>
        <template v-else-if="outcome === 'rejected'">
          <h2 class="tu__status-title">{{ t('topup.rejected_title') }}</h2>
          <p class="tu__muted">{{ topup.rejected_reason ? t('topup.rejected_text', { reason: topup.rejected_reason }) : t('topup.rejected_no_reason') }}</p>
        </template>
        <template v-else-if="outcome === 'expired'">
          <h2 class="tu__status-title">{{ t('topup.expired_title') }}</h2>
          <p class="tu__muted">{{ t('topup.expired_text') }}</p>
        </template>
        <template v-else>
          <h2 class="tu__status-title">{{ t('topup.refunded_title') }}</h2>
        </template>
      </section>

      <div v-if="outcome === 'review' && topup.can_upload_receipt" class="tu__more">
        <p v-if="topup.receipt?.kind !== 'pdf'" class="tu__note">{{ t('topup.review_upload_hint') }}</p>
        <JButton variant="secondary" block :loading="uploadBusy" @click="pickFile">
          <template #icon><FileText :size="18" :stroke-width="2.25" aria-hidden="true" /></template>
          {{ topup.receipt?.kind === 'pdf' ? t('topup.upload_again') : t('topup.upload_pdf') }}
        </JButton>
        <p v-if="uploadBusy" class="tu__note jt-tabular" aria-live="polite">{{ uploadStatus }}</p>
        <p v-if="uploadError" class="tu__err" role="alert">{{ uploadError }}</p>
      </div>

      <JListGroup>
        <JListRow :title="t('topup.amount')" :value="fmt.money(topup.amount)" />
        <JListRow v-if="topup.verified_amount !== null && topup.verified_amount !== topup.amount" :title="t('topup.verified_amount')" :value="fmt.money(topup.verified_amount)" />
        <JListRow :title="t('topup.reference')" :subtitle="topup.reference" />
        <JListRow v-if="topup.receipt?.receipt_serial" :title="t('topup.receipt_number_label')" :subtitle="topup.receipt.receipt_serial" />
        <JListRow v-if="topup.receipt?.paid_at" :title="t('topup.receipt_paid_at')" :value="fmt.dateTime(topup.receipt.paid_at)" />
        <JListRow v-else-if="topup.driver_payment_reference" :title="t('topup.receipt_number')" :subtitle="topup.driver_payment_reference" />
        <JListRow :title="t('topup.created_at')" :value="fmt.dateTime(topup.created_at)" />
        <JListRow :title="t('topup.status')">
          <template #accessory><JBadge :tone="topUpStatusTone(topup.status)" :label="t(`topup_status.${topup.status}`)" /></template>
        </JListRow>
      </JListGroup>
    </template>

    <input ref="fileInput" class="jt-visually-hidden" type="file" :accept="RECEIPT_ACCEPT" tabindex="-1" @change="onFile" />

    <template v-if="topup" #footer>
      <template v-if="waiting && paidMode">
        <JButton block :loading="submitting" @click="submitPaid">{{ t('topup.submit') }}</JButton>
        <JButton variant="plain" block @click="paidMode = false">{{ t('common.back') }}</JButton>
      </template>
      <template v-else-if="waiting">
        <JButton v-if="payee.paymentUrl" block :variant="kaspiOpened ? 'secondary' : 'primary'" @click="openKaspi">
          <template #icon><ExternalLink :size="18" :stroke-width="2.25" aria-hidden="true" /></template>
          {{ t('topup.open_kaspi') }}
        </JButton>
        <JButton block :variant="kaspiOpened || !payee.paymentUrl ? 'primary' : 'secondary'" :loading="uploadBusy" @click="pickFile">
          <template #icon><FileText :size="18" :stroke-width="2.25" aria-hidden="true" /></template>
          {{ t('topup.upload_pdf') }}
        </JButton>
      </template>
      <template v-else-if="outcome === 'credited' || outcome === 'confirmed'">
        <JButton block @click="finish">{{ t('topup.done') }}</JButton>
      </template>
      <template v-else-if="outcome === 'review'">
        <JButton variant="secondary" block @click="router.replace({ name: ROUTE.wallet })">{{ t('topup.to_wallet') }}</JButton>
      </template>
      <template v-else>
        <JButton block @click="router.replace({ name: ROUTE.topup })">{{ t('topup.new_topup') }}</JButton>
      </template>
    </template>
  </DriverPage>
</template>

<style scoped>
.tu__skeleton {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  padding: 0 var(--jt-side-margin);
}

.tu__state {
  padding: var(--jt-space-8) var(--jt-side-margin);
}

.tu__amount {
  margin: 0 var(--jt-side-margin) var(--jt-space-5);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.tu__amount-label,
.tu__amount-hint {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.tu__amount-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
  margin: 2px 0;
}

.tu__amount-value {
  font: 700 34px/41px var(--jt-font-family);
  letter-spacing: -0.4px;
}

.tu__row-copy {
  margin-right: var(--jt-space-4);
}

.tu__section {
  margin: 0 calc(var(--jt-side-margin) + var(--jt-space-4)) var(--jt-space-2);
  font: var(--jt-font-footnote);
  text-transform: uppercase;
  color: var(--jt-color-label-secondary);
}

.tu__steps,
.tu__paid,
.tu__status {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  margin: 0 var(--jt-side-margin) var(--jt-space-5);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.tu__list {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
}

.tu__list li {
  display: flex;
  gap: var(--jt-space-3);
  font: var(--jt-font-body);
}

.tu__step {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.tu__num {
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

.tu__list li.is-done .tu__num {
  background: var(--jt-color-green);
}

.tu__hint,
.tu__muted,
.tu__note {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.tu__note {
  margin: 0 calc(var(--jt-side-margin) + var(--jt-space-1)) var(--jt-space-4);
  font: var(--jt-font-footnote);
}

.tu__drop {
  position: relative;
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-height: 64px;
  padding: var(--jt-space-3) var(--jt-space-4);
  border-radius: 18px;
  background: var(--jt-color-fill-secondary);
  overflow: hidden;
}

.tu__drop-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.tu__drop-title {
  font: var(--jt-font-headline);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tu__drop-sub {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.tu__bar {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 3px;
  background: var(--jt-color-fill-tertiary);
}

.tu__bar-fill {
  display: block;
  height: 100%;
  background: var(--jt-color-primary);
  transform-origin: left center;
  transition: transform var(--jt-duration-fast) linear;
}

.tu__bar.is-indeterminate .tu__bar-fill {
  animation: tu-pulse 1.2s ease-in-out infinite;
}

@keyframes tu-pulse {
  50% {
    opacity: 0.35;
  }
}

@media (prefers-reduced-motion: reduce) {
  .tu__bar.is-indeterminate .tu__bar-fill {
    animation: none;
  }
}

.tu__err {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-2);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}

.tu__retry {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font: var(--jt-font-footnote);
  font-weight: 600;
  color: var(--jt-color-label);
}

.tu__bot {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--jt-space-1);
  padding-top: var(--jt-space-3);
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.tu__link {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: var(--jt-tap-target);
  font: var(--jt-font-subheadline);
  font-weight: 600;
  color: var(--jt-color-label);
}

.tu__link:active {
  opacity: var(--jt-opacity-pressed);
}

.tu__title {
  font: var(--jt-font-headline);
}

.tu__more {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-1);
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
}

.tu__more .tu__note {
  margin: 0 var(--jt-space-1);
}

.tu__status {
  align-items: center;
  gap: var(--jt-space-2);
  text-align: center;
}

.tu__icon {
  margin-bottom: var(--jt-space-1);
  color: var(--jt-color-label-secondary);
}

.tu__icon.is-ok {
  color: var(--jt-color-green);
}

.tu__icon.is-bad {
  color: var(--jt-color-red);
}

.tu__status-title {
  font: var(--jt-font-title2);
}

.tu__balance {
  margin-top: var(--jt-space-1);
  font: var(--jt-font-headline);
}

.tu__reasons {
  align-self: stretch;
  margin-top: var(--jt-space-2);
  padding: var(--jt-space-3) var(--jt-space-4);
  border-radius: 14px;
  background: var(--jt-color-fill-secondary);
  text-align: left;
}

.tu__reasons-title {
  font: var(--jt-font-footnote);
  font-weight: 600;
  color: var(--jt-color-label-secondary);
}

.tu__reasons ul {
  margin-top: var(--jt-space-1);
  padding-left: 1.1em;
  list-style: disc;
  font: var(--jt-font-subheadline);
}
</style>
