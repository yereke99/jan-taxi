<!--
  Campaign budget (marketing.approve): a limit in whole tenge or no limit,
  with a required reason; sent with confirm: true. The current usage is shown
  so a limit below it (no new awards) is a conscious choice.
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { campaignsApi, type Campaign, type CampaignDetail } from '../../api/marketing'
import { AppButton, FormField, Modal, TextArea, TextInput, ToggleSwitch } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { parseInteger } from '../../forms/numbers'
import { codePoints } from '../../utils/text'
import { CAMPAIGN_LIMITS, budgetUsage } from './campaignModel'

const props = defineProps<{ open: boolean; campaign: Campaign }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; saved: [campaign: CampaignDetail] }>()

const MAX_REASON = 300
const { t } = useI18n()
const errors = useErrors()
const fmt = useFormat()
const unlimited = ref(false)
const amount = ref('')
const reason = ref('')
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const busy = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    unlimited.value = props.campaign.budget_limit === null
    amount.value = props.campaign.budget_limit === null ? '' : String(props.campaign.budget_limit)
    reason.value = ''
    fieldErrors.value = {}
    formError.value = ''
  },
)

const usage = computed(() => budgetUsage(props.campaign))
const parsed = computed(() => parseInteger(amount.value))
const belowUsed = computed(() => !unlimited.value && parsed.value !== null && parsed.value < usage.value.used)

async function save(): Promise<void> {
  const local: Record<string, string> = {}
  if (!unlimited.value) {
    if (amount.value.trim() === '') local.budget_limit = 'required'
    else if (parsed.value === null) local.budget_limit = 'integer'
    else if (parsed.value < 0 || parsed.value > CAMPAIGN_LIMITS.budget) local.budget_limit = 'out_of_range'
  }
  const text = reason.value.replace(/\s+/g, ' ').trim()
  if (!text) local.reason = 'required'
  else if (codePoints(text) < 3) local.reason = 'too_short'
  else if (codePoints(text) > MAX_REASON) local.reason = 'too_long'
  fieldErrors.value = errors.translate(local)
  formError.value = ''
  if (Object.keys(local).length > 0 || busy.value) return
  busy.value = true
  try {
    const body = unlimited.value
      ? { unlimited: true as const, reason: text, confirm: true as const }
      : { budget_limit: parsed.value ?? 0, reason: text, confirm: true as const }
    emit('saved', await campaignsApi.budget(props.campaign.id, body))
    emit('update:open', false)
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    formError.value = errors.text(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :open="open" :title="t('marketing.budget_title', { title: fmt.name(campaign.title) })" width="480px" :persistent="busy" @update:open="(v) => emit('update:open', v)">
    <form class="jt-form" novalidate @submit.prevent="save">
      <p class="jt-muted">
        {{ t('marketing.budget_used_note', { used: fmt.money(usage.used), committed: fmt.money(campaign.usage?.committed ?? 0), spent: fmt.money(campaign.usage?.spent ?? 0) }) }}
      </p>
      <ToggleSwitch v-model="unlimited" :label="t('marketing.budget_unlimited_label')" />
      <FormField v-if="!unlimited" v-slot="f" :label="t('marketing.form_budget')" :error="fieldErrors.budget_limit" required>
        <TextInput :id="f.id" v-model="amount" inputmode="numeric" :invalid="f.invalid" />
      </FormField>
      <p v-if="belowUsed" class="warn" role="note">{{ t('marketing.budget_below_used') }}</p>
      <FormField v-slot="f" :label="t('common.reason')" :error="fieldErrors.reason" required>
        <TextArea :id="f.id" v-model="reason" :rows="3" :max-length="MAX_REASON" :invalid="f.invalid" :placeholder="t('marketing.reason_placeholder')" />
      </FormField>
      <p class="jt-muted note">{{ t('marketing.approver_note') }}</p>
      <p v-if="formError" class="error" role="alert">{{ formError }}</p>
    </form>
    <template #footer>
      <AppButton :disabled="busy" @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="primary" :loading="busy" @click="save">{{ t('marketing.budget_save') }}</AppButton>
    </template>
  </Modal>
</template>

<style scoped>
.warn {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-orange-tint);
  color: var(--jt-color-orange-text);
  font: var(--jt-font-small);
}

.note {
  font: var(--jt-font-caption);
}

.error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
