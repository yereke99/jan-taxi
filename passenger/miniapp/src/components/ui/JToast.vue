<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  Top banner toasts from toast.ts on the Liquid Glass material (§15.1). Mount
  once in App.vue. Tap a banner to dismiss it.
-->
<script setup lang="ts">
import { CircleAlert, CircleCheck } from '@lucide/vue'
import { toast } from './toast'

function runAction(id: number): void {
  toast.items.find((t) => t.id === id)?.action?.run()
  toast.dismiss(id)
}
</script>

<template>
  <div class="jt-toasts" aria-live="polite" aria-atomic="false">
    <TransitionGroup name="jt-toast">
      <div
        v-for="item in toast.items"
        :key="item.id"
        class="jt-toast jt-glass"
        :class="`jt-toast--${item.tone}`"
        :role="item.tone === 'error' ? 'alert' : 'status'"
        @click="toast.dismiss(item.id)"
      >
        <CircleAlert v-if="item.tone === 'error'" class="jt-toast__icon" :size="20" aria-hidden="true" />
        <CircleCheck v-else-if="item.tone === 'success'" class="jt-toast__icon" :size="20" aria-hidden="true" />
        <span class="jt-toast__message">{{ item.message }}</span>
        <button v-if="item.action" type="button" class="jt-toast__action" @click.stop="runAction(item.id)">
          {{ item.action.label }}
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>

<style scoped>
.jt-toasts {
  position: fixed;
  top: calc(var(--jt-safe-top) + var(--jt-space-2));
  left: calc(var(--jt-safe-left) + var(--jt-space-2));
  right: calc(var(--jt-safe-right) + var(--jt-space-2));
  z-index: var(--jt-z-toast);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--jt-space-2);
  pointer-events: none;
}

.jt-toast {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  width: 100%;
  max-width: 480px;
  min-height: var(--jt-tap-target);
  padding: var(--jt-space-3) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  color: var(--jt-color-label);
  font: 500 15px/20px var(--jt-font-family);
  pointer-events: auto;
  cursor: pointer;
}

.jt-toast__icon {
  flex: none;
}

.jt-toast--error .jt-toast__icon {
  color: var(--jt-color-red);
}

.jt-toast__action:active {
  opacity: var(--jt-opacity-pressed);
}

.jt-toast--success .jt-toast__icon {
  color: var(--jt-color-green);
}

.jt-toast__message {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
}

.jt-toast__action {
  flex: 0 1 auto;
  max-width: 40%;
  min-height: var(--jt-tap-target);
  margin: -12px -8px -12px 0;
  padding: 0 var(--jt-space-2);
  font: 600 15px/20px var(--jt-font-family);
  color: var(--jt-color-label);
  text-align: right;
  overflow-wrap: anywhere;
}

.jt-toast-enter-active,
.jt-toast-leave-active {
  transition:
    transform var(--jt-duration-base) var(--jt-ease),
    opacity var(--jt-duration-base) var(--jt-ease);
}

.jt-toast-enter-from,
.jt-toast-leave-to {
  transform: translateY(-12px) scale(0.96);
  opacity: 0;
}

@media (prefers-reduced-motion: reduce) {
  .jt-toast-enter-active,
  .jt-toast-leave-active {
    transition: opacity var(--jt-duration-fast) linear;
  }
}
</style>
