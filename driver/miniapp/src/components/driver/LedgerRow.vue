<!--
  One wallet journal entry: localized type, reference (order / top-up),
  time and the signed amount — green when money comes in, red when it goes
  out; reservations (orders accepted before the v2 rules) only change the
  reserved part and are shown in gray. A bonus from Jan Taxi (welcome bonus
  or one from an administrator, with the administrator's comment) carries a
  gift mark.
-->
<script setup lang="ts">
import { Gift } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LedgerEntry } from '../../api/types'
import { useFormat } from '../../composables/useFormat'
import { bonusKind } from '../../features/wallet/balance'

const props = defineProps<{ entry: LedgerEntry }>()

const { t, te } = useI18n()
const fmt = useFormat()

const reserveOnly = computed(() => props.entry.balance_delta === 0 && props.entry.reserved_delta !== 0)
const amount = computed(() => (reserveOnly.value ? props.entry.reserved_delta : props.entry.balance_delta))
const tone = computed(() => (reserveOnly.value ? 'neutral' : amount.value > 0 ? 'in' : amount.value < 0 ? 'out' : 'neutral'))
const bonus = computed(() => bonusKind(props.entry))
const title = computed(() => {
  if (bonus.value === 'welcome') return t('wallet.welcome_bonus')
  const key = `wallet.entry.${props.entry.entry_type}`
  return te(key) ? t(key) : props.entry.entry_type
})
const reference = computed(() => {
  const e = props.entry
  if (bonus.value === 'welcome') return t('wallet.from_company')
  if (bonus.value === 'manual') return e.description ? `${t('wallet.from_company')} · «${e.description}»` : t('wallet.from_company')
  if (e.order_number !== null) return t('wallet.order_ref', { number: e.order_number })
  if (e.topup_reference) return e.topup_reference
  return e.description ?? ''
})
</script>

<template>
  <div class="lrow" :class="{ 'is-bonus': bonus }" role="listitem">
    <span v-if="bonus" class="lrow__gift" aria-hidden="true"><Gift :size="18" :stroke-width="2" /></span>
    <div class="lrow__text">
      <p class="lrow__title">{{ title }}</p>
      <p class="lrow__sub">
        <span v-if="reference">{{ reference }} · </span>{{ fmt.dateTime(entry.created_at) }}
      </p>
    </div>
    <div class="lrow__amount jt-tabular" :class="`is-${tone}`">
      <span>{{ reserveOnly ? fmt.money(Math.abs(amount)) : fmt.signedMoney(amount) }}</span>
      <span v-if="reserveOnly" class="lrow__note">{{ t('wallet.reserve_note') }}</span>
      <span v-else class="lrow__note">{{ fmt.money(entry.balance_after) }}</span>
    </div>
  </div>
</template>

<style scoped>
.lrow {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-height: var(--jt-row-min-height);
  padding: 11px var(--jt-space-4);
  background: var(--jt-color-bg);
}

.lrow:not(:last-child) {
  box-shadow: inset 0 calc(-1 * var(--jt-hairline)) 0 var(--jt-color-separator);
}

.lrow__gift {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  background: var(--jt-color-green-tint);
  color: var(--jt-color-green-text);
}

.lrow__text {
  flex: 1;
  min-width: 0;
}

.lrow__title {
  font: var(--jt-font-body);
}

.lrow__sub {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.lrow__amount {
  flex: none;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  font: var(--jt-font-headline);
}

.lrow__amount.is-in {
  color: var(--jt-color-green-text);
}

.lrow__amount.is-out {
  color: var(--jt-color-red-text);
}

.lrow__amount.is-neutral {
  color: var(--jt-color-label-secondary);
}

.lrow__note {
  font: var(--jt-font-caption);
  font-weight: 400;
  color: var(--jt-color-label-secondary);
}
</style>
