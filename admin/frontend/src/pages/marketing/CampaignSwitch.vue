<!--
  On/off switch of a launch campaign («Алғашқы сапар», «Досыңды шақыр»): ON
  activates a draft or scheduled campaign or resumes a paused one, OFF pauses
  it. Every flip asks for a reason and is sent with confirm: true; switching on
  shows the rules of the version on screen and is refused when a newer version
  was saved meanwhile (the parent reloads it on `stale`). Only approvers
  (marketing.approve) may flip it; everyone else sees who can.
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { campaignsApi, type Campaign, type CampaignDetail } from '../../api/marketing'
import { ReasonDialog, ToggleSwitch, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useAuthStore } from '../../stores/auth'
import { isVersionConflict, launchSwitch, rulesSummary, transitionBody, transitionErrorText } from './campaignModel'

const props = withDefaults(defineProps<{ campaign: Campaign; compact?: boolean }>(), { compact: false })
const emit = defineEmits<{ changed: [campaign: CampaignDetail]; stale: [] }>()

const { t, te } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const auth = useAuthStore()
const open = ref(false)
// Remounts the switch so a cancelled flip shows the stored state again.
const renderKey = ref(0)

const state = computed(() => launchSwitch(props.campaign, auth.can))
const title = computed(() => fmt.name(props.campaign.title))
const who = computed(() => (state.value.action ? t('marketing.switch_who') : t('marketing.switch_ended')))

const budgetText = computed(() => {
  const c = props.campaign
  if (c.budget_limit === null) return t('marketing.budget_unlimited')
  const used = (c.usage?.committed ?? 0) + (c.usage?.spent ?? 0)
  return t('marketing.budget_used', { used: fmt.money(used), limit: fmt.money(c.budget_limit) })
})

const dialog = computed(() => {
  const action = state.value.action
  if (action === 'pause') {
    return { title: t('marketing.switch_off_title', { title: title.value }), message: t('marketing.switch_off_message'), confirm: t('marketing.switch_confirm_off') }
  }
  const c = props.campaign
  const effectKey = `marketing.effect_${c.template}`
  const effect = te(effectKey, 'kk') ? t(effectKey) : ''
  // The approver switches on exactly these rules: the request carries this version.
  const rules = rulesSummary(c.template, c.rules, { t, money: (n: number) => fmt.money(n) })
  const lines = [
    t(action === 'resume' ? 'marketing.switch_resume_message' : 'marketing.switch_on_message', { effect, budget: budgetText.value }),
    t('marketing.switch_rules', { version: t('marketing.version_n', { n: c.version }), rules }),
  ]
  if (c.status === 'scheduled') lines.push(t('marketing.switch_now_not_scheduled'))
  return { title: t('marketing.switch_on_title', { title: title.value }), message: lines.join('\n'), confirm: t('marketing.switch_confirm_on') }
})

function request(): void {
  if (!state.value.allowed) {
    renderKey.value++
    return
  }
  open.value = true
}

async function submit(reason: string): Promise<void> {
  const action = state.value.action
  if (!action) return
  try {
    const updated = await campaignsApi.transition(props.campaign.id, transitionBody(props.campaign, { action, reason, confirm: true }))
    toast.success(action === 'pause' ? t('marketing.switched_off') : t('marketing.switched_on'))
    emit('changed', updated)
  } catch (err) {
    if (!isVersionConflict(err)) throw err
    // A newer version was saved after this one was shown: the dialog closes
    // and the parent reloads, so the approver reads the new rules first.
    errors.report(err)
    emit('stale')
  }
}

watch(open, (v) => {
  if (!v) renderKey.value++
})
</script>

<template>
  <span class="cswitch" :title="state.allowed ? undefined : who" @click.stop @keydown.stop>
    <ToggleSwitch
      :key="renderKey"
      :model-value="state.on"
      :disabled="!state.allowed"
      :label="state.on ? t('marketing.switch_on') : t('marketing.switch_off')"
      @update:model-value="request"
    />
    <span v-if="!compact && !state.allowed" class="cswitch__who">{{ who }}</span>
    <ReasonDialog
      v-model:open="open"
      :title="dialog.title"
      :message="dialog.message"
      :placeholder="t('marketing.reason_placeholder')"
      :max-length="300"
      :min-length="3"
      :confirm-label="dialog.confirm"
      :destructive="state.action === 'pause'"
      :submit="submit"
      :error-text="(err) => transitionErrorText(err, t)"
    />
  </span>
</template>

<style scoped>
.cswitch {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
}

.cswitch__who {
  max-width: 320px;
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
  white-space: normal;
}
</style>
