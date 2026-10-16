<!-- A value the driver copies into Kaspi (amount, payment code) with a copy button. -->
<script setup lang="ts">
import { Check, Copy } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { copyText } from '../../composables/clipboard'
import { haptics } from '../../telegram'
import { toast } from '../ui'

const props = withDefaults(defineProps<{ label: string; value: string; copyValue?: string; large?: boolean }>(), {
  copyValue: undefined,
  large: false,
})

const { t } = useI18n()
const copied = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

async function copy(): Promise<void> {
  const ok = await copyText(props.copyValue ?? props.value)
  if (!ok) {
    toast.error(t('topup.copy_failed'))
    return
  }
  haptics.notification('success')
  copied.value = true
  clearTimeout(timer)
  timer = setTimeout(() => (copied.value = false), 2000)
}
</script>

<template>
  <div class="cfield" :class="{ 'is-large': large }">
    <div class="cfield__head">
      <p class="cfield__label">{{ label }}</p>
      <button type="button" class="cfield__btn" :aria-label="`${t('common.copy')}: ${label}`" @click="copy">
        <Check v-if="copied" :size="16" :stroke-width="2.5" aria-hidden="true" />
        <Copy v-else :size="16" :stroke-width="2.25" aria-hidden="true" />
        <span>{{ copied ? t('common.copied') : t('common.copy') }}</span>
      </button>
    </div>
    <p class="cfield__value jt-tabular">{{ value }}</p>
  </div>
</template>

<style scoped>
.cfield {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--jt-space-3) var(--jt-space-4);
}

.cfield + .cfield {
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.cfield__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
}

.cfield__label {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.cfield__value {
  font: var(--jt-font-title3);
  overflow-wrap: anywhere;
  user-select: all;
  -webkit-user-select: all;
}

.cfield.is-large .cfield__value {
  font: var(--jt-font-title2);
  letter-spacing: 0.3px;
}

.cfield__btn {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 var(--jt-space-3);
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-footnote);
  font-weight: 600;
}

.cfield__btn:active {
  opacity: var(--jt-opacity-pressed);
}
</style>
