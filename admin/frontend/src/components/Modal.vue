<!-- Centred dialog. Esc and backdrop close it unless `persistent`. -->
<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'

const props = withDefaults(
  defineProps<{ open: boolean; title: string; width?: string; persistent?: boolean; role?: 'dialog' | 'alertdialog' }>(),
  { width: '440px', persistent: false, role: 'dialog' },
)
const emit = defineEmits<{ 'update:open': [open: boolean]; close: [] }>()

const titleId = useId()
const panel = ref<HTMLElement | null>(null)
let returnFocus: HTMLElement | null = null

function close(): void {
  if (props.persistent) return
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
      const first = panel.value?.querySelector<HTMLElement>('input, select, textarea, button:not([disabled])')
      ;(first ?? panel.value)?.focus()
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
    <Transition name="modal">
      <div v-if="open" class="overlay" @click.self="close">
        <div
          ref="panel"
          class="modal"
          :role="role"
          aria-modal="true"
          :aria-labelledby="titleId"
          tabindex="-1"
          :style="{ width }"
        >
          <h2 :id="titleId" class="modal__title">{{ title }}</h2>
          <div class="modal__body"><slot /></div>
          <footer v-if="$slots.footer" class="modal__foot"><slot name="footer" /></footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  z-index: var(--jt-z-modal);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--jt-space-6);
  background: var(--jt-color-overlay);
}

.modal {
  max-width: 100%;
  max-height: 100%;
  overflow-y: auto;
  padding: var(--jt-space-6);
  border-radius: var(--jt-radius-modal);
  background: var(--jt-color-bg);
  box-shadow: var(--jt-shadow-popover);
  outline: none;
}

.modal__title {
  font: var(--jt-font-headline);
  font-size: 17px;
}

.modal__body {
  margin-top: var(--jt-space-3);
  color: var(--jt-color-label);
}

.modal__foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--jt-space-2);
  margin-top: var(--jt-space-5);
}

.modal-enter-active,
.modal-leave-active {
  transition: opacity var(--jt-duration-base) var(--jt-ease);
}

.modal-enter-active .modal,
.modal-leave-active .modal {
  transition: transform var(--jt-duration-base) var(--jt-ease);
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}

.modal-enter-from .modal,
.modal-leave-to .modal {
  transform: scale(0.96);
}
</style>
