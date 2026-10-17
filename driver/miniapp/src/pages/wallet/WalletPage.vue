<!--
  Wallet (GET /driver/wallet): available / reserved / balance, the minimum to
  go online, Kaspi top-ups with their statuses and the journal. The balance
  changes only through the journal (top-ups after finance verification,
  commissions on completed trips); realtime wallet.updated / topup.* refresh it.
-->
<script setup lang="ts">
import { ChevronLeft } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import DriverPage from '../../components/driver/DriverPage.vue'
import LedgerRow from '../../components/driver/LedgerRow.vue'
import TopUpRow from '../../components/driver/TopUpRow.vue'
import { JButton, JEmptyState, JListGroup, JSegmented, JSkeleton, JSpinner, type SegmentedOption } from '../../components/ui'
import { useFormat } from '../../composables/useFormat'
import { usePullToRefresh } from '../../composables/usePullToRefresh'
import { useErrorText } from '../../i18n/core'
import { ROUTE } from '../../router/names'
import { useSessionStore } from '../../stores/session'
import { useWalletStore } from '../../stores/wallet'

type Tab = 'transactions' | 'topups'

const { t } = useI18n()
const fmt = useFormat()
const errorText = useErrorText()
const router = useRouter()
const wallet = useWalletStore()
const session = useSessionStore()

const page = ref<InstanceType<typeof DriverPage> | null>(null)
const tab = ref<Tab>('transactions')
const tabs = computed<SegmentedOption<Tab>[]>(() => [
  { value: 'transactions', label: t('wallet.transactions') },
  { value: 'topups', label: t('wallet.topups') },
])

const w = computed(() => wallet.wallet)
const openTopUps = computed(() => wallet.topups.items.filter((x) => x.status === 'created' || x.status === 'pending_verification'))
const belowMin = computed(() => w.value !== null && w.value.available < w.value.min_online_balance)
const canTopUp = computed(() => session.driverState !== 'rejected')

const { pull, refreshing } = usePullToRefresh(
  computed(() => page.value?.scrollEl ?? null),
  () => Promise.all([wallet.refreshWallet(), wallet.loadLedger(true), wallet.loadTopUps(true)]),
)

function back(): void {
  const pos = (window.history.state as { position?: number } | null)?.position ?? 0
  if (pos > 0) router.back()
  else void router.replace({ name: ROUTE.home })
}

onMounted(() => {
  void wallet.refreshWallet()
  void wallet.loadLedger(true)
  void wallet.loadTopUps(true)
})
</script>

