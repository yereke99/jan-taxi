<!--
  Create a campaign (marketing.edit) from a template with typed rules, or save
  a new version of an existing one (copy, rules, window and a change note).
  Changing a live campaign is an approver's call and takes effect at once;
  coupons and bonuses already issued keep their own version.
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { CREATABLE_TEMPLATES, campaignsApi, segmentsApi, type CampaignDetail, type CampaignTemplate } from '../../api/marketing'
import { AppButton, Drawer, FormField, TextArea, TextInput, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useResource } from '../../composables/useResource'
import { useFormat } from '../../composables/useFormat'
import { useAuthStore } from '../../stores/auth'
import {
  CAMPAIGN_LIMITS,
  campaignToForm,
  emptyCampaignForm,
  hasMoney,
  isLive,
  validateCampaignForm,
  type CampaignForm,
} from './campaignModel'
import DateTimeField from './DateTimeField.vue'
import LocalizedFields from './LocalizedFields.vue'
import RulesFields from './RulesFields.vue'

const props = defineProps<{ open: boolean; campaign?: CampaignDetail | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; saved: [campaign: CampaignDetail] }>()

const { t } = useI18n()
const errors = useErrors()
const fmt = useFormat()
const auth = useAuthStore()

const form = reactive<CampaignForm>(emptyCampaignForm())
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const saving = ref(false)

const mode = computed<'create' | 'version'>(() => (props.campaign ? 'version' : 'create'))
const live = computed(() => !!props.campaign && isLive(props.campaign.status))
/** A passenger credit snapshots its segment at activation: its rules are fixed afterwards. */
const rulesLocked = computed(() => !!props.campaign && props.campaign.template === 'passenger_credit' && props.campaign.status !== 'draft')
const canBudget = computed(() => auth.can('marketing.approve'))

const segments = useResource((signal) => segmentsApi.list(signal))
const regions = useResource((signal) => campaignsApi.regions(signal))
const regionOptions = computed(() => regions.data.value?.items ?? [])
function toggleRegion(id: string): void {
  form.cityIds = form.cityIds.includes(id) ? form.cityIds.filter((v) => v !== id) : [...form.cityIds, id]
}

watch(
  () => props.open,
  (open) => {
    if (!open) return
    Object.assign(form, props.campaign ? campaignToForm(props.campaign) : emptyCampaignForm())
    fieldErrors.value = {}
    formError.value = ''
    if (!segments.data.value) void segments.reload()
    if (!regions.data.value) void regions.reload()
  },
)

const templates = computed(() => CREATABLE_TEMPLATES.map((v) => ({ value: v, label: t(`marketing.template_${v}`), hint: t(`marketing.template_hint_${v}`) })))

function pick(template: CampaignTemplate): void {
  form.template = template
  fieldErrors.value = {}
}

