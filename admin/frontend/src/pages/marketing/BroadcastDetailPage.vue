<!--
  Broadcast detail: the message in three languages and its button, the
  audience estimate before sending (with the approval threshold), delivery
  counts ("sent" = accepted by Telegram, not read; test sends apart), the
  recipients, a test send to one listed Telegram id while a draft or
  scheduled, and the actions of its status: schedule, send now, pause, resume
  and cancel, each confirmed. Cancelling cannot recall what Telegram already
  accepted; a scheduled broadcast the worker refused for approval says why.
  Refreshes while sending.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { broadcastsApi, type BroadcastAction, type BroadcastRecipient } from '../../api/marketing'
import { AppButton, Card, DataTable, DetailHeader, FilterSelect, FormField, KeyValueList, Pagination, StateView, StatusBadge, TextInput, confirmAction, toast, type Column, type KeyValueItem } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useResync } from '../../composables/useRealtime'
import { useResource } from '../../composables/useResource'
import { parseInteger } from '../../forms/numbers'
import { DEFAULT_PAGE_SIZE, isApiError } from '../../services/api'
import { useAuthStore } from '../../stores/auth'
import { RECIPIENT_STATUSES } from '../../utils/status'
import ActionDialog from './ActionDialog.vue'
import BroadcastForm from './BroadcastForm.vue'
import { deliveryStats } from './broadcastModel'
import PersonCell from './PersonCell.vue'

const props = defineProps<{ id: string }>()

const { t, te } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const auth = useAuthStore()
const res = useResource((signal) => broadcastsApi.get(props.id, signal))
void res.reload()

const b = computed(() => res.data.value)
const canSend = computed(() => auth.can('marketing.broadcast'))
const editOpen = ref(false)

// --- actions -----------------------------------------------------------------

const allowed = computed<BroadcastAction[]>(() => {
  const s = b.value?.status
  if (!s || !canSend.value) return []
  const out: BroadcastAction[] = []
  if (s === 'draft') out.push('schedule')
  if (s === 'draft' || s === 'scheduled') out.push('send_now')
  if (s === 'sending') out.push('pause')
  if (s === 'paused') out.push('resume')
  if (s !== 'completed' && s !== 'cancelled' && s !== 'failed') out.push('cancel')
  return out
})

const reachable = computed(() => b.value?.estimate?.reachable ?? null)
const overThreshold = computed(() => reachable.value !== null && !!b.value && reachable.value > b.value.approval_threshold)
const needsApprover = computed(() => overThreshold.value && !auth.can('marketing.approve'))
/** Nothing was sent: a non-approver scheduled it and the audience outgrew the threshold by the send time. */
const failedForApproval = computed(() => b.value?.status === 'failed' && b.value.approval_note === 'approval_required')

const startDialog = ref<'schedule' | 'send_now' | null>(null)
const startOpen = computed({
  get: () => startDialog.value !== null,
  set: (v) => {
    if (!v) startDialog.value = null
  },
})
const startMessage = computed(() => {
  const lines = [t('marketing.b_start_message', { n: reachable.value === null ? '—' : fmt.count(reachable.value) })]
  if (overThreshold.value) {
    const params = { recipients: fmt.count(reachable.value ?? 0), threshold: fmt.count(b.value?.approval_threshold ?? 0) }
    lines.push(needsApprover.value ? t('marketing.approval_needed', params) : t('marketing.approval_by_you', params))
  }
  return lines.join('\n')
})

/** 403 approval_required carries the audience size and the threshold. */
function approvalText(err: unknown): string | null {
  if (!isApiError(err) || err.code !== 'approval_required') return null
  const n = (v: unknown): string => (typeof v === 'number' ? fmt.count(v) : '—')
  return t('marketing.approval_needed', { recipients: n(err.details.recipients), threshold: n(err.details.threshold) })
}

async function start(input: { at: string | null; reason: string }): Promise<void> {
  const action = startDialog.value
  if (!action) return
  const body = action === 'schedule'
    ? { action, scheduled_at: input.at ?? undefined, reason: input.reason || undefined, confirm: true }
    : { action, reason: input.reason || undefined, confirm: true }
  res.set(await broadcastsApi.transition(props.id, body))
  toast.success(action === 'schedule' && input.at ? t('marketing.scheduled_ok', { time: fmt.dateTime(input.at) }) : t('marketing.b_done'))
  void recipients.reload({ silent: true })
}

