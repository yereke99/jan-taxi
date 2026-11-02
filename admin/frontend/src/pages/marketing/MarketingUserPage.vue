<!--
  One passenger's promotions (masked: name, the last digits of the phone,
  Telegram id): bonus balance, coupons, the bonus ledger, the invite code, who
  invited them and whom they invited, and the promotions on their orders.
  finance.manage may correct the bonus balance.
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { promoUsersApi, type BonusAccount, type ReferralRow } from '../../api/marketing'
import { AppButton, Card, DataTable, DetailHeader, KeyValueList, StateView, StatusBadge, toast, type Column, type KeyValueItem } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import { useAuthStore } from '../../stores/auth'
import BonusAdjustDialog from './BonusAdjustDialog.vue'
import BonusLedgerTable from './BonusLedgerTable.vue'
import CouponsTable from './CouponsTable.vue'
import OrderLink from './OrderLink.vue'
import PersonCell from './PersonCell.vue'
import PromotionsTable from './PromotionsTable.vue'

const props = defineProps<{ id: string }>()

const { t, te } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()
const res = useResource((signal) => promoUsersApi.get(props.id, signal))
void res.reload()

const u = computed(() => res.data.value)
const adjustOpen = ref(false)
const displayName = computed(() => u.value?.user.name?.trim() || t('marketing.user_short', { id: props.id.slice(0, 8) }))

const bonusItems = computed<KeyValueItem[]>(() => {
  const b = u.value?.bonus
  if (!b) return []
  return [
    { key: 'balance', label: t('marketing.bonus_balance'), value: fmt.money(b.balance) },
    { key: 'reserved', label: t('marketing.bonus_reserved'), value: fmt.money(b.reserved) },
    { key: 'available', label: t('marketing.bonus_available'), value: fmt.money(b.available) },
  ]
})

function reasonText(r: ReferralRow): string {
  if (!r.reason) return ''
  return te(`marketing.referral_reason_${r.reason}`, 'kk') ? t(`marketing.referral_reason_${r.reason}`) : r.reason
}

const invitedColumns = computed<Column<ReferralRow>[]>(() => [
  { key: 'attributed_at', label: t('marketing.col_attributed'), width: '140px', format: (v) => fmt.dateTime(v as string) },
  { key: 'invitee', label: t('marketing.col_invitee') },
  { key: 'status', label: t('common.status') },
  { key: 'reason', label: t('common.reason'), value: (r) => reasonText(r) || '—' },
  { key: 'qualifying_order_number', label: t('marketing.col_qualifying_order') },
])

function onAdjusted(bonus: BonusAccount, amount: number): void {
  toast.success(t('marketing.adjusted_ok', { amount: fmt.signedMoney(amount) }))
  if (u.value) res.set({ ...u.value, bonus })
  void res.reload({ silent: true })
}
</script>

<template>
  <section class="jt-page">
    <StateView v-if="!u" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <DetailHeader :title="displayName" :back="{ name: 'coupons' }" :back-label="t('nav.coupons')">
        <template #subtitle>
          <span class="jt-mono">{{ u.user.phone_masked ?? '—' }}</span> · <span class="jt-mono">TG {{ u.user.telegram_user_id }}</span> ·
          {{ t('marketing.user_registered', { date: fmt.date(u.user.created_at) }) }}
        </template>
        <template v-if="auth.can('finance.manage')" #actions>
          <AppButton variant="primary" @click="adjustOpen = true">{{ t('marketing.adjust_bonus') }}</AppButton>
        </template>
      </DetailHeader>

      <div class="jt-grid-2">
        <Card :title="t('marketing.bonus_balance')">
          <KeyValueList :items="bonusItems" />
        </Card>
        <Card :title="t('marketing.referral')">
          <KeyValueList
            :items="[
              { key: 'code', label: t('marketing.referral_code'), value: u.referral_code ?? t('marketing.referral_code_none'), mono: Boolean(u.referral_code) },
              { key: 'by', label: t('marketing.invited_by') },
              { key: 'count', label: t('marketing.invited_friends'), value: fmt.count(u.invited.length) },
            ]"
          >
            <template #value-by>
              <span v-if="u.invited_by" class="by">
                <PersonCell :user="u.invited_by.referrer" />
                <StatusBadge kind="referral" :status="u.invited_by.status" />
              </span>
              <span v-else class="jt-muted">{{ t('marketing.invited_by_none') }}</span>
            </template>
          </KeyValueList>
        </Card>
      </div>

      <Card :title="t('marketing.tab_coupons')" flush>
        <CouponsTable :rows="u.coupons" :show-user="false" @changed="res.reload({ silent: true })" />
      </Card>

      <Card :title="t('marketing.invited_friends')" flush>
        <DataTable :columns="invitedColumns" :rows="u.invited" row-key="id" :empty-text="t('marketing.no_invited')">
          <template #cell-invitee="{ row }">
            <PersonCell :user="row.invitee" />
          </template>
          <template #cell-status="{ row }">
            <StatusBadge kind="referral" :status="row.status" />
          </template>
          <template #cell-qualifying_order_number="{ row }">
            <OrderLink :number="row.qualifying_order_number" />
          </template>
        </DataTable>
      </Card>

      <Card :title="t('marketing.tab_promotions')" flush>
        <PromotionsTable :rows="u.promotions" :show-user="false" @changed="res.reload({ silent: true })" />
      </Card>

      <Card :title="t('marketing.tab_bonus_ledger')" :subtitle="t('marketing.ledger_latest')" flush>
        <BonusLedgerTable :rows="u.ledger" :show-user="false" />
      </Card>

      <BonusAdjustDialog v-if="auth.can('finance.manage')" v-model:open="adjustOpen" :user-id="id" :user-name="displayName" :bonus="u.bonus" @done="onAdjusted" />
    </template>
  </section>
</template>

<style scoped>
.by {
  display: inline-flex;
  align-items: center;
  gap: var(--jt-space-2);
}
</style>
