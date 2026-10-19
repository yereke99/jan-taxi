<!--
  Modal bottom panel in the iOS action-sheet style (§15): a rounded white card
  8px from the screen edges over a dimmed backdrop. It stays above the
  on-screen keyboard (--jt-keyboard) and below the top safe area; when the
  content is taller than the room left, the content scrolls and the footer
  stays visible. Backdrop tap and Escape emit close (unless `locked`).
-->
<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'

const props = withDefaults(defineProps<{ open: boolean; label: string; locked?: boolean }>(), { locked: false })
const emit = defineEmits<{ close: [] }>()

const panel = ref<HTMLElement | null>(null)
let returnFocus: HTMLElement | null = null

function requestClose(): void {
  if (!props.locked) emit('close')
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Escape') {
    e.preventDefault()
    requestClose()
  }
}

/** Keeps a focused field visible above the keyboard once it has opened. */
function onFocusIn(e: FocusEvent): void {
  const el = e.target as HTMLElement | null
  if (!el || !/^(INPUT|TEXTAREA)$/.test(el.tagName)) return
  // The whole field (label, input, counter and error) once the keyboard has settled.
  const field = el.closest<HTMLElement>('.jt-area, .jt-field') ?? el
  setTimeout(() => field.scrollIntoView({ block: 'nearest', behavior: 'smooth' }), 320)
}

watch(
  () => props.open,
  async (open) => {
    if (open) {
      returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
      document.addEventListener('keydown', onKeydown)
      await nextTick()
      panel.value?.focus({ preventScroll: true })
    } else {
      document.removeEventListener('keydown', onKeydown)
      returnFocus?.focus({ preventScroll: true })
      returnFocus = null
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <Transition name="jt-modal-fade">
      <div v-if="open" class="jt-modal-backdrop" aria-hidden="true" @click="requestClose" />
    </Transition>
    <Transition name="jt-modal-slide">
      <div v-if="open" class="jt-modal" @click.self="requestClose">
        <div ref="panel" class="jt-modal__panel" role="dialog" aria-modal="true" :aria-label="label" tabindex="-1" @focusin="onFocusIn">
          <div class="jt-modal__body">
            <slot />
          </div>
          <div v-if="$slots.footer" class="jt-modal__footer">
            <slot name="footer" />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.jt-modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: var(--jt-z-overlay);
  background: var(--jt-color-overlay);
}

.jt-modal {
  position: fixed;
  inset: 0;
  z-index: var(--jt-z-overlay);
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  padding: calc(var(--jt-safe-top) + var(--jt-space-2)) var(--jt-float-inset)
    calc(max(var(--jt-keyboard), var(--jt-safe-bottom)) + var(--jt-float-inset));
  pointer-events: auto;
}

.jt-modal__panel {
  display: flex;
  flex-direction: column;
  max-height: 100%;
  min-height: 0;
  border-radius: var(--jt-radius-sheet);
  background: var(--jt-color-bg);
  box-shadow: var(--jt-shadow-sheet);
  overflow: hidden;
  outline: none;
}

.jt-modal__body {
  flex: 1 1 auto;
  min-height: 0;
  padding: var(--jt-space-5) var(--jt-space-4) var(--jt-space-3);
  overflow-y: auto;
  overscroll-behavior: contain;
  -webkit-overflow-scrolling: touch;
}

.jt-modal__footer {
  flex: none;
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  padding: var(--jt-space-2) var(--jt-space-4) var(--jt-space-4);
}

.jt-modal-fade-enter-active,
.jt-modal-fade-leave-active {
  transition: opacity var(--jt-duration-base) var(--jt-ease);
}

.jt-modal-fade-enter-from,
.jt-modal-fade-leave-to {
  opacity: 0;
}

.jt-modal-slide-enter-active,
.jt-modal-slide-leave-active {
  transition: transform var(--jt-duration-slow) var(--jt-ease);
}

.jt-modal-slide-enter-from,
.jt-modal-slide-leave-to {
  transform: translateY(100%);
}
</style>
