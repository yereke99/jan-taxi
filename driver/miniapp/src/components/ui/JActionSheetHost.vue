<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!-- Renders the request made through showActionSheet(). Mount once in App.vue. -->
<script setup lang="ts">
import { computed } from 'vue'
import { activeActionSheet } from './actionSheet'
import JActionSheet from './JActionSheet.vue'

const req = computed(() => activeActionSheet.value)

function onOpen(open: boolean): void {
  // Closing without a selection (backdrop, Escape, Cancel) resolves null.
  if (!open) req.value?.resolve(null)
}
</script>

<template>
  <JActionSheet
    :open="req !== null"
    :title="req?.title"
    :message="req?.message"
    :actions="req?.actions ?? []"
    :cancel-label="req?.cancelLabel"
    @select="(id) => req?.resolve(id)"
    @update:open="onOpen"
  />
</template>
