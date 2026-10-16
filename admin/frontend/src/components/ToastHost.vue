<!-- Bottom-right toast stack. Mount once in App.vue. -->
<script setup lang="ts">
import { X } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { toast } from './toast'

const { t } = useI18n()
</script>

<template>
  <div class="toasts" aria-live="polite">
    <TransitionGroup name="toast">
      <div
        v-for="item in toast.items"
        :key="item.id"
        class="toast jt-glass"
        :class="`toast--${item.tone}`"
        :role="item.tone === 'error' ? 'alert' : 'status'"
      >
        <span class="toast__msg">{{ item.message }}</span>
        <button type="button" class="toast__close" :aria-label="t('common.close')" @click="toast.dismiss(item.id)">
          <X :size="14" aria-hidden="true" />
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>

<style scoped>
.toasts {
  position: fixed;
  right: var(--jt-space-5);
  bottom: var(--jt-space-5);
  z-index: var(--jt-z-toast);
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  width: 360px;
  max-width: calc(100vw - 2 * var(--jt-space-5));
}

.toast {
  display: flex;
  align-items: flex-start;
  gap: var(--jt-space-3);
  padding: 12px 12px 12px 16px;
  border-radius: var(--jt-radius-card);
  color: var(--jt-color-label);
  font: var(--jt-font-body);
}

.toast::before {
  content: '';
  flex: none;
  width: 8px;
  height: 8px;
  margin-top: 6px;
  border-radius: 50%;
  background: var(--jt-color-gray);
}

.toast--error::before {
  background: var(--jt-color-red);
}

.toast--success::before {
  background: var(--jt-color-green);
}

.toast__msg {
  flex: 1;
  overflow-wrap: anywhere;
}

.toast__close {
  display: inline-flex;
  padding: 2px;
  color: var(--jt-color-label-secondary);
}

.toast-enter-active,
.toast-leave-active {
  transition:
    opacity var(--jt-duration-base) var(--jt-ease),
    transform var(--jt-duration-base) var(--jt-ease);
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(8px) scale(0.96);
}
</style>
