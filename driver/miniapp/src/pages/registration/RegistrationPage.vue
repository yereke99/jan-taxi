<!--
  Driver registration and corrections (§11, §12): a multi-step iOS form whose
  current step lives in the URL (?step=), so the Telegram BackButton walks
  back through the steps. Text is saved to the server draft as the driver
  types (PUT /driver/registration), on every step change and when the Mini
  App is hidden; photos upload immediately. In changes_requested only the
  steps with flagged fields are shown and unflagged fields are locked.
-->
<script setup lang="ts">
import { Headset, Languages } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import type { RegistrationField } from '../../api/types'
import DriverPage from '../../components/driver/DriverPage.vue'
import StepConsent from '../../components/driver/registration/StepConsent.vue'
import StepDocuments from '../../components/driver/registration/StepDocuments.vue'
import StepPersonal from '../../components/driver/registration/StepPersonal.vue'
import StepPhone from '../../components/driver/registration/StepPhone.vue'
import StepPhotos from '../../components/driver/registration/StepPhotos.vue'
import StepReview from '../../components/driver/registration/StepReview.vue'
import StepVehicle from '../../components/driver/registration/StepVehicle.vue'
import { JButton, JEmptyState, JSkeleton, toast } from '../../components/ui'
import {
  firstOpenStep,
  isCorrectionMode,
  isStepComplete,
  STEP_IDS,
  visibleSteps,
  type StepContext,
  type StepId,
} from '../../features/registration/form'
import { useDriverErrorText } from '../../composables/useDriverErrorText'
import { ROUTE } from '../../router/names'
import { useRegistrationStore } from '../../stores/registration'
import { useSessionStore } from '../../stores/session'
import { haptics, useTelegramFullscreen } from '../../telegram'

const { t } = useI18n()
const errorText = useDriverErrorText()
const route = useRoute()
const router = useRouter()
const reg = useRegistrationStore()
const session = useSessionStore()
const tg = useTelegramFullscreen()

const page = ref<InstanceType<typeof DriverPage> | null>(null)
const showErrors = ref(false)
const advancing = ref(false)
const problemFields = ref<RegistrationField[]>([])

const r = computed(() => reg.registration)
const correction = computed(() => (r.value ? isCorrectionMode(r.value) : false))
const steps = computed<StepId[]>(() => (r.value ? visibleSteps(r.value, reg.consentRecorded) : []))
const ctx = computed<StepContext | null>(() =>
  r.value ? { registration: r.value, form: reg.form, consentGiven: reg.consentGiven || reg.consentRecorded } : null,
)

function isStep(v: unknown): v is StepId {
  return typeof v === 'string' && (STEP_IDS as readonly string[]).includes(v)
}

const step = computed<StepId | null>(() => {
  const q = route.query.step
  return isStep(q) && steps.value.includes(q) ? q : null
})
const index = computed(() => (step.value ? steps.value.indexOf(step.value) : -1))
const isReview = computed(() => step.value === 'review')
const fromReview = computed(() => route.query.from === 'review')
const title = computed(() => (correction.value ? t('registration.corrections_title') : t('registration.title')))
const stepComplete = computed(() => (step.value && ctx.value ? isStepComplete(step.value, ctx.value) : false))

const saveState = computed(() => {
  if (reg.saving) return t('registration.saving')
  if (reg.saveError) return t('registration.save_failed')
  return ''
})

// A missing or hidden step (fresh open, start parameter, corrections) goes to the first open one.
watch(
  [r, step],
  () => {
    if (!r.value || !ctx.value || step.value) return
    // Corrections start at the first flagged step: what is "complete" may still be wrong.
    const target = correction.value ? steps.value[0]! : firstOpenStep(steps.value, ctx.value)
    void router.replace({ name: ROUTE.register, query: { step: target } })
  },
  { immediate: true },
)

watch(step, async () => {
  showErrors.value = false
  await nextTick()
  page.value?.scrollToTop()
})

async function saveNow(): Promise<boolean> {
  try {
    await reg.save()
    return true
  } catch (err) {
    toast.error(errorText(err))
    return false
  }
}

async function next(): Promise<void> {
  const s = step.value
  if (!s || !ctx.value) return
  if (!isStepComplete(s, ctx.value)) {
    showErrors.value = true
    haptics.notification('error')
    return
  }
  advancing.value = true
  const saved = await saveNow()
  advancing.value = false
  if (!saved) return
  if (fromReview.value) {
    router.back()
    return
  }
  const following = steps.value[index.value + 1]
  if (following) void router.push({ name: ROUTE.register, query: { step: following } })
}

function edit(target: StepId): void {
  void router.push({ name: ROUTE.register, query: { step: target, from: 'review' } })
}

async function submit(): Promise<void> {
  if (!ctx.value) return
  if (!isStepComplete('review', ctx.value)) {
    showErrors.value = true
    haptics.notification('error')
    return
  }
  const res = await reg.submit()
  if (res.ok) {
    haptics.notification('success')
    toast.success(t('registration.review.submitted_toast'))
    // The new status (pending) moves the driver to the status screen.
    await session.refreshMe()
    return
  }
  problemFields.value = res.problem.fields
  toast.error(errorText(res.problem.error))
  const first = res.problem.steps.find((s) => steps.value.includes(s))
  if (first && first !== 'review') edit(first)
}

