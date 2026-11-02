<!--
  Typed rule fields of one campaign template with plain-language help; the
  ranges match marketing/rules.go and errors use its "rules.<field>" paths.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { UNLOCK_POLICIES, type CampaignTemplate, type Segment } from '../../api/marketing'
import { FormField, SelectInput, TextInput } from '../../components'
import { CATEGORIES, type Category } from '../../utils/status'
import type { RulesForm } from './campaignModel'

const props = withDefaults(
  defineProps<{ template: CampaignTemplate; modelValue: RulesForm; errors?: Record<string, string>; segments?: Segment[]; disabled?: boolean }>(),
  { errors: () => ({}), segments: () => [], disabled: false },
)
const emit = defineEmits<{ 'update:modelValue': [value: RulesForm] }>()

const { t } = useI18n()

function set<K extends keyof RulesForm>(key: K, v: RulesForm[K]): void {
  emit('update:modelValue', { ...props.modelValue, [key]: v })
}

function toggleCategory(c: Category, on: boolean): void {
  const list = props.modelValue.categories.filter((x) => x !== c)
  set('categories', on ? [...list, c] : list)
}

const unlockOptions = computed(() => UNLOCK_POLICIES.map((u) => ({ value: u, label: t(`marketing.unlock_${u}`) })))
const segmentOptions = computed(() =>
  props.segments.filter((s) => s.audience === 'passengers' && (!s.archived_at || s.id === props.modelValue.segment_id)).map((s) => ({ value: s.id, label: s.name })),
)
const withCategories = computed(() => ['first_ride_coupon', 'referral_first_ride', 'driver_milestone_bonus'].includes(props.template))
</script>

<template>
  <fieldset class="rules" :disabled="disabled">
    <template v-if="template === 'first_ride_coupon'">
      <div class="jt-form-row">
        <FormField v-slot="f" :label="t('marketing.f_amount')" :hint="t('marketing.f_amount_hint')" :error="errors['rules.amount']" required>
          <TextInput :id="f.id" :model-value="modelValue.amount" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('amount', v)" />
        </FormField>
        <FormField v-slot="f" :label="t('marketing.f_validity_hours')" :hint="t('marketing.f_validity_hint')" :error="errors['rules.validity_hours']" required>
          <TextInput :id="f.id" :model-value="modelValue.validity_hours" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('validity_hours', v)" />
        </FormField>
      </div>
      <FormField v-slot="f" :label="t('marketing.f_min_order_fare')" :hint="t('marketing.f_min_order_fare_hint')" :error="errors['rules.min_order_fare']" required>
        <TextInput :id="f.id" :model-value="modelValue.min_order_fare" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('min_order_fare', v)" />
      </FormField>
    </template>

    <template v-else-if="template === 'referral_first_ride'">
      <FormField v-slot="f" :label="t('marketing.f_reward_amount')" :hint="t('marketing.f_reward_hint')" :error="errors['rules.reward_amount']" required>
        <TextInput :id="f.id" :model-value="modelValue.reward_amount" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('reward_amount', v)" />
      </FormField>
      <FormField v-slot="f" :label="t('marketing.f_unlock')" :hint="t('marketing.f_unlock_hint')" :error="errors['rules.unlock']" required>
        <SelectInput :id="f.id" :model-value="modelValue.unlock" :options="unlockOptions" :invalid="f.invalid" @update:model-value="(v) => v && set('unlock', v)" />
      </FormField>
      <div class="jt-form-row">
        <FormField v-slot="f" :label="t('marketing.f_attribution_window')" :hint="t('marketing.f_attribution_hint')" :error="errors['rules.attribution_window_minutes']" required>
          <TextInput :id="f.id" :model-value="modelValue.attribution_window_minutes" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('attribution_window_minutes', v)" />
        </FormField>
        <FormField v-slot="f" :label="t('marketing.f_min_ride_seconds')" :hint="t('marketing.f_min_ride_hint')" :error="errors['rules.min_ride_seconds']" required>
          <TextInput :id="f.id" :model-value="modelValue.min_ride_seconds" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('min_ride_seconds', v)" />
        </FormField>
      </div>
    </template>

    <template v-else-if="template === 'passenger_credit'">
      <FormField v-slot="f" :label="t('marketing.f_bonus_amount')" :hint="t('marketing.f_credit_hint')" :error="errors['rules.amount']" required>
        <TextInput :id="f.id" :model-value="modelValue.amount" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('amount', v)" />
      </FormField>
      <FormField v-slot="f" :label="t('marketing.f_segment')" :hint="t('marketing.f_segment_hint')" :error="errors['rules.segment_id']" required>
        <SelectInput
          :id="f.id"
          :model-value="modelValue.segment_id || null"
          :options="segmentOptions"
          :placeholder="t('marketing.f_segment_none')"
          :invalid="f.invalid"
          @update:model-value="(v) => set('segment_id', v ?? '')"
        />
      </FormField>
    </template>

    <template v-else-if="template === 'driver_milestone_bonus'">
      <div class="jt-form-row">
        <FormField v-slot="f" :label="t('marketing.f_bonus_amount')" :hint="t('marketing.f_milestone_hint')" :error="errors['rules.amount']" required>
          <TextInput :id="f.id" :model-value="modelValue.amount" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('amount', v)" />
        </FormField>
        <FormField v-slot="f" :label="t('marketing.f_rides')" :hint="t('marketing.f_rides_hint')" :error="errors['rules.rides']" required>
          <TextInput :id="f.id" :model-value="modelValue.rides" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('rides', v)" />
        </FormField>
      </div>
    </template>

    <template v-else-if="template === 'manual_grant'">
      <FormField v-slot="f" :label="t('marketing.f_max_amount')" :hint="t('marketing.f_max_amount_hint')" :error="errors['rules.max_amount']" required>
        <TextInput :id="f.id" :model-value="modelValue.max_amount" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('max_amount', v)" />
      </FormField>
    </template>

    <p v-else class="jt-muted">{{ t('marketing.form_no_rules') }}</p>

    <div v-if="withCategories" class="cats">
      <p class="cats__label">{{ t('marketing.f_categories') }} <span class="cats__required" aria-hidden="true">*</span></p>
      <div class="cats__list">
        <label v-for="c in CATEGORIES" :key="c" class="cats__item">
          <input type="checkbox" :checked="modelValue.categories.includes(c)" @change="toggleCategory(c, ($event.target as HTMLInputElement).checked)" />
          <span>{{ t(`category.${c}`) }}</span>
        </label>
      </div>
      <p class="cats__hint" :class="{ 'is-error': errors['rules.categories'] }" :role="errors['rules.categories'] ? 'alert' : undefined">
        {{ errors['rules.categories'] || t('marketing.f_categories_hint') }}
      </p>
    </div>
    <!-- Errors about the rules as a whole (e.g. locked_after_activation). -->
    <p v-if="errors.rules" class="cats__hint is-error" role="alert">{{ errors.rules }}</p>
  </fieldset>
</template>

<style scoped>
.rules {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.cats {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.cats__label {
  font: 500 13px/18px var(--jt-font-family);
}

.cats__required {
  color: var(--jt-color-red);
}

.cats__list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px var(--jt-space-4);
}

.cats__item {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.cats__item input {
  accent-color: #000;
}

.cats__hint {
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.cats__hint.is-error {
  color: var(--jt-color-red);
}
</style>
