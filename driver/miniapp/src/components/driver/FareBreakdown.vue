<!--
  The fare and how the passenger pays it. Drivers see the fare only: the
  platform commission is taken from the wallet when the driver accepts the
  order (§22.2) and appears in the wallet's history, never on order screens.
  With a passenger's promotion (§23.6) the fare stays and two rows follow:
  the cash to collect from the passenger and Jan Taxi's part with where it
  is (credited after the ride, credited, taken back). A cancelled order's
  promotion credits nothing and is not shown.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OrderPromotion, PaymentMethod } from '../../api/types'
import { useFormat } from '../../composables/useFormat'
import { JListGroup, JListRow } from '../ui'

const props = withDefaults(
  defineProps<{
    fare: number
    paymentMethod?: PaymentMethod | null
    header?: string
    /** The passenger's coupon or bonus on the order (DriverOrderDetail.promotion). */
    promotion?: OrderPromotion | null
  }>(),
  { paymentMethod: null, header: '', promotion: null },
)

const { t } = useI18n()
const fmt = useFormat()

const promo = computed(() => {
  const p = props.promotion
  return p && p.discount > 0 && p.status !== 'released' ? p : null
})
</script>

<template>
  <JListGroup :header="header">
    <JListRow :title="t('order.fare')">
      <template #accessory><span class="fare__value jt-tabular" :class="{ 'is-plain': promo }">{{ fmt.money(fare) }}</span></template>
    </JListRow>
    <template v-if="promo">
      <JListRow :title="t('promo.cash_due')">
        <template #accessory><span class="fare__value jt-tabular">{{ fmt.money(promo.cash_due) }}</span></template>
      </JListRow>
      <JListRow :title="t('promo.title')" :subtitle="t(`promo.status.${promo.status}`)">
        <template #accessory><span class="fare__promo jt-tabular" :class="`is-${promo.status}`">{{ fmt.signedMoney(promo.discount) }}</span></template>
      </JListRow>
    </template>
    <JListRow v-if="paymentMethod" :title="t('order.payment')" :value="t(`payment_method.${paymentMethod}`)" />
  </JListGroup>
</template>

<style scoped>
.fare__value {
  font: var(--jt-font-headline);
}

/* With a promotion the cash to collect carries the weight; the fare reads as plain text. */
.fare__value.is-plain {
  font: var(--jt-font-body);
}

/* Jan Taxi's part is money coming in to the driver; one taken back is struck through. */
.fare__promo {
  font: var(--jt-font-headline);
  color: var(--jt-color-green-text);
}

.fare__promo.is-reversed {
  color: var(--jt-color-label-secondary);
  text-decoration: line-through;
}
</style>
