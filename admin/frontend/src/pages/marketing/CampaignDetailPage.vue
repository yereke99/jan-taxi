<!--
  Campaign detail: rules in plain words, copy in three languages, budget and
  usage, the forecast (audience, maximum cost, warnings), the version history
  and the actions its status and the operator's permissions allow. Approver
  actions ask for a reason and are sent with confirm: true and the version on
  screen (a newer one closes the dialog and reloads the page); the launch
  campaigns also get their on/off switch here.
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { campaignsApi, type CampaignAction, type CampaignDetail, type CampaignTransitionBody } from '../../api/marketing'
import { AppButton, Card, DetailHeader, KeyValueList, ReasonDialog, StateView, StatusBadge, confirmAction, toast, type KeyValueItem } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import { useAuthStore } from '../../stores/auth'
import ActionDialog from './ActionDialog.vue'
import BudgetDialog from './BudgetDialog.vue'
import CampaignForm from './CampaignForm.vue'
import {
  ACTION_RULES,
  availableActions,
  canDuplicate,
  canEditCampaign,
  hasMoney,
  isLaunchCampaign,
  isVersionConflict,
  rulesSummary,
  transitionBody,
  transitionErrorText,
} from './campaignModel'
import CampaignSwitch from './CampaignSwitch.vue'
import UsageBar from './UsageBar.vue'

const props = defineProps<{ id: string }>()

const { t, te } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const router = useRouter()
const auth = useAuthStore()

const res = useResource((signal) => campaignsApi.get(props.id, signal))
const preview = useResource((signal) => campaignsApi.preview(props.id, signal))
void res.reload()
void preview.reload()

const c = computed(() => res.data.value)
const words = computed(() => ({ t, money: (n: number) => fmt.money(n) }))
const summary = computed(() => (c.value ? rulesSummary(c.value.template, c.value.rules, words.value) : ''))
const actions = computed(() => (c.value ? availableActions(c.value, auth.can) : []))
const canEdit = computed(() => !!c.value && canEditCampaign(c.value, auth.can))
const canCopy = computed(() => !!c.value && canDuplicate(c.value, auth.can))
const canBudget = computed(() => !!c.value && hasMoney(c.value.template) && auth.can('marketing.approve'))
const launch = computed(() => !!c.value && isLaunchCampaign(c.value))
/** Actions the operator could take with marketing.approve but may not. */
const lockedApproverActions = computed(() => !!c.value && !auth.can('marketing.approve') && c.value.status !== 'archived' && c.value.template !== 'manual_grant')

const editOpen = ref(false)
const budgetOpen = ref(false)
const pending = ref<CampaignAction | null>(null)
const reasonOpen = computed({
  get: () => pending.value !== null && pending.value !== 'schedule',
  set: (v) => {
    if (!v) pending.value = null
  },
})
const scheduleOpen = computed({
  get: () => pending.value === 'schedule',
  set: (v) => {
    if (!v) pending.value = null
  },
})

const DESTRUCTIVE: readonly CampaignAction[] = ['pause', 'end', 'archive', 'freeze_redemptions']

function budgetText(d: CampaignDetail): string {
  if (d.budget_limit === null) return t('marketing.budget_unlimited')
  const used = (d.usage?.committed ?? 0) + (d.usage?.spent ?? 0)
  return t('marketing.budget_used', { used: fmt.money(used), limit: fmt.money(d.budget_limit) })
}

/** What the action does, in plain words, plus who answers for it. */
function actionMessage(action: CampaignAction): string {
  const d = c.value
  if (!d) return ''
  const effectKey = `marketing.effect_${d.template}`
  const effect = te(effectKey, 'kk') ? t(effectKey) : ''
  let text: string
  if (action === 'activate') {
    text = t('marketing.switch_on_message', { effect, budget: budgetText(d) })
    if (d.status === 'scheduled') text = `${text}\n${t('marketing.switch_now_not_scheduled')}`
  } else if (action === 'resume') text = t('marketing.switch_resume_message', { effect, budget: budgetText(d) })
  else if (action === 'pause') text = t('marketing.switch_off_message')
  else text = t(`marketing.action_msg_${action}`)
  return ACTION_RULES[action].approve ? `${text}\n${t('marketing.approver_note')}` : text
}

const dialogTitle = computed(() => (pending.value && c.value ? t('marketing.action_title', { action: t(`marketing.action_${pending.value}`), title: fmt.name(c.value.title) }) : ''))

