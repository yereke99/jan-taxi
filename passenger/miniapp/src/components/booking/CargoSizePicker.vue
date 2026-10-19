<!--
  Cargo size of a parcel / other order (ARCHITECTURE §21.5): an iOS segmented
  control S / M / L / XL and, under it, what fits that size and its volume in
  litres (from the public config).
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { cargoSizeLabel, formatCount } from '../../composables/useVehicleText'
import { cargoVolume } from '../../domain/vehicleClass'
import type { CargoSize, CargoSizeInfo } from '../../services/config'
import type { Locale } from '../../utils/format'
import { JSegmented, type SegmentedOption } from '../ui'

const props = withDefaults(defineProps<{ modelValue: CargoSize; sizes: CargoSizeInfo[]; locale: Locale; disabled?: boolean }>(), {
  disabled: false,
})
const emit = defineEmits<{ 'update:modelValue': [value: CargoSize] }>()

const { t } = useI18n()

const options = computed<SegmentedOption<CargoSize>[]>(() => props.sizes.map((s) => ({ value: s.code, label: cargoSizeLabel(s.code) })))
const hint = computed(() =>
  [t(`cargo_hint.${props.modelValue}`), t('cargo_hint.volume', { n: formatCount(cargoVolume(props.modelValue, props.sizes), props.locale) })].join(' · '),
)
</script>

<template>
  <div class="cargo-size">
    <div class="cargo-size__head">
      <span class="cargo-size__label">{{ t('confirm.cargo_size') }}</span>
    </div>
    <JSegmented
      :model-value="modelValue"
      :options="options"
      :aria-label="t('confirm.cargo_size')"
      :disabled="disabled"
      @update:model-value="(v) => emit('update:modelValue', v)"
    />
    <p class="cargo-size__hint" aria-live="polite">{{ hint }}</p>
  </div>
</template>

<style scoped>
.cargo-size {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  padding: var(--jt-space-3) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
}

.cargo-size__label {
  font: var(--jt-font-body);
}

.cargo-size__hint {
  min-height: 18px;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}
</style>
