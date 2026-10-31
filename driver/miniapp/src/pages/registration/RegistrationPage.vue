<!--
  Driver registration and corrections (§11, §12) on two pages whose current
  one lives in the URL (?step=), so the Telegram BackButton goes back from
  the car to the personal details:
    1. personal — profile photo, first name, surname, optional patronymic,
       IIN and the phone confirmed through Telegram → «Келесі»;
    2. vehicle — body type (Sedan preselected), make and model, colour,
       plate and the personal-data consent → «Тіркелуді аяқтау».
  Text is saved to the server draft as the driver types (PUT
  /driver/registration), on every page change and when the Mini App is
  hidden; the photo uploads at once. The first "Next" creates the profile
  and sends the device position with it, so the driver's code takes the
  prefix of the city (or nationwide region) where they are. Finishing runs
  the automatic checks and approves the driver in the same request; the app
  then opens Orders and goes online (features/registration/useStartWork).
  In changes_requested only the pages with flagged fields are shown and
  unflagged fields are locked.
-->
<script setup lang="ts">
import { CircleCheck, Headset, Languages } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { isApiError } from '../../services/http'
import DriverPage from '../../components/driver/DriverPage.vue'
import ConsentCheck from '../../components/driver/registration/ConsentCheck.vue'
import StepPersonal from '../../components/driver/registration/StepPersonal.vue'
import StepVehicle from '../../components/driver/registration/StepVehicle.vue'
import { JButton, JEmptyState, JSkeleton, JSpinner, toast } from '../../components/ui'
import {
  firstOpenStep,
  isCorrectionMode,
  isStepComplete,
  STEP_IDS,
  visibleSteps,
  type StepContext,
  type StepId,
} from '../../features/registration/form'
import { useStartWork } from '../../features/registration/useStartWork'
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
const startWork = useStartWork()

const page = ref<InstanceType<typeof DriverPage> | null>(null)
const showErrors = ref(false)
const advancing = ref(false)
const consentError = ref(false)
/** Finished and approved: the app is on its way to Orders. */
const finished = ref(false)
/** Open the next page with its problems shown (finishing found one there). */
let errorsOnArrival = false

const r = computed(() => reg.registration)
const correction = computed(() => (r.value ? isCorrectionMode(r.value) : false))
const steps = computed<StepId[]>(() => (r.value ? visibleSteps(r.value) : []))
const ctx = computed<StepContext | null>(() => (r.value ? { registration: r.value, form: reg.form, classes: session.extras.vehicleClasses } : null))

function isStep(v: unknown): v is StepId {
  return typeof v === 'string' && (STEP_IDS as readonly string[]).includes(v)
}

const step = computed<StepId | null>(() => {
  const q = route.query.step
  return isStep(q) && steps.value.includes(q) ? q : null
})
const index = computed(() => (step.value ? steps.value.indexOf(step.value) : -1))
const isLast = computed(() => index.value === steps.value.length - 1)
const askConsent = computed(() => isLast.value && !reg.consentRecorded)
const title = computed(() => (correction.value ? t('registration.corrections_title') : t('registration.title')))
const stepComplete = computed(() => (step.value && ctx.value ? isStepComplete(step.value, ctx.value) : false))
const ready = computed(() => stepComplete.value && (!askConsent.value || reg.consentGiven))

const saveState = computed(() => {
  if (reg.saving) return t('registration.saving')
  const err = reg.saveError
  if (!err) return ''
  // A refused value (taken plate or IIN, invalid field) is shown under its
  // field; "check your connection" is only for requests that did not get through.
  if (isApiError(err) && err.status >= 400 && err.status < 500 && err.status !== 408 && err.status !== 429) {
    return t('registration.save_rejected')
  }
  return t('registration.save_failed')
})

// A missing or hidden page (fresh open, start parameter, corrections) goes to the first open one.
watch(
  [r, step],
  () => {
    // Not while leaving the screen (finished, or a status change moves the driver on).
    if (!r.value || !ctx.value || step.value || finished.value || route.name !== ROUTE.register) return
    // Corrections start at the first page with remarks: what is "complete" may still be wrong.
    const target = correction.value ? steps.value[0]! : firstOpenStep(steps.value, ctx.value)
    void router.replace({ name: ROUTE.register, query: { step: target } })
  },
  { immediate: true },
)

watch(step, async () => {
  // A page opened to show its problems does so once it is in place (onStepShown).
  showErrors.value = false
  if (errorsOnArrival) return
  await nextTick()
  page.value?.scrollToTop()
})

async function onStepShown(): Promise<void> {
  if (!errorsOnArrival) return
  errorsOnArrival = false
  showErrors.value = true
  await nextTick()
  scrollToProblem()
}

/** Brings the first highlighted field (or the consent box) into view. */
function scrollToProblem(): void {
  const root = page.value?.scrollEl
  const el = root?.querySelector('[aria-invalid="true"], [role="alert"]')
  el?.scrollIntoView?.({ block: 'center', behavior: 'smooth' })
}

