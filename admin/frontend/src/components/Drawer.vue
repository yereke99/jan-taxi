<!-- Right-side panel for details/edit forms. Esc and backdrop close it. -->
<script setup lang="ts">
import { X } from '@lucide/vue'
import { nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(defineProps<{ open: boolean; title: string; width?: string }>(), { width: '520px' })
const emit = defineEmits<{ 'update:open': [open: boolean]; close: [] }>()

const { t } = useI18n()
const titleId = useId()
const panel = ref<HTMLElement | null>(null)
let returnFocus: HTMLElement | null = null

function close(): void {
  emit('update:open', false)
  emit('close')
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Escape') close()
}

watch(
  () => props.open,
  async (open) => {
    if (open) {
      returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
      document.addEventListener('keydown', onKeydown)
      await nextTick()
      panel.value?.focus()
    } else {
      document.removeEventListener('keydown', onKeydown)
      returnFocus?.focus()
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="open" class="backdrop" aria-hidden="true" @click="close" />
    </Transition>
    <Transition name="slide">
      <aside
        v-if="open"
        ref="panel"
        class="drawer"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="titleId"
        tabindex="-1"
        :style="{ width }"
      >
        <header class="drawer__head">
          <h2 :id="titleId" class="drawer__title">{{ title }}</h2>
          <button type="button" class="drawer__close" :aria-label="t('common.close')" @click="close">
            <X :size="18" aria-hidden="true" />
          </button>
        </header>
        <div class="drawer__body"><slot /></div>
        <footer v-if="$slots.footer" class="drawer__foot"><slot name="footer" /></footer>
      </aside>
    </Transition>
  </Teleport>
</template>

<style scoped>
.backdrop {
  position: fixed;
  inset: 0;
  z-index: var(--jt-z-drawer);
  background: var(--jt-color-overlay);
}

.drawer {
  position: fixed;
  top: 8px;
  right: 8px;
  bottom: 8px;
  z-index: var(--jt-z-drawer);
  display: flex;
  flex-direction: column;
  max-width: calc(100vw - 16px);
  overflow: hidden;
  border-radius: var(--jt-radius-modal);
  background: var(--jt-color-bg);
  box-shadow: var(--jt-shadow-popover);
  outline: none;
}

.drawer__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
  height: var(--jt-topbar-height);
  padding: 0 var(--jt-space-4) 0 var(--jt-space-5);
  box-shadow: inset 0 calc(-1 * var(--jt-hairline)) 0 var(--jt-color-separator);
}

.drawer__title {
  font: var(--jt-font-headline);
}

.drawer__close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--jt-control-height);
  height: var(--jt-control-height);
  border-radius: var(--jt-radius-control);
  color: var(--jt-color-label-secondary);
}

.drawer__close:hover {
  background: var(--jt-color-hover);
}

.drawer__body {
  flex: 1;
  overflow-y: auto;
  padding: var(--jt-space-5);
}

.drawer__foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--jt-space-2);
  padding: var(--jt-space-3) var(--jt-space-5);
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.fade-enter-active,
.fade-leave-active {
  transition: opacity var(--jt-duration-base) var(--jt-ease);
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}

.slide-enter-active,
.slide-leave-active {
  transition: transform var(--jt-duration-base) var(--jt-ease);
}

.slide-enter-active {
  transition-timing-function: var(--jt-ease-spring);
}

.slide-enter-from,
.slide-leave-to {
  transform: translateX(calc(100% + 16px));
}
</style>
