<!--
  Create or edit a segment (marketing.edit): a name, the audience (fixed once
  saved) and typed conditions, with a live estimate of how many people match
  and how many can actually get a message (debounced, counts only).
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { AUDIENCES, segmentsApi, type Audience, type AudienceCount, type Segment } from '../../api/marketing'
import { AppButton, Drawer, FormField, Tabs, TextInput, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { isAbortError } from '../../services/api'
import { codePoints } from '../../utils/text'
import FiltersEditor from './FiltersEditor.vue'
import { emptyFiltersForm, filtersToForm, formToFilters, type FiltersForm } from './segmentModel'

const props = defineProps<{ open: boolean; segment: Segment | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; saved: [segment: Segment] }>()

const MAX_NAME = 120
const { t } = useI18n()
const errors = useErrors()
const fmt = useFormat()

const form = reactive<{ name: string; audience: Audience; filters: FiltersForm }>({ name: '', audience: 'passengers', filters: emptyFiltersForm() })
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const saving = ref(false)
const count = ref<AudienceCount | null>(null)
const counting = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
let ctrl: AbortController | null = null

watch(
  () => props.open,
  (open) => {
    if (!open) return
    form.name = props.segment?.name ?? ''
    form.audience = props.segment?.audience ?? 'passengers'
    form.filters = filtersToForm(props.segment?.filters)
    fieldErrors.value = {}
    formError.value = ''
    count.value = null
  },
)

const audienceTabs = computed(() => AUDIENCES.map((a) => ({ value: a, label: t(`marketing.audience_${a}`) })))
const checked = computed(() => formToFilters(form.audience, form.filters))

async function estimate(): Promise<void> {
  ctrl?.abort()
  const filters = checked.value.filters
  if (!filters) {
    count.value = null
    counting.value = false
    return
  }
  const mine = new AbortController()
  ctrl = mine
  counting.value = true
  try {
    const c = await segmentsApi.preview(form.audience, filters, mine.signal)
    if (ctrl === mine) count.value = c
  } catch (err) {
    if (!isAbortError(err) && ctrl === mine) count.value = null
  } finally {
    if (ctrl === mine) counting.value = false
  }
}

// Recount after a pause in editing.
watch(
  () => [props.open, form.audience, JSON.stringify(form.filters)] as const,
  ([open]) => {
    clearTimeout(timer)
    if (!open) return
    timer = setTimeout(() => void estimate(), 500)
  },
)

onBeforeUnmount(() => {
  clearTimeout(timer)
  ctrl?.abort()
})

async function save(): Promise<void> {
  const local: Record<string, string> = { ...checked.value.errors }
  const name = form.name.replace(/\s+/g, ' ').trim()
  if (!name) local.name = 'required'
  else if (codePoints(name) > MAX_NAME) local.name = 'too_long'
  fieldErrors.value = errors.translate(local)
  formError.value = ''
  if (Object.keys(local).length > 0 || !checked.value.filters || saving.value) return
  saving.value = true
  try {
    const saved = props.segment
      ? await segmentsApi.update(props.segment.id, { name, filters: checked.value.filters })
      : await segmentsApi.create({ name, audience: form.audience, filters: checked.value.filters })
    toast.success(t('marketing.segment_saved'))
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
  <Drawer :open="open" :title="segment ? t('marketing.edit_segment') : t('marketing.new_segment')" width="560px" @update:open="(v) => emit('update:open', v)">
    <form class="jt-form" novalidate @submit.prevent="save">
      <FormField v-slot="f" :label="t('common.name')" :error="fieldErrors.name" required>
        <TextInput :id="f.id" v-model="form.name" :invalid="f.invalid" />
      </FormField>
      <div class="audience">
        <p class="audience__label">{{ t('marketing.audience') }}</p>
        <Tabs v-if="!segment" v-model="form.audience" :tabs="audienceTabs" :aria-label="t('marketing.audience')" />
        <p v-else>{{ t(`marketing.audience_${segment.audience}`) }} <span class="jt-muted">· {{ t('marketing.audience_fixed') }}</span></p>
      </div>
      <FiltersEditor v-model="form.filters" :audience="form.audience" :errors="fieldErrors" />

      <div class="estimate" role="status">
        <strong>{{ t('marketing.preview_live') }}</strong>
        <span v-if="!checked.filters" class="jt-muted">{{ t('marketing.preview_fix') }}</span>
        <span v-else-if="counting && !count" class="jt-muted">{{ t('marketing.preview_counting') }}</span>
        <span v-else-if="count" class="jt-num">{{ t('marketing.counts_value', { reachable: fmt.count(count.reachable), suppressed: fmt.count(count.suppressed), matching: fmt.count(count.matching) }) }}</span>
        <span class="jt-muted estimate__hint">{{ t('marketing.count_hint') }}</span>
      </div>

      <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
    </form>
    <template #footer>
      <AppButton :disabled="saving" @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="primary" :loading="saving" @click="save">{{ t('common.save') }}</AppButton>
    </template>
  </Drawer>
</template>

<style scoped>
.audience {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.audience__label {
  font: 500 13px/18px var(--jt-font-family);
}

.estimate {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-fill-secondary);
}

.estimate__hint {
  font: var(--jt-font-caption);
}

.form-error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
