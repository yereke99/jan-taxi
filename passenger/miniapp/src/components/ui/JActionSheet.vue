<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  iOS action sheet for confirmations and choices (§15). Controlled with
  v-model:open; emits select(id) or cancel. For one-off confirmations prefer
  showActionSheet()/confirmDestructive() with <JActionSheetHost /> in App.vue.
-->
<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { haptics } from '../../telegram/haptics'
import type { ActionSheetAction } from './types'

const props = withDefaults(
  defineProps<{
    open: boolean
    title?: string
    message?: string
    actions: ActionSheetAction[]
    cancelLabel?: string
  }>(),
  { title: '', message: '', cancelLabel: '' },
)

const emit = defineEmits<{
  'update:open': [open: boolean]
  select: [id: string]
  cancel: []
}>()

const { t } = useI18n()
const panel = ref<HTMLElement | null>(null)
let returnFocus: HTMLElement | null = null

function close(): void {
  emit('update:open', false)
}

function cancel(): void {
  emit('cancel')
  close()
}

function choose(a: ActionSheetAction): void {
  if (a.disabled) return
  if (a.destructive) haptics.impact('medium')
  else haptics.selection()
  emit('select', a.id)
  close()
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Escape') {
    e.preventDefault()
    cancel()
  }
}

watch(
  () => props.open,
  async (open) => {
    if (open) {
      returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
      document.addEventListener('keydown', onKeydown)
      await nextTick()
      panel.value?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus()
    } else {
      document.removeEventListener('keydown', onKeydown)
      returnFocus?.focus()
      returnFocus = null
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <Transition name="jt-as-fade">
      <div v-if="open" class="jt-as-backdrop" aria-hidden="true" @click="cancel" />
    </Transition>
    <Transition name="jt-as-slide">
      <div
        v-if="open"
        ref="panel"
        class="jt-as"
        role="dialog"
        aria-modal="true"
        :aria-label="title || message || undefined"
      >
        <div class="jt-as__group">
          <div v-if="title || message" class="jt-as__head">
            <p v-if="title" class="jt-as__title">{{ title }}</p>
            <p v-if="message" class="jt-as__message">{{ message }}</p>
          </div>
          <button
            v-for="a in actions"
            :key="a.id"
            type="button"
            class="jt-as__action"
            :class="{ 'is-destructive': a.destructive }"
            :disabled="a.disabled"
            @click="choose(a)"
          >
            {{ a.label }}
          </button>
        </div>
        <button type="button" class="jt-as__cancel" @click="cancel">{{ cancelLabel || t('common.cancel') }}</button>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.jt-as-backdrop {
  position: fixed;
  inset: 0;
  z-index: var(--jt-z-overlay);
  background: var(--jt-color-overlay);
}

.jt-as {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: var(--jt-z-overlay);
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  padding: 0 var(--jt-space-2) calc(var(--jt-safe-bottom) + var(--jt-space-2));
}

.jt-as__group,
.jt-as__cancel {
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  overflow: hidden;
}

.jt-as__head {
  padding: var(--jt-space-3) var(--jt-space-4);
  text-align: center;
  box-shadow: inset 0 calc(-1 * var(--jt-hairline)) 0 var(--jt-color-separator);
}

.jt-as__title {
  font: 600 13px/18px var(--jt-font-family);
  color: var(--jt-color-label-secondary);
}

.jt-as__message {
  margin-top: 2px;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.jt-as__action,
.jt-as__cancel {
  display: block;
  width: 100%;
  min-height: 57px;
  padding: var(--jt-space-2) var(--jt-space-4);
  font: 400 20px/25px var(--jt-font-family);
  color: var(--jt-color-label);
  text-align: center;
}

.jt-as__action:not(:last-child) {
  box-shadow: inset 0 calc(-1 * var(--jt-hairline)) 0 var(--jt-color-separator);
}

.jt-as__action.is-destructive {
  color: var(--jt-color-red);
}

.jt-as__action:disabled {
  color: var(--jt-color-label-tertiary);
}

.jt-as__action:active:not(:disabled),
.jt-as__cancel:active {
  background: var(--jt-color-fill-tertiary);
}

.jt-as__cancel {
  font-weight: 600;
}

.jt-as-fade-enter-active,
.jt-as-fade-leave-active {
  transition: opacity var(--jt-duration-base) var(--jt-ease);
}

.jt-as-fade-enter-from,
.jt-as-fade-leave-to {
  opacity: 0;
}

.jt-as-slide-enter-active,
.jt-as-slide-leave-active {
  transition: transform var(--jt-duration-slow) var(--jt-ease);
}

.jt-as-slide-enter-from,
.jt-as-slide-leave-to {
  transform: translateY(100%);
}
</style>
