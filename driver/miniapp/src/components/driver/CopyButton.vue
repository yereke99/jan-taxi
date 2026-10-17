<!-- Capsule "Copy" button for a value the driver pastes into Kaspi (amount, BIN). -->
<script setup lang="ts">
import { Check, Copy } from '@lucide/vue'
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { copyText } from '../../composables/clipboard'
import { haptics } from '../../telegram'
import { toast } from '../ui'

const props = defineProps<{ value: string; label: string }>()

const { t } = useI18n()
const copied = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

async function copy(): Promise<void> {
  if (!(await copyText(props.value))) {
    toast.error(t('topup.copy_failed'))
    return
  }
  haptics.notification('success')
  copied.value = true
  clearTimeout(timer)
  timer = setTimeout(() => (copied.value = false), 2000)
}

onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <button type="button" class="copy" :aria-label="`${t('common.copy')}: ${label}`" @click.stop="copy">
    <Check v-if="copied" :size="16" :stroke-width="2.5" aria-hidden="true" />
    <Copy v-else :size="16" :stroke-width="2.25" aria-hidden="true" />
    <span>{{ copied ? t('common.copied') : t('common.copy') }}</span>
  </button>
</template>

<style scoped>
.copy {
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
  color: var(--jt-color-label);
}

.copy:active {
  opacity: var(--jt-opacity-pressed);
}
</style>