async function showProblems(): Promise<void> {
  showErrors.value = true
  haptics.notification('error')
  await nextTick()
  scrollToProblem()
}

/** Opens a page: back through the history when it is the previous entry, else forward. */
function openStep(target: StepId, withErrors: boolean): void {
  if (target === step.value) {
    if (withErrors) void showProblems()
    return
  }
  errorsOnArrival = withErrors
  const location = { name: ROUTE.register, query: { step: target } }
  if (router.options.history.state.back === router.resolve(location).fullPath) router.back()
  else void router.push(location)
}

async function saveNow(): Promise<boolean> {
  try {
    await reg.save(false, true)
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
    await showProblems()
    return
  }
  advancing.value = true
  const saved = await saveNow()
  advancing.value = false
  if (!saved) return
  const following = steps.value[index.value + 1]
  if (following) openStep(following, false)
}

async function finish(): Promise<void> {
  if (!ctx.value || reg.submitting || finished.value) return
  const open = steps.value.find((s) => !isStepComplete(s, ctx.value!))
  if (open) {
    openStep(open, true)
    if (open !== step.value) haptics.notification('error')
    return
  }
  if (askConsent.value && !reg.consentGiven) {
    consentError.value = true
    await showProblems()
    return
  }
  const res = await reg.submit()
  if (res.ok) {
    finished.value = true
    toast.success(t('registration.finished_toast'))
    await startWork(res.registration)
    return
  }
  const err = res.problem.error
  if (isApiError(err) && err.code === 'validation_failed' && Object.keys(err.fields).some((k) => k.startsWith('pd_consent'))) {
    reg.setConsent(false)
    consentError.value = true
  }
  toast.error(errorText(err))
  const first = res.problem.steps.find((s) => steps.value.includes(s))
  if (first) openStep(first, true)
  else await showProblems()
}

// The consent message goes once the box is ticked.
watch(
  () => reg.consentGiven,
  (given) => {
    if (given) consentError.value = false
  },
)

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
  // The body type row needs the active classes from the public config.
  if (!session.configLoaded && !session.configLoading) void session.refreshConfig()
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

    <section v-if="finished" class="reg__done" role="status">
      <CircleCheck :size="56" :stroke-width="1.5" class="reg__done-icon" aria-hidden="true" />
      <h2 class="reg__done-title">{{ t('registration.finished_title') }}</h2>
      <p class="reg__done-text">{{ t('registration.finishing') }}</p>
      <JSpinner size="large" color="dark" />
    </section>

    <template v-else-if="r && step">
      <header class="reg__head">
        <div
          v-if="steps.length > 1"
          class="reg__progress"
          role="progressbar"
          :aria-valuemin="1"
          :aria-valuemax="steps.length"
          :aria-valuenow="index + 1"
        >
          <span v-for="(s, i) in steps" :key="s" class="reg__seg" :class="{ 'is-done': i < index, 'is-current': i === index }" />
        </div>
        <p v-if="steps.length > 1" class="reg__count jt-tabular">{{ t('registration.step_of', { current: index + 1, total: steps.length }) }}</p>
        <h2 class="reg__title">{{ t(`registration.steps.${step}`) }}</h2>
        <template v-if="correction && index === 0">
          <p class="reg__corr">{{ t('registration.corrections.text') }}</p>
          <p v-if="r.correction_comment" class="reg__comment">{{ t('registration.corrections.comment') }}: «{{ r.correction_comment }}»</p>
        </template>
      </header>

      <Transition name="jt-fade" mode="out-in" @after-enter="onStepShown">
        <StepPersonal v-if="step === 'personal'" :show-errors="showErrors" />
        <StepVehicle v-else :show-errors="showErrors" />
      </Transition>

      <ConsentCheck v-if="askConsent" :invalid="consentError" />
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

    <template v-if="r && step && !finished" #footer>
      <p v-if="saveState" class="reg__save" :class="{ 'is-error': reg.saveError && !reg.saving }" aria-live="polite">{{ saveState }}</p>
      <JButton v-if="isLast" block :variant="ready ? 'primary' : 'secondary'" :loading="reg.submitting" @click="finish">
        {{ t('registration.finish') }}
      </JButton>
      <JButton v-else block :variant="stepComplete ? 'primary' : 'secondary'" :loading="advancing" @click="next">
        {{ t('registration.next') }}
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

.reg__comment {
  margin-top: var(--jt-space-1);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.reg__done {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--jt-space-3);
  margin: var(--jt-space-8) var(--jt-side-margin) 0;
  padding: var(--jt-space-8) var(--jt-space-5);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  text-align: center;
}

.reg__done-icon {
  color: var(--jt-color-green);
}

.reg__done-title {
  font: var(--jt-font-title2);
}

.reg__done-text {
  font: var(--jt-font-body);
  color: var(--jt-color-label-secondary);
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
