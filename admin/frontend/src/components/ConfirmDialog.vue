<!-- Confirmation built on Modal; destructive actions get a red confirm button. -->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'
import Modal from './Modal.vue'

withDefaults(
  defineProps<{
    open: boolean
    title: string
    message?: string
    confirmLabel?: string
    cancelLabel?: string
    destructive?: boolean
    loading?: boolean
  }>(),
  { message: '', confirmLabel: '', cancelLabel: '', destructive: false, loading: false },
)

const emit = defineEmits<{ 'update:open': [open: boolean]; confirm: []; cancel: [] }>()
const { t } = useI18n()

function cancel(): void {
  emit('cancel')
  emit('update:open', false)
}
</script>

<template>
  <Modal :open="open" :title="title" role="alertdialog" :persistent="loading" @update:open="(v) => !v && cancel()">
    <p v-if="message" class="confirm__message">{{ message }}</p>
    <slot />
    <template #footer>
      <AppButton :disabled="loading" @click="cancel">{{ cancelLabel || t('common.cancel') }}</AppButton>
      <AppButton :variant="destructive ? 'destructive' : 'primary'" :loading="loading" @click="emit('confirm')">
        {{ confirmLabel || t('common.confirm') }}
      </AppButton>
    </template>
  </Modal>
</template>

<style scoped>
.confirm__message {
  color: var(--jt-color-label-secondary);
  white-space: pre-line;
}
</style>