async function run(action: BroadcastAction): Promise<void> {
  if (action === 'schedule' || action === 'send_now') {
    startDialog.value = action
    return
  }
  const ok = await confirmAction({
    title: t(`marketing.b_${action}_title`),
    message: t(`marketing.b_${action}_message`),
    confirmLabel: t(`marketing.b_action_${action}`),
    destructive: action === 'cancel',
  })
  if (!ok) return
  try {
    res.set(await broadcastsApi.transition(props.id, { action }))
    toast.success(t('marketing.b_done'))
    void recipients.reload({ silent: true })
  } catch (err) {
    errors.report(err)
  }
}

// --- delivery --------------------------------------------------------------------

const stats = computed(() => deliveryStats(b.value?.stats))
const statRows = computed(() => RECIPIENT_STATUSES.map((s) => ({ status: s, n: stats.value.live[s] ?? 0 })))
const testRows = computed(() => Object.entries(stats.value.test).filter(([, n]) => n > 0))

// --- recipients --------------------------------------------------------------------

const rStatus = ref('')
const rPage = ref(1)
const recipients = useResource((signal) => broadcastsApi.recipients(props.id, { status: rStatus.value }, rPage.value, DEFAULT_PAGE_SIZE, signal))
watch([rStatus, rPage], () => void recipients.reload(), { immediate: true })
watch(rStatus, () => (rPage.value = 1))

const recipientStatusOptions = computed(() => RECIPIENT_STATUSES.map((s) => ({ value: s as string, label: t(`recipient_status.${s}`) })))
const recipientColumns = computed<Column<BroadcastRecipient>[]>(() => [
  { key: 'user', label: t('marketing.recipient_user') },
  { key: 'language', label: t('common.language'), format: (v) => String(v).toUpperCase() },
  { key: 'status', label: t('common.status') },
  { key: 'attempts', label: t('marketing.recipient_attempts'), align: 'right' },
  { key: 'sent_at', label: t('marketing.recipient_sent_at'), format: (v) => fmt.dateTime(v as string | null) },
])

/** Telegram's answer to a failed delivery (HTTP status and description). */
function deliveryError(r: BroadcastRecipient): string {
  return r.last_error ? `${r.http_status ?? ''} ${r.last_error}`.trim() : ''
}

// --- test send ----------------------------------------------------------------------

/** The server tests drafts and scheduled broadcasts only, to ids an approver listed (marketing.test_recipients). */
const canTest = computed(() => canSend.value && (b.value?.status === 'draft' || b.value?.status === 'scheduled'))
const testId = ref('')
const testError = ref('')
const testing = ref(false)

async function sendTest(): Promise<void> {
  const id = parseInteger(testId.value)
  testError.value = ''
  if (id === null || id <= 0) {
    testError.value = errors.reason(testId.value.trim() ? 'invalid' : 'required')
    return
  }
  testing.value = true
  try {
    await broadcastsApi.test(props.id, id)
    toast.success(t('marketing.test_queued'))
    void res.reload({ silent: true })
    void recipients.reload({ silent: true })
  } catch (err) {
    testError.value = errors.fields(err).telegram_user_id ?? errors.text(err)
  } finally {
    testing.value = false
  }
}

// --- info -------------------------------------------------------------------------

const infoItems = computed<KeyValueItem[]>(() => {
  const d = b.value
  if (!d) return []
  const rows: KeyValueItem[] = [
    { key: 'audience', label: t('marketing.audience'), value: t(`marketing.audience_${d.audience}`) },
    { key: 'targeting', label: t('marketing.b_targeting'), value: d.segment_id ? t('marketing.b_targeting_segment') : t('marketing.b_targeting_filters') },
    { key: 'recipients', label: t('marketing.b_recipient_count'), value: d.recipient_count === null ? '—' : fmt.count(d.recipient_count) },
    { key: 'created', label: t('common.created'), value: fmt.dateTime(d.created_at) },
  ]
  if (d.scheduled_at) rows.push({ key: 'scheduled', label: t('marketing.time_scheduled_at'), value: fmt.dateTime(d.scheduled_at) })
  if (d.started_at) rows.push({ key: 'started', label: t('marketing.b_started_at'), value: fmt.dateTime(d.started_at) })
  if (d.completed_at) rows.push({ key: 'completed', label: t('marketing.b_completed_at'), value: fmt.dateTime(d.completed_at) })
  if (d.cancelled_at) rows.push({ key: 'cancelled', label: t('marketing.b_cancelled_at'), value: fmt.dateTime(d.cancelled_at) })
  // The worker's approval_required code is explained at the top of the page, not shown as a note.
  if (d.approval_note && !failedForApproval.value) rows.push({ key: 'note', label: t('marketing.b_approval_note'), value: d.approval_note })
  if (d.campaign_id) rows.push({ key: 'campaign', label: t('marketing.b_campaign') })
  return rows
})

