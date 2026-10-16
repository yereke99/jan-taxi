<!--
  Fare, the platform commission (charged from the wallet on completion) and
  what the driver keeps. The passenger pays the full fare to the driver.
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
    commissionBps?: number | null
    paymentMethod?: PaymentMethod | null
    header?: string
  }>(),
  { commissionBps: null, paymentMethod: null, header: '' },
)

const { t } = useI18n()
const fmt = useFormat()

const commissionLabel = computed(() =>
  props.commissionBps !== null ? t('order.commission', { percent: fmt.percent(props.commissionBps) }) : t('history.commission'),
)
</script>

<template>
  <JListGroup :header="header" :footer="t('order.commission_note')">
    <JListRow :title="t('order.fare')">
      <template #accessory><span class="fare__value jt-tabular">{{ fmt.money(fare) }}</span></template>
    </JListRow>
    <JListRow v-if="paymentMethod" :title="t('order.payment')" :value="t(`payment_method.${paymentMethod}`)" />
    <JListRow :title="commissionLabel">
      <template #accessory><span class="fare__minus jt-tabular">−{{ fmt.money(commission) }}</span></template>
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
