<!--
  «Bonus and commission» block of the settings (§22.1, §22.2, §22.7): the
  welcome bonus switch and amount, the commission type and the fixed amount
  within its read-only bounds. Values are drafts of the settings page and are
  saved with the other changes in one PATCH.
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SettingItem } from '../../api/config'
import { AppButton, Card, TextInput, Tabs, ToggleSwitch } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { parseInteger } from '../../forms/numbers'
import { useAuthStore } from '../../stores/auth'
import type { Draft } from './settingsModel'
import { DEFAULT_WELCOME_BONUS, RULE_KEYS, commissionBounds, welcomeBonusOn } from './settingsModel'

const props = defineProps<{ items: SettingItem[]; drafts: Record<string, Draft>; errors: Record<string, string>; changed: (item: SettingItem) => boolean }>()
const emit = defineEmits<{ update: [key: string, value: Draft]; reset: [item: SettingItem]; grant: [] }>()

const { t } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()

const item = (key: string): SettingItem | undefined => props.items.find((i) => i.key === key)
const bonusItem = computed(() => item(RULE_KEYS.welcomeBonus))
const modeItem = computed(() => item(RULE_KEYS.mode))
const amountItem = computed(() => item(RULE_KEYS.fixedAmount))
const bounds = computed(() => commissionBounds(props.items))

const bonusDraft = computed(() => String(props.drafts[RULE_KEYS.welcomeBonus] ?? ''))
const bonusOn = computed(() => welcomeBonusOn(bonusDraft.value))
/** Amount restored when the switch is turned back on. */
const lastBonus = ref(DEFAULT_WELCOME_BONUS)
watch(
  bonusDraft,
  (v) => {
    const n = parseInteger(v)
    if (n !== null && n > 0) lastBonus.value = n
  },
  { immediate: true },
)

function setBonusOn(on: boolean): void {
  emit('update', RULE_KEYS.welcomeBonus, on ? String(lastBonus.value) : '0')
}

function setBonusAmount(v: string): void {
  emit('update', RULE_KEYS.welcomeBonus, v)
}

function setMode(v: 'fixed' | 'percent'): void {
  emit('update', RULE_KEYS.mode, v)
}

function setFixedAmount(v: string): void {
  emit('update', RULE_KEYS.fixedAmount, v)
}

const mode = computed<'fixed' | 'percent'>(() => (props.drafts[RULE_KEYS.mode] === 'percent' ? 'percent' : 'fixed'))
const modeTabs = computed(() => [
  { value: 'fixed' as const, label: t('bonus.mode_fixed') },
  { value: 'percent' as const, label: t('bonus.mode_percent') },
])

const amountDraft = computed(() => String(props.drafts[RULE_KEYS.fixedAmount] ?? ''))

function errorOf(key: string): string {
  if (key === RULE_KEYS.fixedAmount && props.errors[key]) return t('bonus.commission_range_error', { min: fmt.money(bounds.value.min), max: fmt.money(bounds.value.max) })
  return props.errors[key] ?? ''
}

function isChanged(i: SettingItem | undefined): boolean {
  return i ? props.changed(i) : false
}
</script>

