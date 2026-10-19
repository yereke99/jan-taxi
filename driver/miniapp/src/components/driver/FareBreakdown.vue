<!--
  Fare, the platform commission and what the driver keeps. The passenger
  pays the full fare to the driver; the commission is taken from the wallet
  when the driver accepts the order (§22.2), so on the driver's own order it
  has already been charged (`charged`).
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PaymentMethod } from '../../api/types'
import { useFormat } from '../../composables/useFormat'
import { JListGroup, JListRow } from '../ui'

const props = withDefaults(
  defineProps<{
    fare: number
    commission: number
    paymentMethod?: PaymentMethod | null
    header?: string
    /** Only the commission and the net earning (order preview before accepting). */
    compact?: boolean
    /** The commission was already taken from the wallet (the driver's own order). */
    charged?: boolean
  }>(),
  { paymentMethod: null, header: '', compact: false, charged: false },
)

const { t } = useI18n()
const fmt = useFormat()

const footer = computed(() => {
  if (props.compact) return undefined
  return props.charged ? t('order.commission_charged_note') : t('order.commission_note')
})
</script>

<template>
  <JListGroup :header="header" :footer="footer">
    <JListRow v-if="!compact" :title="t('order.fare')">
      <template #accessory><span class="fare__value jt-tabular">{{ fmt.money(fare) }}</span></template>
    </JListRow>
    <JListRow v-if="paymentMethod && !compact" :title="t('order.payment')" :value="t(`payment_method.${paymentMethod}`)" />
    <JListRow :title="t('history.commission')">
      <template #accessory><span class="fare__minus jt-tabular">{{ compact ? '' : '−' }}{{ fmt.money(commission) }}</span></template>
    </JListRow>
    <JListRow :title="t('order.net')">
      <template #accessory><span class="fare__net jt-tabular">{{ fmt.money(fare - commission) }}</span></template>
    </JListRow>
  </JListGroup>
</template>

<style scoped>
.fare__value {
  font: var(--jt-font-headline);
}

.fare__minus {
  color: var(--jt-color-label-secondary);
}

.fare__net {
  font: var(--jt-font-headline);
  color: var(--jt-color-green-text);
}
</style>
