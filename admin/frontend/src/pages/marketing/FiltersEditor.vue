<!--
  Typed audience conditions of a segment or a broadcast. Passenger and driver
  audiences show only their own fields; every field is optional (empty = no
  condition). Errors use the API's "filters.<field>" paths.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Language } from '../../api/common'
import { SEGMENT_DRIVER_STATUSES, type Audience } from '../../api/marketing'
import { FormField, SelectInput, TextInput } from '../../components'
import { useCities } from '../../composables/useCities'
import type { FiltersForm } from './segmentModel'

const props = withDefaults(defineProps<{ audience: Audience; modelValue: FiltersForm; errors?: Record<string, string> }>(), { errors: () => ({}) })
const emit = defineEmits<{ 'update:modelValue': [value: FiltersForm] }>()

const { t } = useI18n()
const { options: cityOptions } = useCities()
const LANGS: readonly Language[] = ['kk', 'ru', 'en']

function set<K extends keyof FiltersForm>(key: K, v: FiltersForm[K]): void {
  emit('update:modelValue', { ...props.modelValue, [key]: v })
}

function toggle(key: 'languages' | 'driver_statuses' | 'city_ids', value: string, on: boolean): void {
  const list = (props.modelValue[key] as string[]).filter((x) => x !== value)
  set(key, (on ? [...list, value] : list) as FiltersForm[typeof key])
}

const couponOptions = computed(() => [
  { value: 'yes' as const, label: t('marketing.f_coupon_yes') },
  { value: 'no' as const, label: t('marketing.f_coupon_no') },
])
</script>

<template>
  <div class="filters">
    <div class="group">
      <p class="group__label">{{ t('marketing.f_languages') }}</p>
      <div class="group__list">
        <label v-for="l in LANGS" :key="l" class="check">
          <input type="checkbox" :checked="modelValue.languages.includes(l)" @change="toggle('languages', l, ($event.target as HTMLInputElement).checked)" />
          <span>{{ t(`languages.${l}`) }}</span>
        </label>
      </div>
      <p class="group__hint">{{ t('marketing.f_languages_hint') }}</p>
    </div>

    <div class="jt-form-row">
      <FormField v-slot="f" :label="t('marketing.f_registered_within')" :error="errors['filters.registered_within_days']">
        <TextInput :id="f.id" :model-value="modelValue.registered_within_days" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('registered_within_days', v)" />
      </FormField>
      <FormField v-slot="f" :label="t('marketing.f_registered_before')" :error="errors['filters.registered_before_days']">
        <TextInput :id="f.id" :model-value="modelValue.registered_before_days" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('registered_before_days', v)" />
      </FormField>
    </div>
    <div class="jt-form-row">
      <FormField v-slot="f" :label="t('marketing.f_rides_min')" :error="errors['filters.completed_rides_min']">
        <TextInput :id="f.id" :model-value="modelValue.completed_rides_min" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('completed_rides_min', v)" />
      </FormField>
      <FormField v-slot="f" :label="t('marketing.f_rides_max')" :error="errors['filters.completed_rides_max']">
        <TextInput :id="f.id" :model-value="modelValue.completed_rides_max" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('completed_rides_max', v)" />
      </FormField>
    </div>

    <template v-if="audience === 'passengers'">
      <div class="jt-form-row">
        <FormField v-slot="f" :label="t('marketing.f_last_ride')" :error="errors['filters.last_ride_within_days']">
          <TextInput :id="f.id" :model-value="modelValue.last_ride_within_days" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('last_ride_within_days', v)" />
        </FormField>
        <FormField v-slot="f" :label="t('marketing.f_no_ride')" :error="errors['filters.no_ride_for_days']">
          <TextInput :id="f.id" :model-value="modelValue.no_ride_for_days" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('no_ride_for_days', v)" />
        </FormField>
      </div>
      <FormField v-slot="f" :label="t('marketing.f_coupon')" :error="errors['filters.has_active_coupon']">
        <SelectInput
          :id="f.id"
          :model-value="modelValue.has_active_coupon || null"
          :options="couponOptions"
          :placeholder="t('marketing.f_coupon_any')"
          @update:model-value="(v) => set('has_active_coupon', v ?? '')"
        />
      </FormField>
    </template>

    <template v-else>
      <div class="group">
        <p class="group__label">{{ t('marketing.f_driver_statuses') }}</p>
        <div class="group__list">
          <label v-for="s in SEGMENT_DRIVER_STATUSES" :key="s" class="check">
            <input type="checkbox" :checked="modelValue.driver_statuses.includes(s)" @change="toggle('driver_statuses', s, ($event.target as HTMLInputElement).checked)" />
            <span>{{ t(`moderation_status.${s}`) }}</span>
          </label>
        </div>
        <p class="group__hint">{{ t('marketing.f_driver_statuses_hint') }}</p>
      </div>
      <FormField v-slot="f" :label="t('marketing.f_online_within')" :error="errors['filters.online_within_days']">
        <TextInput :id="f.id" :model-value="modelValue.online_within_days" inputmode="numeric" :invalid="f.invalid" @update:model-value="(v) => set('online_within_days', v)" />
      </FormField>
      <div class="group">
        <p class="group__label">{{ t('marketing.f_cities') }}</p>
        <div class="group__list">
          <label v-for="c in cityOptions" :key="c.value" class="check">
            <input type="checkbox" :checked="modelValue.city_ids.includes(c.value)" @change="toggle('city_ids', c.value, ($event.target as HTMLInputElement).checked)" />
            <span>{{ c.label }}</span>
          </label>
        </div>
        <p class="group__hint" :class="{ 'is-error': errors['filters.city_ids'] }">{{ errors['filters.city_ids'] || t('marketing.f_cities_hint') }}</p>
      </div>
    </template>

    <p class="group__hint">{{ t('marketing.f_days_hint') }}</p>
    <p v-if="errors.filters" class="group__hint is-error" role="alert">{{ errors.filters }}</p>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.group__label {
  font: 500 13px/18px var(--jt-font-family);
}

.group__list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px var(--jt-space-4);
}

.group__hint {
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.group__hint.is-error {
  color: var(--jt-color-red);
}

.check {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.check input {
  accent-color: #000;
}
</style>
