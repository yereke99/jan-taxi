<!-- Renders confirmAction() requests. Mount once in App.vue. -->
<script setup lang="ts">
import { activeConfirm } from './confirm'
import ConfirmDialog from './ConfirmDialog.vue'
</script>

<template>
  <ConfirmDialog
    :open="activeConfirm !== null"
    :title="activeConfirm?.title ?? ''"
    :message="activeConfirm?.message"
    :confirm-label="activeConfirm?.confirmLabel"
    :cancel-label="activeConfirm?.cancelLabel"
    :destructive="activeConfirm?.destructive"
    @confirm="activeConfirm?.resolve(true)"
    @cancel="activeConfirm?.resolve(false)"
  />
</template>