function apply(updated: CampaignDetail): void {
  res.set(updated)
  void preview.reload({ silent: true })
}

/** Loads the campaign again, e.g. after a newer version was saved elsewhere. */
function refresh(): void {
  void res.reload({ silent: true })
  void preview.reload({ silent: true })
}

function onBudgetSaved(updated: CampaignDetail): void {
  apply(updated)
  toast.success(t('marketing.budget_saved'))
}

/**
 * Sends an action with the version on screen; false when the server refused
 * it because a newer version was saved meanwhile (409 version_conflict): the
 * dialog then closes and the page reloads, so the approver reads the new
 * rules before trying again. Other errors stay in the dialog.
 */
async function transition(body: CampaignTransitionBody): Promise<boolean> {
  const d = c.value
  if (!d) return false
  try {
    apply(await campaignsApi.transition(props.id, transitionBody(d, body)))
    return true
  } catch (err) {
    if (!isVersionConflict(err)) throw err
    errors.report(err)
    refresh()
    return false
  }
}

async function runAction(reason: string): Promise<void> {
  const action = pending.value
  if (action && (await transition({ action, reason, confirm: true }))) toast.success(t('marketing.campaign_updated'))
}

async function runSchedule(input: { at: string | null; reason: string }): Promise<void> {
  if (!input.at) return
  if (await transition({ action: 'schedule', scheduled_at: input.at, reason: input.reason, confirm: true })) {
    toast.success(t('marketing.scheduled_ok', { time: fmt.dateTime(input.at) }))
  }
}

async function duplicate(): Promise<void> {
  if (!c.value) return
  const ok = await confirmAction({ title: t('marketing.duplicate'), message: t('marketing.duplicate_message'), confirmLabel: t('marketing.duplicate') })
  if (!ok) return
  try {
    const copy = await campaignsApi.duplicate(props.id)
    toast.success(t('marketing.duplicated'))
    await router.push({ name: 'campaign-detail', params: { id: copy.id } })
  } catch (err) {
    errors.report(err)
  }
}

const ruleItems = computed<KeyValueItem[]>(() => {
  const d = c.value
  if (!d) return []
  const window =
    d.starts_at || d.ends_at
      ? t('marketing.window_range', { from: d.starts_at ? fmt.dateTime(d.starts_at) : '…', to: d.ends_at ? fmt.dateTime(d.ends_at) : '…' })
      : t('marketing.window_open')
  return [
    { key: 'template', label: t('marketing.template'), value: t(`marketing.template_${d.template}`) },
    { key: 'window', label: t('marketing.window'), value: window },
    { key: 'priority', label: t('marketing.priority'), value: d.priority },
    { key: 'group', label: t('marketing.exclusivity_group'), value: d.exclusivity_group, mono: true },
    { key: 'key', label: t('marketing.key'), value: d.key, mono: true },
    { key: 'version', label: t('marketing.version'), value: t('marketing.version_n', { n: d.version }) },
  ]
})

const usageItems = computed<KeyValueItem[]>(() => {
  const d = c.value
  if (!d) return []
  const u = d.usage
  const coupons = Object.entries(u?.coupons ?? {})
    .filter(([, n]) => (n ?? 0) > 0)
    .map(([s, n]) => `${te(`coupon_status.${s}`, 'kk') ? t(`coupon_status.${s}`) : s} ${fmt.count(n ?? 0)}`)
    .join(' · ')
  const grants = Object.entries(u?.grants ?? {})
    .filter(([, n]) => n > 0)
    .map(([s, n]) => `${te(`marketing.grant_status_${s}`, 'kk') ? t(`marketing.grant_status_${s}`) : s} ${fmt.count(n)}`)
    .join(' · ')
  return [
    { key: 'budget', label: t('marketing.budget'), value: d.budget_limit === null ? t('marketing.budget_unlimited') : fmt.money(d.budget_limit) },
    { key: 'committed', label: t('marketing.usage_committed'), value: fmt.money(u?.committed ?? 0) },
    { key: 'spent', label: t('marketing.usage_spent'), value: fmt.money(u?.spent ?? 0) },
    { key: 'remaining', label: t('marketing.usage_remaining'), value: u?.remaining === null || u?.remaining === undefined ? t('marketing.budget_unlimited') : fmt.money(u.remaining) },
    ...(coupons ? [{ key: 'coupons', label: t('marketing.usage_coupons'), value: coupons }] : []),
    ...(grants ? [{ key: 'grants', label: t('marketing.usage_grants'), value: grants }] : []),
  ]
})

