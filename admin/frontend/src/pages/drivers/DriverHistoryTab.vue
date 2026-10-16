<!-- Application rounds and the moderation journal (who decided what, when, why). -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminDriverDetail, CorrectionItem } from '../../api/drivers'
import { Card, StatusBadge, Timeline, type TimelineItem } from '../../components'
import { useFormat } from '../../composables/useFormat'

const props = defineProps<{ driver: AdminDriverDetail }>()

const { t, te } = useI18n()
const fmt = useFormat()

function correctionText(items: CorrectionItem[]): string {
  return items
    .map((c) => `${t(`correction_field.${c.field}`)}: ${t(`correction_reason.${c.reason}`)}${c.comment ? ` — ${c.comment}` : ''}`)
    .join('\n')
}

const applications = computed(() => [...props.driver.applications].sort((a, b) => b.round - a.round))

const events = computed<TimelineItem[]>(() =>
  [...props.driver.moderation_events].reverse().map((e) => {
    const parts = [e.comment, e.reasons.length ? correctionText(e.reasons) : null].filter(Boolean)
    const who = e.admin ? e.admin.full_name || e.admin.email : te(`actor.${e.actor_type}`, 'kk') ? t(`actor.${e.actor_type}`) : e.actor_type
    const status = e.from_status && e.to_status && e.from_status !== e.to_status ? `${t(`moderation_status.${e.from_status}`)} → ${t(`moderation_status.${e.to_status}`)}` : null
    return {
      key: e.id,
      time: fmt.dateTime(e.created_at),
      title: te(`moderation_action.${e.action}`, 'kk') ? t(`moderation_action.${e.action}`) : e.action,
      detail: parts.join('\n') || null,
      meta: [who, status].filter(Boolean).join(' · '),
      tone: e.action === 'approved' || e.action === 'restored' ? 'success' : e.action === 'rejected' || e.action === 'suspended' ? 'danger' : 'neutral',
    }
  }),
)
</script>

<template>
  <div class="history">
    <Card :title="t('drivers.applications')" :subtitle="t('drivers.rounds', { n: applications.length })">
      <p v-if="applications.length === 0" class="jt-muted">{{ t('drivers.no_applications') }}</p>
      <ol v-else class="rounds">
        <li v-for="a in applications" :key="a.id" class="round">
          <div class="round__head">
            <strong>{{ t('drivers.round_n', { n: a.round }) }}</strong>
            <StatusBadge kind="moderation" :status="a.status" />
            <span class="jt-muted">{{ t('drivers.submitted_at', { time: fmt.dateTime(a.submitted_at) }) }}</span>
          </div>
          <p v-if="a.reviewed_at" class="jt-muted">
            {{ t('drivers.reviewed', { time: fmt.dateTime(a.reviewed_at), name: a.reviewed_by?.full_name || a.reviewed_by?.email || '—' }) }}
          </p>
          <p v-if="a.decision_comment" class="round__comment">{{ a.decision_comment }}</p>
          <ul v-if="a.corrections.length" class="round__corrections">
            <li v-for="(c, i) in a.corrections" :key="i">
              <strong>{{ t(`correction_field.${c.field}`) }}</strong> — {{ t(`correction_reason.${c.reason}`) }}<template v-if="c.comment">: {{ c.comment }}</template>
            </li>
          </ul>
        </li>
      </ol>
    </Card>
    <Card :title="t('drivers.moderation_journal')">
      <Timeline :items="events" :empty="t('table.empty')" />
    </Card>
  </div>
</template>

<style scoped>
.history {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: var(--jt-space-4);
  align-items: start;
}

.rounds {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.round {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding-bottom: var(--jt-space-3);
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.round:last-child {
  border-bottom: 0;
}

.round__head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--jt-space-2);
}

.round__comment {
  padding: 8px 10px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-fill-secondary);
  overflow-wrap: anywhere;
}

.round__corrections {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-left: var(--jt-space-3);
  list-style: disc;
}

:deep(.timeline__detail) {
  white-space: pre-line;
}
</style>