async function save(): Promise<void> {
  const current = props.campaign
  const checked = validateCampaignForm(form, mode.value, {
    canBudget: canBudget.value,
    rulesLocked: rulesLocked.value,
    window: current ? { starts: current.starts_at !== null, ends: current.ends_at !== null } : undefined,
  })
  fieldErrors.value = errors.translate(checked.errors)
  formError.value = ''
  if (!checked.payload || saving.value) return
  saving.value = true
  try {
    const saved = props.campaign ? await campaignsApi.newVersion(props.campaign.id, checked.payload) : await campaignsApi.create(checked.payload)
    toast.success(props.campaign ? t('marketing.version_saved') : t('marketing.created_ok'))
    emit('saved', saved)
    emit('update:open', false)
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    formError.value = errors.text(err)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Drawer :open="open" :title="campaign ? t('marketing.edit_campaign') : t('marketing.new_campaign')" width="640px" @update:open="(v) => emit('update:open', v)">
    <form class="jt-form" novalidate @submit.prevent="save">
      <p v-if="live" class="notice" role="note">{{ t('marketing.live_warning') }}</p>

      <section v-if="form.template !== 'manual_grant'" class="block campaign-regions">
        <h3 class="block__title">{{ t('marketing.regions_title') }}</h3>
        <p class="jt-muted">{{ t('marketing.regions_hint') }}</p>
        <p v-if="regions.error.value" class="field-error" role="alert">{{ errors.text(regions.error.value) }}</p>
        <AppButton v-if="regions.error.value" @click="regions.reload()">{{ t('common.retry') }}</AppButton>
        <label class="region-option">
          <input type="checkbox" :checked="form.cityIds.length === 0" :disabled="rulesLocked || !regions.data.value" @change="form.cityIds = []" />
          <span>{{ t('marketing.regions_all') }}</span>
        </label>
        <div class="region-options">
          <label v-for="region in regionOptions" :key="region.id" class="region-option">
            <input type="checkbox" :checked="form.cityIds.includes(region.id)" :disabled="rulesLocked" @change="toggleRegion(region.id)" />
            <span>{{ fmt.name(region.name) }}</span>
          </label>
        </div>
        <p v-if="fieldErrors.city_ids" class="field-error" role="alert">{{ fieldErrors.city_ids }}</p>
      </section>

      <section v-if="!campaign" class="block">
        <h3 class="block__title">{{ t('marketing.form_template') }}</h3>
        <div class="templates" role="radiogroup" :aria-label="t('marketing.form_template')">
          <label v-for="tpl in templates" :key="tpl.value" class="tpl" :class="{ 'is-active': form.template === tpl.value }">
            <input type="radio" name="campaign-template" :value="tpl.value" :checked="form.template === tpl.value" @change="pick(tpl.value)" />
            <span class="tpl__text">
              <strong>{{ tpl.label }}</strong>
              <span class="jt-muted">{{ tpl.hint }}</span>
            </span>
          </label>
        </div>
        <p v-if="fieldErrors.template" class="field-error" role="alert">{{ fieldErrors.template }}</p>
      </section>
      <p v-else class="jt-muted">{{ t('marketing.template') }}: <strong>{{ t(`marketing.template_${form.template}`) }}</strong> · <span class="jt-mono">{{ form.key }}</span></p>

      <section class="block">
        <LocalizedFields v-model="form.title" :label="t('marketing.title')" name="title" :errors="fieldErrors" :hint="t('marketing.form_title_hint')" required />
        <LocalizedFields
          v-model="form.description"
          :label="t('marketing.description')"
          name="description"
          :errors="fieldErrors"
          :hint="t('marketing.form_description_hint')"
          multiline
          :rows="3"
          :max-length="CAMPAIGN_LIMITS.description"
        />
      </section>

      <section class="block">
        <h3 class="block__title">{{ t('marketing.rules') }}</h3>
        <p v-if="rulesLocked" class="jt-muted">{{ t('marketing.form_rules_locked') }}</p>
        <RulesFields v-model="form.rules" :template="form.template" :errors="fieldErrors" :segments="segments.data.value?.items ?? []" :disabled="rulesLocked" />
      </section>

      <section class="block">
        <h3 class="block__title">{{ t('marketing.form_window') }}</h3>
        <p class="jt-muted block__hint">{{ t('marketing.form_window_hint') }}</p>
        <p v-if="campaign && (campaign.starts_at || campaign.ends_at)" class="jt-muted block__hint">{{ t('marketing.form_window_keep') }}</p>
        <div class="jt-form-row">
          <FormField v-slot="f" :label="t('marketing.form_starts')" :error="fieldErrors.starts_at" :required="form.template === 'driver_milestone_bonus'">
            <DateTimeField :id="f.id" v-model="form.startsAt" :invalid="f.invalid" />
          </FormField>
          <FormField v-slot="f" :label="t('marketing.form_ends')" :error="fieldErrors.ends_at" :required="form.template === 'driver_milestone_bonus'">
            <DateTimeField :id="f.id" v-model="form.endsAt" :invalid="f.invalid" />
          </FormField>
        </div>
      </section>

      <section v-if="!campaign" class="block">
        <h3 class="block__title">{{ t('marketing.form_more') }}</h3>
        <div class="jt-form-row">
          <FormField v-slot="f" :label="t('marketing.priority')" :hint="t('marketing.form_priority_hint')" :error="fieldErrors.priority">
            <TextInput :id="f.id" v-model="form.priority" inputmode="numeric" :invalid="f.invalid" />
          </FormField>
          <FormField v-slot="f" :label="t('marketing.exclusivity_group')" :hint="t('marketing.form_group_hint')" :error="fieldErrors.exclusivity_group">
            <TextInput :id="f.id" v-model="form.exclusivityGroup" class="jt-mono" :invalid="f.invalid" />
          </FormField>
        </div>
        <FormField v-slot="f" :label="t('marketing.key')" :hint="t('marketing.form_key_hint')" :error="fieldErrors.key">
          <TextInput :id="f.id" v-model="form.key" class="jt-mono" :invalid="f.invalid" />
        </FormField>
        <FormField v-if="canBudget && hasMoney(form.template)" v-slot="f" :label="t('marketing.form_budget')" :hint="t('marketing.form_budget_hint')" :error="fieldErrors.budget_limit">
          <TextInput :id="f.id" v-model="form.budgetLimit" inputmode="numeric" :invalid="f.invalid" />
        </FormField>
      </section>

      <FormField v-if="campaign" v-slot="f" :label="t('marketing.change_note')" :hint="t('marketing.form_change_note_hint')" :error="fieldErrors.change_note">
        <TextArea :id="f.id" v-model="form.changeNote" :rows="2" :max-length="CAMPAIGN_LIMITS.changeNote" :invalid="f.invalid" />
      </FormField>

      <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
    </form>
    <template #footer>
      <AppButton :disabled="saving" @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="primary" :loading="saving" @click="save">{{ campaign ? t('marketing.save_version') : t('common.create') }}</AppButton>
    </template>
  </Drawer>
</template>

<style scoped>
.region-options { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 4px 12px; }
.region-option { display: flex; align-items: center; gap: 10px; min-height: 44px; cursor: pointer; font: var(--jt-font-body); }
.region-option input { width: 18px; height: 18px; flex: none; accent-color: var(--jt-color-primary); }
.block {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.block__title {
  font: var(--jt-font-headline);
}

.block__hint {
  margin-top: -6px;
  font: var(--jt-font-small);
}

.templates {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

.tpl {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  cursor: pointer;
}

.tpl.is-active {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}

.tpl input {
  margin-top: 3px;
  accent-color: #000;
}

.tpl__text {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.tpl__text .jt-muted {
  font: var(--jt-font-small);
}

.field-error {
  font: var(--jt-font-small);
  color: var(--jt-color-red);
}

.notice {
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-orange-tint);
  color: var(--jt-color-orange-text);
}

.form-error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