<template>
  <Card :title="t('bonus.settings_title')" :subtitle="t('bonus.settings_hint')">
    <section v-if="bonusItem" class="rule" :class="{ 'is-changed': isChanged(bonusItem) }">
      <div class="rule__head">
        <h3 class="rule__title">{{ t('bonus.welcome_title') }}</h3>
        <button v-if="bonusItem.source === 'database'" type="button" class="jt-link-button rule__reset" @click="emit('reset', bonusItem)">{{ t('settings.reset_default') }}</button>
      </div>
      <div class="rule__row">
        <ToggleSwitch :id="RULE_KEYS.welcomeBonus + '.on'" :model-value="bonusOn" :label="bonusOn ? t('bonus.welcome_on') : t('bonus.welcome_off')" @update:model-value="setBonusOn" />
        <label v-if="bonusOn" class="amount">
          <TextInput :id="RULE_KEYS.welcomeBonus" :model-value="bonusDraft" inputmode="numeric" :aria-label="t('bonus.welcome_amount')" :invalid="Boolean(errors[RULE_KEYS.welcomeBonus])" @update:model-value="setBonusAmount" />
          <span class="amount__unit">₸</span>
        </label>
      </div>
      <p v-if="errorOf(RULE_KEYS.welcomeBonus)" class="rule__error" role="alert">{{ errorOf(RULE_KEYS.welcomeBonus) }}</p>
      <ul class="rule__notes">
        <li>{{ t('bonus.welcome_note_once') }}</li>
        <li>{{ t('bonus.welcome_note_when') }}</li>
        <li>{{ t('bonus.welcome_note_off') }}</li>
      </ul>
    </section>

    <section v-if="modeItem && amountItem" class="rule" :class="{ 'is-changed': isChanged(modeItem) || isChanged(amountItem) }">
      <div class="rule__head">
        <h3 class="rule__title">{{ t('bonus.commission_title') }}</h3>
        <button
          v-if="modeItem.source === 'database' || amountItem.source === 'database'"
          type="button"
          class="jt-link-button rule__reset"
          @click="emit('reset', amountItem.source === 'database' ? amountItem : modeItem)"
        >
          {{ t('settings.reset_default') }}
        </button>
      </div>
      <Tabs :model-value="mode" :tabs="modeTabs" :aria-label="t('bonus.commission_mode')" @update:model-value="setMode" />
      <div v-if="mode === 'fixed'" class="rule__row">
        <label class="amount amount--wide">
          <span class="rule__label">{{ t('bonus.commission_amount') }}</span>
          <TextInput :id="RULE_KEYS.fixedAmount" :model-value="amountDraft" inputmode="numeric" :aria-label="t('bonus.commission_amount')" :invalid="Boolean(errors[RULE_KEYS.fixedAmount])" @update:model-value="setFixedAmount" />
          <span class="amount__unit">₸</span>
        </label>
        <span class="jt-muted rule__range">{{ t('bonus.commission_range', { min: fmt.money(bounds.min), max: fmt.money(bounds.max) }) }}</span>
      </div>
      <p v-else class="jt-muted">{{ t('bonus.commission_percent_note') }}</p>
      <p v-if="errorOf(RULE_KEYS.fixedAmount) || errorOf(RULE_KEYS.mode)" class="rule__error" role="alert">{{ errorOf(RULE_KEYS.fixedAmount) || errorOf(RULE_KEYS.mode) }}</p>
      <div class="explain" role="note">
        <strong>{{ t('bonus.commission_how') }}</strong>
        <ul>
          <li>{{ t('bonus.commission_note_accept') }}</li>
          <li>{{ t('bonus.commission_note_no_refund') }}</li>
          <li>{{ t('bonus.commission_note_admin_refund') }}</li>
          <li>{{ t('bonus.commission_note_new_orders') }}</li>
          <li>{{ t('bonus.commission_note_balance') }}</li>
        </ul>
      </div>
    </section>

    <section v-if="auth.can('finance.manage')" class="rule rule--last">
      <div class="rule__head">
        <div>
          <h3 class="rule__title">{{ t('bonus.manual_title') }}</h3>
          <p class="jt-muted">{{ t('bonus.manual_hint') }}</p>
        </div>
        <AppButton @click="emit('grant')">{{ t('bonus.grant') }}</AppButton>
      </div>
    </section>
  </Card>
</template>

<style scoped>
.rule {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--jt-space-2);
  padding: var(--jt-space-3) 0 var(--jt-space-4);
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.rule:last-child,
.rule--last {
  border-bottom: 0;
  padding-bottom: 0;
}

.rule__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
  align-self: stretch;
}

.rule__title {
  font: var(--jt-font-body-strong);
}

.is-changed .rule__title::after {
  content: ' •';
  color: var(--jt-color-blue);
}

.rule__reset {
  font: var(--jt-font-small);
}

.rule__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--jt-space-4);
}

.rule__label {
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
  white-space: nowrap;
}

.rule__range {
  font: var(--jt-font-small);
}

.amount {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  width: 160px;
}

.amount--wide {
  width: auto;
}

.amount--wide :deep(.input) {
  width: 120px;
}

.amount__unit {
  color: var(--jt-color-label-secondary);
}

.rule__notes,
.explain ul {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-left: var(--jt-space-4);
  list-style: disc;
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.explain {
  display: flex;
  flex-direction: column;
  gap: 6px;
  align-self: stretch;
  padding: 12px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-fill-secondary);
}

.explain ul {
  color: var(--jt-color-label);
}

.rule__error {
  font: var(--jt-font-small);
  color: var(--jt-color-red-text);
}
</style>
