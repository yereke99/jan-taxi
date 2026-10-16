<!--
  New Kaspi top-up: amount from the configured presets or a custom amount
  within the limits (public config topup), then POST /driver/wallet/topups
  (idempotent) and on to the payment instructions. `?amount=` pre-selects the
  amount the driver is short of (going online, accepting an order).
-->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import DriverPage from '../../components/driver/DriverPage.vue'
import { JButton, JEmptyState, JTextField, toast } from '../../components/ui'
import { useDriverErrorText } from '../../composables/useDriverErrorText'
import { useFormat } from '../../composables/useFormat'
import { ROUTE } from '../../router/names'
import { isApiError } from '../../services/http'
import { useSessionStore } from '../../stores/session'
import { parseAmountInput, useWalletStore, validateTopUpAmount } from '../../stores/wallet'
import { haptics } from '../../telegram'

const { t } = useI18n()
const fmt = useFormat()
const errorText = useDriverErrorText()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const wallet = useWalletStore()

const limits = computed(() => session.config.topup)
const reloading = ref(false)
const custom = ref('')
const selected = ref<number | null>(null)
const showError = ref(false)

/** The amount the driver is short of, passed by the screen that sent them here. */
const required = computed(() => {
  const raw = route.query.amount
  const n = typeof raw === 'string' ? parseAmountInput(raw) : null
  return n && n > 0 ? n : null
})

const amount = computed<number | null>(() => (custom.value.trim() ? parseAmountInput(custom.value) : selected.value))
const problem = computed(() => {
  const l = limits.value
  if (!l || amount.value === null) return amount.value === null && custom.value.trim() ? 'invalid' : null
  return validateTopUpAmount(amount.value, l)
})
const errorMessage = computed(() => {
  const l = limits.value
  if (!showError.value || !problem.value || !l) return ''
  if (problem.value === 'too_small') return t('topup.too_small', { min: fmt.money(l.min) })
  if (problem.value === 'too_large') return t('topup.too_large', { max: fmt.money(l.max) })
  return t('topup.invalid')
})

function pickPreset(p: number): void {
  haptics.selection()
  selected.value = p
  custom.value = ''
  showError.value = false
}

function onCustom(v: string): void {
  const n = parseAmountInput(v)
  custom.value = n === null ? v.replace(/[^\d\s]/g, '') : String(n)
  selected.value = null
  showError.value = false
}

async function reloadConfig(): Promise<void> {
  reloading.value = true
  try {
    await session.refreshConfig()
  } finally {
    reloading.value = false
  }
}

async function create(): Promise<void> {
  if (amount.value === null || problem.value) {
    showError.value = true
    haptics.notification('error')
    return
  }
  try {
    const topup = await wallet.createTopUp(amount.value)
    void router.replace({ name: ROUTE.topupDetail, params: { id: topup.id } })
  } catch (err) {
    haptics.notification('error')
    if (isApiError(err) && err.code === 'validation_failed' && err.fields.amount) {
      showError.value = true
      if (typeof err.details.min === 'number' && typeof err.details.max === 'number') void session.refreshConfig()
    }
    toast.error(errorText(err))
  }
}

// Pre-select once the limits are known (they may arrive after a config retry).
let initialized = false
watch(
  limits,
  (l) => {
    if (!l || initialized) return
    initialized = true
    const want = required.value
    if (want !== null) {
      // Smallest preset that covers the shortfall, else the shortfall itself (within limits).
      const preset = l.presets.find((p) => p >= want)
      if (preset !== undefined) selected.value = preset
      else custom.value = String(Math.min(l.max, Math.max(l.min, want)))
    } else if (l.presets.length > 0) {
      selected.value = l.presets[0]!
    }
  },
  { immediate: true },
)

onMounted(() => {
  if (!limits.value) void reloadConfig()
})
</script>

<template>
  <DriverPage :title="t('topup.title')">
    <template v-if="limits">
      <p v-if="required" class="amt__required" role="note">{{ t('topup.required_hint', { amount: fmt.money(required) }) }}</p>

      <section class="amt">
        <h2 class="amt__title">{{ t('topup.amount_title') }}</h2>
        <div v-if="limits.presets.length > 0" class="amt__presets" :class="{ 'is-three': limits.presets.length % 3 === 0 }">
          <button
            v-for="p in limits.presets"
            :key="p"
            type="button"
            class="amt__preset jt-tabular"
            :class="{ 'is-selected': selected === p && !custom }"
            :aria-pressed="selected === p && !custom"
            @click="pickPreset(p)"
          >
            {{ fmt.money(p) }}
          </button>
        </div>
        <JTextField
          :model-value="custom"
          :label="t('topup.custom')"
          :placeholder="t('topup.custom_placeholder')"
          inputmode="numeric"
          enterkeyhint="done"
          :error="errorMessage"
          :hint="t('topup.limits', { min: fmt.money(limits.min), max: fmt.money(limits.max) })"
          @update:model-value="onCustom"
          @enter="create"
        />
      </section>
      <p class="amt__note">{{ t('topup.verification_note') }}</p>
    </template>

    <div v-else class="amt__state">
      <JEmptyState :title="t('topup.title')" :description="t('topup.config_missing')">
        <template #action><JButton block :loading="reloading" @click="reloadConfig">{{ t('common.retry') }}</JButton></template>
      </JEmptyState>
    </div>

    <template v-if="limits" #footer>
      <JButton block :loading="wallet.creating" :disabled="amount === null" @click="create">
        {{ amount !== null && !problem ? t('topup.continue_amount', { amount: fmt.money(amount) }) : t('topup.continue') }}
      </JButton>
    </template>
  </DriverPage>
</template>

<style scoped>
.amt__required {
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
  padding: var(--jt-space-3) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  font: var(--jt-font-subheadline);
}

.amt {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.amt__title {
  font: var(--jt-font-headline);
}

.amt__presets {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--jt-space-2);
}

.amt__presets.is-three {
  grid-template-columns: repeat(3, 1fr);
}

.amt__preset {
  height: var(--jt-button-height);
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-headline);
  transition:
    background-color var(--jt-duration-fast) var(--jt-ease),
    color var(--jt-duration-fast) var(--jt-ease);
}

.amt__preset.is-selected {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.amt__preset:active {
  opacity: var(--jt-opacity-pressed);
}

.amt__note {
  margin: 0 calc(var(--jt-side-margin) + var(--jt-space-4));
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.amt__state {
  padding: var(--jt-space-8) var(--jt-side-margin);
}
</style>