const previewItems = computed<KeyValueItem[]>(() => {
  const p = preview.data.value
  if (!p) return []
  return [
    { key: 'audience', label: t('marketing.preview_audience'), value: fmt.count(p.audience) },
    { key: 'per_person', label: t('marketing.preview_per_person'), value: p.per_person > 0 ? fmt.money(p.per_person) : '—' },
    { key: 'max', label: t('marketing.preview_max_liability'), value: p.max_liability === null ? '—' : fmt.money(p.max_liability) },
  ]
})

function warningText(w: string): string {
  return te(`marketing.warning_${w}`, 'kk') ? t(`marketing.warning_${w}`) : w
}

const timeItems = computed<KeyValueItem[]>(() => {
  const d = c.value
  if (!d) return []
  const rows: Array<[string, string | null]> = [
    ['created_at', d.created_at],
    ['scheduled_at', d.scheduled_at],
    ['activated_at', d.activated_at],
    ['active_since', d.active_since],
    ['paused_at', d.paused_at],
    ['ended_at', d.ended_at],
    ['archived_at', d.archived_at],
  ]
  return rows.filter(([, v]) => v).map(([k, v]) => ({ key: k, label: t(`marketing.time_${k}`), value: fmt.dateTime(v) }))
})

watch(
  () => props.id,
  () => {
    void res.reload()
    void preview.reload()
  },
)
</script>

