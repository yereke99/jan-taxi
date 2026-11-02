<!--
  Coupons and bonuses: find a passenger (masked) and open their marketing
  page, then tabs for coupons, the bonus ledger and the promotions applied to
  orders. The open tab lives in the URL (?tab=); switching tabs clears the
  other tab's filters.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { Tabs } from '../../components'
import BonusLedgerTab from './BonusLedgerTab.vue'
import CouponsTab from './CouponsTab.vue'
import PromotionsTab from './PromotionsTab.vue'
import UserLookup from './UserLookup.vue'

type TabKey = 'coupons' | 'ledger' | 'promotions'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const tabs = computed<Array<{ value: TabKey; label: string }>>(() => [
  { value: 'coupons', label: t('marketing.tab_coupons') },
  { value: 'ledger', label: t('marketing.tab_bonus_ledger') },
  { value: 'promotions', label: t('marketing.tab_promotions') },
])

const tab = computed<TabKey>({
  get: () => tabs.value.find((x) => x.value === route.query.tab)?.value ?? 'coupons',
  set: (v) => void router.replace({ query: v === 'coupons' ? {} : { tab: v } }),
})
</script>

<template>
  <section class="jt-page">
    <UserLookup />
    <Tabs v-model="tab" :tabs="tabs" :aria-label="t('nav.coupons')" />
    <CouponsTab v-if="tab === 'coupons'" />
    <BonusLedgerTab v-else-if="tab === 'ledger'" />
    <PromotionsTab v-else />
  </section>
</template>
