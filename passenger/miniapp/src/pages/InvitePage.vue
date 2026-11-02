<!--
  «Досыңды шақыр» (§23.5) from GET /promotions/referral. Locked: why inviting
  is not open yet (by default: after the first-ride coupon is used). Locked
  for good (no first-ride coupon left to use or to earn, read from GET
  /promotions, e.g. a passenger who rode before the launch): the promotion is
  for new passengers, without the reward text. Open: the campaign text, the
  reward paid once whatever the number of friends, the passenger's code with
  Share (Telegram's share sheet with the invitation link) and Copy, what was
  earned and the invited friends with their status. Re-reads on realtime
  hints. Opened from the profile or a bot button (start parameter invite).
-->
<script setup lang="ts">
import { CircleCheck, Lock, UserPlus } from '@lucide/vue'
import { computed, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getReferral } from '../api/promotions'
import type { ReferralFriend, ReferralView } from '../api/types'
import ScrollPage from '../components/ScrollPage.vue'
import { JButton, JEmptyState, JListGroup, JListRow, JSkeleton, toast } from '../components/ui'
import { inviteOutOfReach } from '../domain/promotions'
import { useErrorText } from '../i18n/core'
import { pickLocalized } from '../services/config'
import { isApiError } from '../services/http'
import { usePromotionsStore } from '../stores/promotions'
import { useSessionStore } from '../stores/session'
import { openTelegram } from '../telegram'
import { copyText } from '../utils/clipboard'
import { formatDate, formatMoney } from '../utils/format'

const { t } = useI18n()
const errorText = useErrorText()
const promotions = usePromotionsStore()
const session = useSessionStore()

const lang = computed(() => session.language)
const view = shallowRef<ReferralView | null>(null)
const loading = ref(true)
const error = shallowRef<unknown>(null)
let request = 0

/**
 * GET /promotions/referral together with the passenger's promotions (loaded
 * once; they tell whether a lock can still open, and the store subscribes to
 * the realtime hints below), so the page never shows a lock first and that it
 * is not for this passenger a moment later.
 */
async function load(): Promise<void> {
  const id = ++request
  try {
    const [v] = await Promise.all([getReferral(), promotions.ensureLoaded()])
    if (id !== request) return
    view.value = v
    error.value = null
  } catch (err) {
    if (id === request) error.value = err
  } finally {
    if (id === request) loading.value = false
  }
}

/** No campaign is running, or the API has no promotions. */
const unavailable = computed(
  () => view.value?.state === 'unavailable' || (!view.value && isApiError(error.value) && error.value.status === 404),
)
/** Locked for good: no first-ride coupon left to use or to earn (§23.14 items 1–2). */
const outOfReach = computed(() => Boolean(view.value && promotions.data && inviteOutOfReach(view.value, promotions.data, promotions.clock)))
const reward = computed(() => formatMoney(view.value?.reward_amount ?? 0, lang.value))
/** The campaign's own text, else the app's. */
const description = computed(() => pickLocalized(view.value?.description, lang.value) || t('invite.lead', { amount: reward.value }))
const lockText = computed(() => t(`invite.lock.${view.value?.lock_reason ?? 'coupon_not_redeemed'}`))
/** The bot link first: a friend who starts the bot also gets its messages. */
const link = computed(() => view.value?.link ?? view.value?.app_link ?? null)

function friendLine(f: ReferralFriend): string {
  const date = formatDate(f.attributed_at, lang.value)
  return `${t(`invite.friend_status.${f.status}`)} · ${date}`
}

/** Telegram's share sheet with the invitation link and a short text. */
function share(): void {
  if (!link.value) return
  openTelegram(`https://t.me/share/url?url=${encodeURIComponent(link.value)}&text=${encodeURIComponent(t('invite.share_text'))}`)
}

async function copy(): Promise<void> {
  const text = link.value ?? view.value?.code
  if (!text) return
  if (await copyText(text)) toast.success(link.value ? t('invite.link_copied') : t('invite.code_copied'))
  else toast.error(t('invite.copy_failed'))
}

onMounted(() => void load())
// A friend arrived or finished a first ride (promotions.updated).
watch(() => promotions.changes, () => void load())
</script>

<template>
  <ScrollPage :title="t('invite.title')">
    <div v-if="!view && loading" class="invite-loading">
      <JSkeleton height="148px" radius="22px" />
      <JSkeleton height="96px" radius="22px" />
    </div>

    <JEmptyState v-else-if="unavailable" class="invite-empty" :title="t('invite.unavailable_title')" :description="t('invite.unavailable_text')">
      <template #icon><UserPlus :size="40" :stroke-width="1.5" /></template>
    </JEmptyState>

    <JEmptyState v-else-if="outOfReach" class="invite-empty" :title="t('invite.ineligible_title')" :description="t('invite.ineligible_text')">
      <template #icon><UserPlus :size="40" :stroke-width="1.5" /></template>
    </JEmptyState>

    <JEmptyState v-else-if="!view" class="invite-empty" :title="t('app.load_failed_title')" :description="errorText(error)">
      <template #action>
        <JButton block @click="load">{{ t('common.retry') }}</JButton>
      </template>
    </JEmptyState>

    <template v-else>
      <section v-if="view.state === 'locked'" class="invite-card is-locked">
        <span class="invite-card__icon" aria-hidden="true"><Lock :size="24" :stroke-width="1.75" /></span>
        <h2 class="invite-card__title">{{ t('invite.locked_title') }}</h2>
        <p class="invite-card__text">{{ lockText }}</p>
        <p class="invite-card__note">{{ description }}</p>
      </section>
      <section v-else class="invite-card">
        <span class="invite-card__icon" aria-hidden="true"><UserPlus :size="24" :stroke-width="1.75" /></span>
        <p class="invite-card__text">{{ description }}</p>
        <p v-if="view.rewarded" class="invite-card__done">
          <CircleCheck :size="18" :stroke-width="2" class="invite-card__done-icon" aria-hidden="true" />
          <span>{{ t('invite.rewarded') }}</span>
        </p>
      </section>

      <JListGroup :footer="`${t('invite.once')} ${t('invite.only_inviter')}`">
        <JListRow :title="t('invite.reward')" :value="reward" />
        <JListRow v-if="view.state === 'unlocked'" :title="t('invite.earned')" :value="formatMoney(view.earned, lang)" />
      </JListGroup>

      <section v-if="view.state === 'unlocked' && view.code" class="invite-code">
        <p class="invite-code__label">{{ t('invite.code') }}</p>
        <p class="invite-code__value">{{ view.code }}</p>
        <JButton v-if="link" block class="invite-share" @click="share">{{ t('invite.share') }}</JButton>
        <JButton block variant="secondary" class="invite-copy" @click="copy">{{ link ? t('invite.copy_link') : t('invite.copy_code') }}</JButton>
      </section>

      <JListGroup v-if="view.state === 'unlocked'" :header="t('invite.friends')" :footer="view.friends.length === 0 ? t('invite.no_friends') : ''">
        <JListRow v-for="(f, i) in view.friends" :key="i" :title="f.name" :subtitle="friendLine(f)" />
      </JListGroup>
    </template>
  </ScrollPage>
</template>

<style scoped>
.invite-loading {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  margin: 0 var(--jt-side-margin);
}

.invite-empty {
  margin-top: var(--jt-space-8);
  padding: 0 var(--jt-side-margin);
}

.invite-card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--jt-space-2);
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.invite-card__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  margin-bottom: var(--jt-space-1);
  border-radius: 50%;
  background: var(--jt-color-fill-secondary);
}

.invite-card__title {
  font: var(--jt-font-headline);
}

.invite-card__text {
  font: var(--jt-font-callout);
}

.invite-card__note {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.invite-card__done {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  font: var(--jt-font-subheadline);
}

/* The reward was paid: success. */
.invite-card__done-icon {
  flex: none;
  color: var(--jt-color-green);
}

.invite-code {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--jt-space-2);
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
  padding: var(--jt-space-5) var(--jt-space-4) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.invite-code__label {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  text-transform: uppercase;
}

.invite-code__value {
  margin-bottom: var(--jt-space-2);
  font: var(--jt-font-title1);
  letter-spacing: 3px;
  font-variant-numeric: tabular-nums;
  user-select: all;
  -webkit-user-select: all;
}
</style>