<template>
  <DriverPage ref="page" :title="t('wallet.title')" :tab-bar="session.isApproved">
    <template v-if="!session.isApproved" #leading>
      <button type="button" class="wallet__back" :aria-label="t('common.back')" @click="back">
        <ChevronLeft :size="26" :stroke-width="2.25" aria-hidden="true" />
      </button>
    </template>

    <template #top>
      <div class="ptr" :style="{ height: `${pull}px` }" aria-hidden="true"><JSpinner v-if="refreshing" size="small" /></div>
    </template>

    <section class="bal">
      <template v-if="w">
        <p class="bal__label">{{ t('wallet.available') }}</p>
        <p class="bal__value jt-tabular">{{ fmt.money(w.available) }}</p>
        <div class="bal__grid">
          <div>
            <p class="bal__label">{{ t('wallet.balance') }}</p>
            <p class="bal__sub jt-tabular">{{ fmt.money(w.balance) }}</p>
          </div>
          <div>
            <p class="bal__label">{{ t('wallet.reserved') }}</p>
            <p class="bal__sub jt-tabular">{{ fmt.money(w.reserved) }}</p>
          </div>
        </div>
        <p class="bal__hint" :class="{ 'is-warn': belowMin }">{{ t('wallet.min_online', { amount: fmt.money(w.min_online_balance) }) }}</p>
      </template>
      <template v-else-if="wallet.walletError">
        <p class="bal__hint is-error" role="alert">{{ errorText(wallet.walletError) }}</p>
        <JButton variant="secondary" size="medium" @click="wallet.refreshWallet()">{{ t('common.retry') }}</JButton>
      </template>
      <template v-else>
        <JSkeleton width="40%" height="16px" />
        <JSkeleton width="60%" height="34px" />
      </template>
      <JButton v-if="canTopUp" block @click="router.push({ name: ROUTE.topup })">{{ t('wallet.top_up') }}</JButton>
    </section>
    <p class="wallet__explainer">{{ t('wallet.explainer') }}</p>

    <JListGroup v-if="openTopUps.length > 0" :header="t('wallet.open_topups')">
      <TopUpRow v-for="x in openTopUps" :key="x.id" :topup="x" />
    </JListGroup>

    <div class="wallet__tabs">
      <JSegmented v-model="tab" :options="tabs" :aria-label="t('wallet.title')" />
    </div>

    <template v-if="tab === 'transactions'">
      <div v-if="!wallet.ledger.loaded && wallet.ledger.loading" class="wallet__skeleton"><JSkeleton height="180px" radius="22px" /></div>
      <div v-else-if="wallet.ledger.loaded && wallet.ledger.items.length === 0" class="wallet__empty">
        <JEmptyState :title="t('wallet.no_transactions')" />
      </div>
      <JListGroup v-else-if="wallet.ledger.items.length > 0">
        <LedgerRow v-for="e in wallet.ledger.items" :key="e.id" :entry="e" />
      </JListGroup>
      <p v-if="wallet.ledger.error" class="wallet__error" role="alert">{{ errorText(wallet.ledger.error) }}</p>
      <div v-if="wallet.ledger.next" class="wallet__more">
        <JButton variant="secondary" block :loading="wallet.ledger.loading" @click="wallet.loadLedger(false)">{{ t('common.loading_more') }}</JButton>
      </div>
    </template>

    <template v-else>
      <div v-if="!wallet.topups.loaded && wallet.topups.loading" class="wallet__skeleton"><JSkeleton height="180px" radius="22px" /></div>
      <div v-else-if="wallet.topups.loaded && wallet.topups.items.length === 0" class="wallet__empty">
        <JEmptyState :title="t('wallet.no_topups')" />
      </div>
      <JListGroup v-else-if="wallet.topups.items.length > 0">
        <TopUpRow v-for="x in wallet.topups.items" :key="x.id" :topup="x" />
      </JListGroup>
      <p v-if="wallet.topups.error" class="wallet__error" role="alert">{{ errorText(wallet.topups.error) }}</p>
      <div v-if="wallet.topups.next" class="wallet__more">
        <JButton variant="secondary" block :loading="wallet.topups.loading" @click="wallet.loadTopUps(false)">{{ t('common.loading_more') }}</JButton>
      </div>
    </template>
  </DriverPage>
</template>

<style scoped>
.wallet__back {
  display: flex;
  align-items: center;
  justify-content: center;
  width: var(--jt-tap-target);
  height: var(--jt-tap-target);
  margin-left: -12px;
  color: var(--jt-color-label);
}

.ptr {
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  transition: height var(--jt-duration-fast) var(--jt-ease);
}

.bal {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  margin: 0 var(--jt-side-margin) var(--jt-space-2);
  padding: var(--jt-space-5) var(--jt-space-4) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.bal__label {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.bal__value {
  font: var(--jt-font-large-title);
  letter-spacing: var(--jt-tracking-large-title);
}

.bal__grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--jt-space-3);
  padding: var(--jt-space-2) 0;
}

.bal__sub {
  font: var(--jt-font-headline);
}

.bal__hint {
  margin-bottom: var(--jt-space-2);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.bal__hint.is-warn {
  color: var(--jt-color-label);
}

.bal__hint.is-error {
  color: var(--jt-color-red-text);
}

.wallet__explainer {
  margin: 0 calc(var(--jt-side-margin) + var(--jt-space-4)) var(--jt-space-6);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.wallet__tabs {
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
}

.wallet__skeleton,
.wallet__more {
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
}

.wallet__empty {
  padding: var(--jt-space-6) var(--jt-side-margin);
}

.wallet__error {
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}
</style>