// Live counters while the worker sends.
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => {
    if (document.visibilityState === 'visible' && b.value?.status === 'sending') {
      void res.reload({ silent: true })
      void recipients.reload({ silent: true })
    }
  }, 5000)
})
onBeforeUnmount(() => clearInterval(timer))
useResync(() => void res.reload({ silent: true }))
</script>

<template>
  <section class="jt-page">
    <StateView v-if="!b" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <DetailHeader :title="b.title" :back="{ name: 'broadcasts' }" :back-label="t('nav.broadcasts')">
        <template #badges>
          <StatusBadge kind="broadcast" :status="b.status" />
        </template>
        <template #subtitle>{{ t(`marketing.audience_${b.audience}`) }} · {{ t('marketing.version_n', { n: b.version }) }} · {{ fmt.dateTime(b.created_at) }}</template>
        <template v-if="canSend" #actions>
          <AppButton v-if="b.status === 'draft'" @click="editOpen = true">{{ t('common.edit') }}</AppButton>
          <AppButton
            v-for="a in allowed"
            :key="a"
            :variant="a === 'send_now' ? 'primary' : a === 'cancel' ? 'destructive' : 'secondary'"
            @click="run(a)"
          >
            {{ t(`marketing.b_action_${a}`) }}
          </AppButton>
        </template>
      </DetailHeader>

      <p v-if="needsApprover && (b.status === 'draft' || b.status === 'scheduled')" class="notice" role="note">
        {{ t('marketing.approval_needed', { recipients: fmt.count(reachable ?? 0), threshold: fmt.count(b.approval_threshold) }) }}
      </p>
      <p v-if="failedForApproval" class="notice" role="note">{{ t('marketing.b_failed_approval') }}</p>

      <div class="detail">
        <div class="detail__main">
          <Card :title="t('marketing.b_info_title')">
            <dl class="copy">
              <template v-for="lang in ['kk', 'ru', 'en'] as const" :key="lang">
                <dt class="copy__lang">{{ lang.toUpperCase() }}</dt>
                <dd class="copy__text">{{ b.content[lang] }}</dd>
              </template>
            </dl>
            <p v-if="b.button" class="button">
              <span class="button__label">{{ fmt.name(b.button.label) }}</span>
              <span class="jt-muted">→ {{ te(`marketing.sp_${b.button.start_param}`, 'kk') ? t(`marketing.sp_${b.button.start_param}`) : b.button.start_param }}</span>
            </p>
            <p v-else class="jt-muted button">{{ t('marketing.b_no_button') }}</p>
          </Card>

          <Card :title="t('marketing.col_recipients')" flush>
            <template #actions>
              <FilterSelect v-model="rStatus" :options="recipientStatusOptions" :placeholder="t('marketing.all_statuses')" />
            </template>
            <StateView v-if="recipients.error.value" :error="recipients.error.value" compact @retry="recipients.reload()" />
            <template v-else>
              <DataTable :columns="recipientColumns" :rows="recipients.data.value?.items ?? []" row-key="id" :loading="recipients.loading.value" :empty-text="t('marketing.no_recipients')">
                <template #cell-user="{ row }">
                  <span class="user">
                    <PersonCell :id="row.user_id" />
                    <span v-if="row.is_test" class="tag">{{ t('marketing.recipient_test') }}</span>
                  </span>
                </template>
                <template #cell-status="{ row }">
                  <span class="delivery">
                    <StatusBadge kind="recipient" :status="row.status" />
                    <span v-if="row.last_error" class="delivery__error" :title="deliveryError(row)">{{ t('marketing.recipient_error') }}: {{ deliveryError(row) }}</span>
                  </span>
                </template>
              </DataTable>
              <div class="pager">
                <Pagination v-model:page="rPage" :page-size="DEFAULT_PAGE_SIZE" :total="recipients.data.value?.total ?? 0" />
              </div>
            </template>
          </Card>
        </div>

        <aside class="detail__side">
          <Card v-if="b.estimate" :title="t('marketing.estimate_title')" :subtitle="t('marketing.estimate_hint')">
            <KeyValueList
              :items="[
                { key: 'reachable', label: t('marketing.count_reachable'), value: fmt.count(b.estimate.reachable) },
                { key: 'suppressed', label: t('marketing.count_suppressed'), value: fmt.count(b.estimate.suppressed) },
                { key: 'matching', label: t('marketing.count_matching'), value: fmt.count(b.estimate.matching) },
                { key: 'threshold', label: t('marketing.approval_threshold'), value: fmt.count(b.approval_threshold) },
              ]"
            />
          </Card>

          <Card :title="t('marketing.col_delivery')" :subtitle="t('marketing.sent_means')">
            <ul class="stats">
              <li v-for="s in statRows" :key="s.status" class="stats__item">
                <StatusBadge kind="recipient" :status="s.status" />
                <span class="jt-num">{{ fmt.count(s.n) }}</span>
              </li>
            </ul>
            <template v-if="testRows.length">
              <p class="stats__title">{{ t('marketing.test_stats') }}</p>
              <ul class="stats">
                <li v-for="[s, n] in testRows" :key="s" class="stats__item">
                  <StatusBadge kind="recipient" :status="s" />
                  <span class="jt-num">{{ fmt.count(n) }}</span>
                </li>
              </ul>
            </template>
            <p v-if="b.status !== 'completed' && b.status !== 'cancelled' && b.status !== 'failed'" class="jt-muted recall">{{ t('marketing.b_recall_note') }}</p>
          </Card>

          <Card v-if="canTest" :title="t('marketing.test_title')" :subtitle="t('marketing.test_hint')">
            <form class="test" novalidate @submit.prevent="sendTest">
              <FormField v-slot="f" :label="t('drivers.telegram_id')" :error="testError">
                <TextInput :id="f.id" v-model="testId" inputmode="numeric" class="jt-mono" :invalid="f.invalid" />
              </FormField>
              <AppButton type="submit" :loading="testing">{{ t('marketing.test_send') }}</AppButton>
            </form>
          </Card>

          <Card :title="t('marketing.history_title')">
            <KeyValueList :items="infoItems">
              <template #value-campaign>
                <RouterLink v-if="b.campaign_id" :to="{ name: 'campaign-detail', params: { id: b.campaign_id } }">{{ t('common.open') }}</RouterLink>
              </template>
            </KeyValueList>
          </Card>
        </aside>
      </div>

      <BroadcastForm v-model:open="editOpen" :broadcast="b" @saved="(d) => res.set(d)" />
      <ActionDialog
        v-model:open="startOpen"
        :title="startDialog === 'schedule' ? t('marketing.b_schedule_title') : t('marketing.b_send_now_title')"
        :message="startMessage"
        :with-time="startDialog === 'schedule'"
        :confirm-label="startDialog === 'schedule' ? t('marketing.b_action_schedule') : t('marketing.b_action_send_now')"
        :submit="start"
        :error-text="approvalText"
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

.notice {
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-orange-tint);
  color: var(--jt-color-orange-text);
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

.copy__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.button {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  margin-top: var(--jt-space-4);
}

.button__label {
  display: inline-flex;
  align-items: center;
  height: 28px;
  padding: 0 12px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  font-weight: 600;
}

.user {
  display: inline-flex;
  align-items: center;
  gap: var(--jt-space-2);
}

.tag {
  display: inline-flex;
  align-items: center;
  height: 20px;
  padding: 0 7px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font: 600 11px/16px var(--jt-font-family);
}

.delivery {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
}

.delivery__error {
  max-width: 170px;
  overflow: hidden;
  text-overflow: ellipsis;
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
}

.pager {
  padding: 0 var(--jt-space-5) var(--jt-space-3);
}

.stats {
  display: flex;
  flex-direction: column;
}

.stats__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 0;
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.stats__item:last-child {
  border-bottom: 0;
}

.stats__title {
  margin-top: var(--jt-space-3);
  font: var(--jt-font-headline);
}

.recall {
  margin-top: var(--jt-space-3);
  font: var(--jt-font-small);
}

.test {
  display: flex;
  align-items: flex-end;
  gap: var(--jt-space-2);
}

.test > :first-child {
  flex: 1;
}
</style>