<template>
  <section class="jt-page">
    <StateView v-if="!c" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <DetailHeader :title="fmt.name(c.title)" :back="{ name: 'campaigns' }" :back-label="t('nav.campaigns')">
        <template #badges>
          <StatusBadge kind="campaign" :status="c.status" />
          <span v-if="launch" class="tag tag--dark">{{ t('marketing.launch') }}</span>
          <span v-else-if="c.is_system" class="tag">{{ t('marketing.system') }}</span>
          <span v-if="c.redemptions_frozen" class="tag tag--danger">{{ t('marketing.frozen') }}</span>
        </template>
        <template #subtitle>
          {{ t(`marketing.template_${c.template}`) }} · <span class="jt-mono">{{ c.key }}</span> · {{ t('marketing.version_n', { n: c.version }) }}
        </template>
        <template #actions>
          <AppButton v-if="canEdit" @click="editOpen = true">{{ t('marketing.edit_campaign') }}</AppButton>
          <AppButton v-if="canCopy" @click="duplicate">{{ t('marketing.duplicate') }}</AppButton>
          <AppButton
            v-for="a in actions"
            :key="a"
            :variant="a === 'activate' || a === 'resume' ? 'primary' : DESTRUCTIVE.includes(a) ? 'destructive' : 'secondary'"
            @click="pending = a"
          >
            {{ t(`marketing.action_${a}`) }}
          </AppButton>
        </template>
      </DetailHeader>

      <p v-if="lockedApproverActions" class="hint" role="note">{{ t('marketing.approver_only_hint') }}</p>

      <div class="detail">
        <div class="detail__main">
          <Card :title="t('marketing.rules')">
            <p class="summary">{{ summary }}</p>
            <KeyValueList :items="ruleItems" />
          </Card>

          <Card :title="t('marketing.copy_title')" :subtitle="t('marketing.copy_hint')">
            <dl class="copy">
              <template v-for="lang in ['kk', 'ru', 'en'] as const" :key="lang">
                <dt class="copy__lang">{{ lang.toUpperCase() }}</dt>
                <dd>
                  <p class="copy__title">{{ c.title[lang] || '—' }}</p>
                  <p v-if="c.description?.[lang]" class="copy__text">{{ c.description[lang] }}</p>
                </dd>
              </template>
            </dl>
          </Card>

          <Card :title="t('marketing.versions_title')" :subtitle="t('marketing.versions_hint')">
            <ol class="versions">
              <li v-for="v in c.versions" :key="v.id" class="versions__item">
                <div class="versions__head">
                  <strong>{{ t('marketing.version_n', { n: v.version }) }}</strong>
                  <span v-if="v.version === c.version" class="tag">{{ t('marketing.version_current') }}</span>
                  <span class="jt-muted">{{ fmt.dateTime(v.created_at) }}</span>
                </div>
                <p>{{ fmt.name(v.title) }} — {{ rulesSummary(c.template, v.rules, words) }}</p>
                <p v-if="v.starts_at || v.ends_at" class="jt-muted">
                  {{ t('marketing.window_range', { from: v.starts_at ? fmt.dateTime(v.starts_at) : '…', to: v.ends_at ? fmt.dateTime(v.ends_at) : '…' }) }}
                </p>
                <p v-if="v.change_note" class="jt-muted">{{ t('marketing.change_note') }}: {{ v.change_note === 'seed' ? t('marketing.version_seed') : v.change_note }}</p>
              </li>
            </ol>
          </Card>
        </div>

        <aside class="detail__side">
          <Card v-if="launch" :title="t('marketing.switch_card')">
            <CampaignSwitch :campaign="c" @changed="apply" @stale="refresh" />
          </Card>

          <Card v-if="hasMoney(c.template)" :title="t('marketing.usage_title')">
            <template v-if="canBudget" #actions>
              <AppButton size="sm" @click="budgetOpen = true">{{ t('marketing.change_budget') }}</AppButton>
            </template>
            <UsageBar :campaign="c" class="usage" />
            <KeyValueList :items="usageItems" />
          </Card>

          <Card :title="t('marketing.preview_title')" :subtitle="t('marketing.preview_hint')">
            <StateView v-if="!preview.data.value" :loading="preview.loading.value" :error="preview.error.value" compact @retry="preview.reload()" />
            <template v-else>
              <KeyValueList :items="previewItems" />
              <ul v-if="preview.data.value.warnings.length" class="warnings">
                <li v-for="w in preview.data.value.warnings" :key="w" class="warnings__item">{{ warningText(w) }}</li>
              </ul>
            </template>
          </Card>

          <Card :title="t('marketing.history_title')">
            <KeyValueList :items="timeItems" />
          </Card>
        </aside>
      </div>

      <CampaignForm v-model:open="editOpen" :campaign="c" @saved="apply" />
      <BudgetDialog v-model:open="budgetOpen" :campaign="c" @saved="onBudgetSaved" />
      <ReasonDialog
        v-model:open="reasonOpen"
        :title="dialogTitle"
        :message="pending ? actionMessage(pending) : ''"
        :placeholder="t('marketing.reason_placeholder')"
        :max-length="300"
        :min-length="3"
        :confirm-label="pending ? t(`marketing.action_${pending}`) : ''"
        :destructive="pending !== null && DESTRUCTIVE.includes(pending)"
        :submit="runAction"
        :error-text="(err) => transitionErrorText(err, t)"
      />
      <ActionDialog
        v-model:open="scheduleOpen"
        :title="dialogTitle"
        :message="actionMessage('schedule')"
        with-time
        reason-required
        :confirm-label="t('marketing.action_schedule')"
        :submit="runSchedule"
      />
    </template>
  </section>
</template>

<style scoped>
.detail {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 380px;
  gap: var(--jt-space-4);
  align-items: start;
}

.detail__main,
.detail__side {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  min-width: 0;
}

.hint {
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
  color: var(--jt-color-label-secondary);
}

.summary {
  margin-bottom: var(--jt-space-3);
  font: var(--jt-font-headline);
}

.copy {
  display: grid;
  grid-template-columns: 32px minmax(0, 1fr);
  gap: var(--jt-space-3) var(--jt-space-2);
}

.copy__lang {
  padding-top: 2px;
  font: 600 11px/16px var(--jt-font-family);
  color: var(--jt-color-label-secondary);
}

.copy__title {
  font-weight: 600;
}

.copy__text {
  margin-top: 2px;
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.versions {
  display: flex;
  flex-direction: column;
}

.versions__item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 0;
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.versions__item:last-child {
  border-bottom: 0;
}

.versions__head {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
}

.usage {
  margin-bottom: var(--jt-space-2);
}

.warnings {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: var(--jt-space-3);
}

.warnings__item {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-orange-tint);
  color: var(--jt-color-orange-text);
  font: var(--jt-font-small);
}

.tag {
  display: inline-flex;
  align-items: center;
  height: 20px;
  padding: 0 7px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label-secondary);
  font: 600 11px/16px var(--jt-font-family);
}

.tag--dark {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.tag--danger {
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}
</style>
