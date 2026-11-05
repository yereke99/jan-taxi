<script setup lang="ts">
import { Gift } from '@lucide/vue'
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import DriverPage from '../components/driver/DriverPage.vue'
import { JButton, JEmptyState, JSpinner } from '../components/ui'
import { useFormat } from '../composables/useFormat'
import { useErrorText } from '../i18n/core'
import { ROUTE } from '../router/names'
import { pickLocalized } from '../services/config'
import { useDriverPromotionsStore } from '../stores/promotions'
import { useSessionStore } from '../stores/session'

const { t } = useI18n()
const router = useRouter()
const fmt = useFormat()
const errors = useErrorText()
const session = useSessionStore()
const promotions = useDriverPromotionsStore()
onMounted(() => void promotions.refresh())
</script>

<template>
  <DriverPage :title="t('promotions.title')">
    <div v-if="promotions.loading && !promotions.loaded" class="promotions-loading"><JSpinner size="large" /></div>
    <JEmptyState v-else-if="promotions.error" :title="t('app.load_failed_title')" :description="errors(promotions.error)">
      <template #action><JButton @click="promotions.refresh">{{ t('common.retry') }}</JButton></template>
    </JEmptyState>
    <JEmptyState v-else-if="!promotions.offers.length" :title="t('promotions.empty_title')" :description="t('promotions.empty_text')">
      <template #icon><Gift :size="40" :stroke-width="1.5" /></template>
    </JEmptyState>
    <div v-else class="promotions-list">
      <article v-for="offer in promotions.offers" :key="offer.id" class="promotion-card">
        <div class="promotion-card__head"><span class="promotion-card__icon" aria-hidden="true"><Gift :size="28" :stroke-width="1.6" /></span><p class="promotion-card__reward">+{{ fmt.money(offer.amount) }}</p></div>
        <h2>{{ pickLocalized(offer.title, session.language) || t('promotions.title') }}</h2>
        <p class="promotion-card__text">{{ pickLocalized(offer.description, session.language) || t('promotions.conditions', { n: offer.rides }) }}</p>
        <p v-if="offer.rewarded" class="promotion-card__credited">{{ t('promotions.rewarded') }}</p>
        <template v-else>
          <div class="promotion-card__progress" role="progressbar" :aria-label="t('promotions.progress')" :aria-valuenow="Math.min(offer.completed, offer.rides)" :aria-valuemin="0" :aria-valuemax="offer.rides"><span :style="{ width: `${Math.min(100, offer.completed / offer.rides * 100)}%` }" /></div>
          <p class="promotion-card__count">{{ t('promotions.completed', { done: Math.min(offer.completed, offer.rides), total: offer.rides }) }}</p>
          <p v-if="offer.completed >= offer.rides" class="promotion-card__text">{{ t('promotions.processing') }}</p>
        </template>
        <p v-if="offer.ends_at" class="promotion-card__until">{{ t('promotions.until', { date: fmt.date(offer.ends_at) }) }}</p>
      </article>
      <JButton block variant="secondary" @click="router.push({ name: ROUTE.wallet })">{{ t('promotions.wallet') }}</JButton>
    </div>
  </DriverPage>
</template>

<style scoped>
.promotions-loading { display: grid; place-items: center; padding: 40px; }
.promotions-list { display: grid; gap: 16px; margin: 0 var(--jt-side-margin) var(--jt-space-5); }
.promotion-card { padding: 22px; border-radius: var(--jt-radius-card); background: linear-gradient(145deg,var(--jt-color-bg),var(--jt-color-gift-bg)); }
.promotion-card__head { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 18px; }
.promotion-card__icon { display: grid; place-items: center; width: 54px; height: 54px; border-radius: 18px; background: var(--jt-color-gift-accent); color: var(--jt-color-gift-ink); }
.promotion-card__reward { font: var(--jt-font-title1); letter-spacing: -0.8px; }
.promotion-card h2 { margin-bottom: 8px; font: var(--jt-font-title3); }
.promotion-card__text { font: var(--jt-font-subheadline); color: var(--jt-color-label-secondary); }
.promotion-card__progress { height: 7px; overflow: hidden; margin-top: 22px; border-radius: 20px; background: var(--jt-color-fill-tertiary); }
.promotion-card__progress span { display: block; height: 100%; border-radius: inherit; background: var(--jt-color-gift-ink); }
.promotion-card__count { margin-top: 8px; font: var(--jt-font-footnote); }
.promotion-card__until { margin-top: 20px; font: var(--jt-font-caption); color: var(--jt-color-label-secondary); }
.promotion-card__credited { margin-top: 16px; font: var(--jt-font-headline); color: var(--jt-color-green-text); }
</style>
