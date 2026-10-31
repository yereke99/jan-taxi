<!--
  The fare and how the passenger pays it. Drivers see the fare only: the
  platform commission is taken from the wallet when the driver accepts the
  order (§22.2) and appears in the wallet's history, never on order screens.
-->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PaymentMethod } from '../../api/types'
import { useFormat } from '../../composables/useFormat'
import { JListGroup, JListRow } from '../ui'

withDefaults(
  defineProps<{
    fare: number
    paymentMethod?: PaymentMethod | null
    header?: string
  }>(),
  { paymentMethod: null, header: '' },
)

const { t } = useI18n()
const fmt = useFormat()
</script>

<template>
  <JListGroup :header="header">
    <JListRow :title="t('order.fare')">
      <template #accessory><span class="fare__value jt-tabular">{{ fmt.money(fare) }}</span></template>
    </JListRow>
    <JListRow v-if="paymentMethod" :title="t('order.payment')" :value="t(`payment_method.${paymentMethod}`)" />
  </JListGroup>
</template>

<style scoped>
.fare__value {
  font: var(--jt-font-headline);
}
</style>