// Nothing typed is lost when the Mini App goes to the background.
function flush(): void {
  void reg.save().catch(() => undefined)
}
function onVisibility(): void {
  if (document.visibilityState === 'hidden') flush()
}
let offActive: (() => void) | undefined

onMounted(async () => {
  document.addEventListener('visibilitychange', onVisibility)
  offActive = tg.onActiveChange((active) => {
    if (!active) flush()
  })
  if (!reg.registration) await reg.load()
  else await reg.refresh()
  if (session.me) reg.prefillNames(session.me.first_name, session.me.last_name)
})

onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', onVisibility)
  offActive?.()
  flush()
})
</script>

<template>
  <DriverPage ref="page" :title="title" :large="false">
    <template #trailing>
      <button type="button" class="reg__icon" :aria-label="t('language.title')" @click="router.push({ name: ROUTE.language })">
        <Languages :size="22" :stroke-width="2" aria-hidden="true" />
      </button>
      <button type="button" class="reg__icon" :aria-label="t('support.title')" @click="router.push({ name: ROUTE.support })">
        <Headset :size="22" :stroke-width="2" aria-hidden="true" />
      </button>
    </template>

    <template v-if="r && step">
      <header class="reg__head">
        <div class="reg__progress" role="progressbar" :aria-valuemin="1" :aria-valuemax="steps.length" :aria-valuenow="index + 1">
          <span v-for="(s, i) in steps" :key="s" class="reg__seg" :class="{ 'is-done': i < index, 'is-current': i === index }" />
        </div>
        <p class="reg__count jt-tabular">{{ t('registration.step_of', { current: index + 1, total: steps.length }) }}</p>
        <h2 class="reg__title">{{ t(`registration.steps.${step}`) }}</h2>
        <p v-if="correction && index === 0" class="reg__corr">{{ t('registration.corrections.text') }}</p>
      </header>

      <Transition name="jt-fade" mode="out-in">
        <StepPersonal v-if="step === 'personal'" :show-errors="showErrors" />
        <StepPhone v-else-if="step === 'phone'" :show-errors="showErrors" />
        <StepDocuments v-else-if="step === 'documents'" :show-errors="showErrors" />
        <StepVehicle v-else-if="step === 'vehicle'" :show-errors="showErrors" />
        <StepPhotos v-else-if="step === 'photos'" :show-errors="showErrors" />
        <StepConsent v-else-if="step === 'consent'" :show-errors="showErrors" />
        <StepReview v-else :steps="steps" :problem-fields="problemFields" @edit="edit" />
      </Transition>
    </template>

    <div v-else-if="reg.loadError && !r" class="reg__state">
      <JEmptyState :title="t('app.load_failed_title')" :description="errorText(reg.loadError)">
        <template #action>
          <JButton block :loading="reg.loading" @click="reg.load()">{{ t('common.retry') }}</JButton>
        </template>
      </JEmptyState>
    </div>

    <div v-else class="reg__skeleton" aria-busy="true">
      <JSkeleton height="6px" radius="3px" />
      <JSkeleton width="40%" height="28px" />
      <JSkeleton height="180px" radius="22px" />
      <JSkeleton height="120px" radius="22px" />
    </div>

    <template v-if="r && step" #footer>
      <p v-if="saveState" class="reg__save" :class="{ 'is-error': reg.saveError && !reg.saving }" aria-live="polite">{{ saveState }}</p>
      <JButton v-if="isReview" block :loading="reg.submitting" :disabled="!stepComplete" @click="submit">
        {{ correction ? t('registration.review.resubmit') : t('registration.review.submit') }}
      </JButton>
      <JButton v-else block :variant="stepComplete ? 'primary' : 'secondary'" :loading="advancing" @click="next">
        {{ fromReview ? t('common.done') : t('registration.next') }}
      </JButton>
    </template>
  </DriverPage>
</template>

<style scoped>
.reg__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: var(--jt-tap-target);
  height: var(--jt-tap-target);
  color: var(--jt-color-label);
}

.reg__icon:active {
  opacity: var(--jt-opacity-pressed);
}

.reg__head {
  padding: var(--jt-space-2) calc(var(--jt-side-margin) + var(--jt-space-1)) var(--jt-space-4);
}

.reg__progress {
  display: flex;
  gap: 4px;
  margin-bottom: var(--jt-space-3);
}

.reg__seg {
  flex: 1;
  height: 4px;
  border-radius: 2px;
  background: var(--jt-color-fill-tertiary);
  transition: background-color var(--jt-duration-base) var(--jt-ease);
}

.reg__seg.is-done,
.reg__seg.is-current {
  background: var(--jt-color-primary);
}

.reg__count {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.reg__title {
  margin-top: 2px;
  font: var(--jt-font-title1);
}

.reg__corr {
  margin-top: var(--jt-space-2);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-red-text);
}

.reg__state {
  padding: var(--jt-space-8) var(--jt-side-margin);
}

.reg__skeleton {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  padding: var(--jt-space-4) var(--jt-side-margin);
}

.reg__save {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  text-align: center;
}

.reg__save.is-error {
  color: var(--jt-color-red-text);
}
</style>
