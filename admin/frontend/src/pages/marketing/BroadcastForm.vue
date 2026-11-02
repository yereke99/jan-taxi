<!--
  Broadcast editor (marketing.broadcast, drafts only): internal title, the
  audience, a saved segment or this broadcast's own conditions, plain-text
  message per language (line breaks kept, counter up to 3000), an optional
  button that opens a Mini App screen of that audience, and an optional
  campaign link. Nothing is sent from here: sending starts on the detail page.
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { AUDIENCES, START_PARAMS, broadcastsApi, campaignsApi, segmentsApi, type Audience, type BroadcastDetail } from '../../api/marketing'
import { AppButton, Drawer, FormField, SelectInput, Tabs, TextInput, ToggleSwitch, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import { BROADCAST_LIMITS, broadcastToForm, emptyBroadcastForm, validateBroadcastForm, type BroadcastForm } from './broadcastModel'
import FiltersEditor from './FiltersEditor.vue'
import LocalizedFields from './LocalizedFields.vue'
import { emptyFiltersForm } from './segmentModel'

const props = defineProps<{ open: boolean; broadcast?: BroadcastDetail | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; saved: [broadcast: BroadcastDetail] }>()

const { t } = useI18n()
const errors = useErrors()
const fmt = useFormat()

const form = reactive<BroadcastForm>(emptyBroadcastForm())
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const saving = ref(false)

const segments = useResource((signal) => segmentsApi.list(signal))
const campaigns = useResource((signal) => campaignsApi.list({}, 1, 100, signal))

watch(
  () => props.open,
  (open) => {
    if (!open) return
    Object.assign(form, props.broadcast ? broadcastToForm(props.broadcast) : emptyBroadcastForm())
    fieldErrors.value = {}
    formError.value = ''
    void segments.reload({ silent: true })
    void campaigns.reload({ silent: true })
  },
)

const audienceTabs = computed(() => AUDIENCES.map((a) => ({ value: a, label: t(`marketing.audience_${a}`) })))
const targetingTabs = computed(() => [
  { value: 'filters' as const, label: t('marketing.b_targeting_filters') },
  { value: 'segment' as const, label: t('marketing.b_targeting_segment') },
])
const segmentOptions = computed(() =>
  (segments.data.value?.items ?? []).filter((s) => s.audience === form.audience && !s.archived_at).map((s) => ({ value: s.id, label: s.name })),
)
const campaignOptions = computed(() =>
  (campaigns.data.value?.items ?? []).filter((c) => c.status !== 'archived' && c.template !== 'manual_grant').map((c) => ({ value: c.id, label: `${fmt.name(c.title)} · ${c.key}` })),
)
const startParamOptions = computed(() => START_PARAMS[form.audience].map((p) => ({ value: p, label: t(`marketing.sp_${p}`) })))

function setAudience(a: Audience): void {
  if (a === form.audience) return
  form.audience = a
  // Conditions, segments and screens belong to one audience.
  form.segmentId = ''
  form.filters = emptyFiltersForm()
  if (!START_PARAMS[a].includes(form.startParam)) form.startParam = 'home'
}

async function save(): Promise<void> {
  const checked = validateBroadcastForm(form, !!props.broadcast)
  fieldErrors.value = errors.translate(checked.errors)
  formError.value = ''
  if (!checked.payload || saving.value) return
  saving.value = true
  try {
    const saved = props.broadcast ? await broadcastsApi.update(props.broadcast.id, checked.payload) : await broadcastsApi.create(checked.payload)
    toast.success(t('marketing.broadcast_saved'))
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
  <Drawer :open="open" :title="broadcast ? t('marketing.editor_title_edit') : t('marketing.new_broadcast')" width="640px" @update:open="(v) => emit('update:open', v)">
    <form class="jt-form" novalidate @submit.prevent="save">
      <FormField v-slot="f" :label="t('marketing.b_title')" :hint="t('marketing.b_title_hint')" :error="fieldErrors.title" required>
        <TextInput :id="f.id" v-model="form.title" :invalid="f.invalid" />
      </FormField>

      <section class="block">
        <p class="block__label">{{ t('marketing.b_audience') }}</p>
        <Tabs :model-value="form.audience" :tabs="audienceTabs" :aria-label="t('marketing.b_audience')" @update:model-value="setAudience" />
        <p class="block__label">{{ t('marketing.b_targeting') }}</p>
        <Tabs v-model="form.targeting" :tabs="targetingTabs" :aria-label="t('marketing.b_targeting')" />
        <FormField v-if="form.targeting === 'segment'" v-slot="f" :label="t('marketing.b_targeting_segment')" :error="fieldErrors.segment_id" required>
          <SelectInput
            :id="f.id"
            :model-value="form.segmentId || null"
            :options="segmentOptions"
            :placeholder="t('marketing.f_segment_none')"
            :invalid="f.invalid"
            @update:model-value="(v) => (form.segmentId = v ?? '')"
          />
        </FormField>
        <FiltersEditor v-else v-model="form.filters" :audience="form.audience" :errors="fieldErrors" />
      </section>

      <section class="block">
        <LocalizedFields
          v-model="form.content"
          :label="t('marketing.b_content')"
          name="content"
          :errors="fieldErrors"
          :hint="t('marketing.b_content_hint')"
          multiline
          :rows="6"
          :max-length="BROADCAST_LIMITS.text"
          required
        />
      </section>

      <section class="block">
        <ToggleSwitch v-model="form.withButton" :label="t('marketing.b_button')" />
        <template v-if="form.withButton">
          <LocalizedFields v-model="form.buttonLabel" :label="t('marketing.b_button_label')" name="button.label" :errors="fieldErrors" :hint="t('marketing.b_button_label_hint')" required />
          <FormField v-slot="f" :label="t('marketing.b_start_param')" :error="fieldErrors['button.start_param']" required>
            <SelectInput :id="f.id" :model-value="form.startParam || null" :options="startParamOptions" :invalid="f.invalid" @update:model-value="(v) => (form.startParam = v ?? '')" />
          </FormField>
        </template>
      </section>

      <FormField v-slot="f" :label="t('marketing.b_campaign')" :hint="t('marketing.b_campaign_hint')" :error="fieldErrors.campaign_id">
        <SelectInput
          :id="f.id"
          :model-value="form.campaignId || null"
          :options="campaignOptions"
          :placeholder="t('marketing.b_campaign_none')"
          @update:model-value="(v) => (form.campaignId = v ?? '')"
        />
      </FormField>

      <p class="jt-muted">{{ t('marketing.b_draft_note') }}</p>
      <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
    </form>
    <template #footer>
      <AppButton :disabled="saving" @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="primary" :loading="saving" @click="save">{{ t('common.save') }}</AppButton>
    </template>
  </Drawer>
</template>

<style scoped>
.block {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.block__label {
  font: 500 13px/18px var(--jt-font-family);
}

.form-error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
