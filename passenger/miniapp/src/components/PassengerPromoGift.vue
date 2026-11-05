<script setup lang="ts">
import { ArrowRight, Gift, X } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { getReferral } from '../api/promotions'
import type { ReferralView } from '../api/types'
import { homeGift } from '../domain/promotions'
import { useErrorText } from '../i18n/core'
import { ROUTE } from '../router/names'
import { usePromotionsStore } from '../stores/promotions'
import { useSessionStore } from '../stores/session'
import { openTelegram } from '../telegram'
import { copyText } from '../utils/clipboard'
import { formatMoney } from '../utils/format'
import ModalPanel from './ModalPanel.vue'
import { JButton, JPromoGift, JSpinner, toast } from './ui'

const { t } = useI18n()
const router = useRouter()
const errors = useErrorText()
const session = useSessionStore()
const promotions = usePromotionsStore()
const offer = computed(() => homeGift(promotions.data, promotions.clock))
const open = ref(false)
const view = shallowRef<ReferralView | null>(null)
const loading = ref(false)
const error = shallowRef<unknown>(null)
let controller: AbortController | null = null
let request = 0
const amount = computed(() => formatMoney(view.value?.reward_amount ?? offer.value?.amount ?? 0, session.language))
const value = computed(() => offer.value?.amount ? `+${formatMoney(offer.value.amount, session.language)}` : t('gift.offers'))
const link = computed(() => view.value?.link ?? view.value?.app_link ?? null)
const available = computed(() => view.value?.state === 'unlocked' && Boolean(view.value.code))

watch(() => session.isReady, (ready) => { if (ready) void promotions.ensureLoaded() }, { immediate: true })
watch(() => promotions.changes, () => { if (open.value) void load() })
watch(offer, (current) => { if (!current || current.kind !== 'invite') close() })
function close(): void { open.value = false; controller?.abort(); request++; loading.value = false }
async function load(): Promise<void> {
  controller?.abort()
  controller = new AbortController()
  const id = ++request
  loading.value = true
  error.value = null
  try { const next = await getReferral(controller.signal); if (id === request) view.value = next }
  catch (err) { if (id === request) error.value = err }
  finally { if (id === request) loading.value = false }
}
function show(): void {
  if (!offer.value) return
  if (offer.value.kind !== 'invite') { void router.push({ name: ROUTE[offer.value.kind] }); return }
  view.value = null
  open.value = true
  void load()
}
function share(): void {
  if (!available.value || !link.value || loading.value) return
  openTelegram(`https://t.me/share/url?url=${encodeURIComponent(link.value)}&text=${encodeURIComponent(t('invite.share_text'))}`)
}
async function copy(): Promise<void> {
  if (!available.value || loading.value) return
  const text = link.value ?? view.value?.code
  if (!text) return
  if (await copyText(text)) toast.success(link.value ? t('invite.link_copied') : t('invite.code_copied'))
  else toast.error(t('invite.copy_failed'))
}
function details(): void { close(); void router.push({ name: ROUTE.invite }) }
onBeforeUnmount(() => { controller?.abort(); request++ })
</script>

<template>
  <div v-if="offer" class="passenger-gift">
    <JPromoGift :label="t('gift.label')" :value="value" :aria-label="`${t('gift.label')} · ${value}`" @click="show" />
    <ModalPanel :open="open" :label="t('gift.invite_title')" @close="close">
      <div class="gift-panel">
        <button type="button" class="gift-panel__close" :aria-label="t('common.close')" @click="close"><X :size="20" /></button>
        <span class="gift-panel__art" aria-hidden="true"><Gift :size="46" :stroke-width="1.5" /></span>
        <p class="gift-panel__eyebrow">{{ t('gift.label') }}</p>
        <h2 class="gift-panel__title">{{ t('gift.invite_title') }}</h2>
        <p class="gift-panel__amount">+{{ amount }}</p>
        <p class="gift-panel__text">{{ t('gift.invite_text') }}</p>
        <p class="gift-panel__note">{{ t('invite.once') }}</p>
        <JSpinner v-if="loading" size="large" />
        <div v-else-if="error" class="gift-panel__error" role="alert"><p>{{ errors(error) }}</p><JButton size="small" variant="secondary" @click="load">{{ t('common.retry') }}</JButton></div>
        <p v-else-if="!available" class="gift-panel__note">{{ t('gift.unavailable') }}</p>
        <p v-else class="gift-panel__code" :aria-label="t('invite.code')">{{ view?.code }}</p>
      </div>
      <template #footer>
        <JButton block class="gift-share" :disabled="loading || !available || !link || Boolean(error)" @click="share">{{ t('invite.share') }}</JButton>
        <JButton block class="gift-copy" variant="secondary" :disabled="loading || !available || Boolean(error)" @click="copy">{{ link ? t('invite.copy_link') : t('invite.copy_code') }}</JButton>
        <button type="button" class="gift-panel__details" @click="details">{{ t('gift.details') }}<ArrowRight :size="16" aria-hidden="true" /></button>
      </template>
    </ModalPanel>
  </div>
</template>

<style scoped>
.passenger-gift { max-width: min(220px, calc(100vw - 112px)); }
.gift-panel { position: relative; display: flex; flex-direction: column; align-items: center; gap: 10px; text-align: center; padding: 8px 8px 0; }
.gift-panel__close { position: absolute; right: 0; top: 0; display: grid; place-items: center; width: 36px; height: 36px; border: 0; border-radius: 50%; background: var(--jt-color-fill-secondary); color: var(--jt-color-label-secondary); }
.gift-panel__art { display: grid; place-items: center; width: 90px; height: 90px; margin: 4px 0 6px; border-radius: 30px; background: linear-gradient(145deg,var(--jt-color-gift-bg),var(--jt-color-gift-accent)); color: var(--jt-color-gift-ink); transform: rotate(-7deg); }
.gift-panel__eyebrow { font: var(--jt-font-caption); color: var(--jt-color-gift-ink); }
.gift-panel__title { max-width: 280px; font: var(--jt-font-title2); text-wrap: balance; }
.gift-panel__amount { font: 750 40px/48px var(--jt-font-family); letter-spacing: -1.5px; }
.gift-panel__text { font: var(--jt-font-subheadline); }
.gift-panel__note { font: var(--jt-font-footnote); color: var(--jt-color-label-secondary); }
.gift-panel__code { margin-top: 4px; padding: 10px 18px; border-radius: 14px; background: var(--jt-color-grouped-bg); font: 600 20px/26px var(--jt-font-family); letter-spacing: 3px; }
.gift-panel__error { display: grid; gap: 8px; justify-items: center; font: var(--jt-font-footnote); color: var(--jt-color-red-text); }
.gift-panel__details { display: flex; justify-content: center; align-items: center; gap: 6px; min-height: 40px; border: 0; background: transparent; font: var(--jt-font-footnote); color: var(--jt-color-label-secondary); }
</style>
