<!--
  Thumbnail of a private file (ID document, vehicle photo, receipt). The image
  is fetched with the session cookie into a blob: URL (useSecureImage); a
  click opens it in the viewer. Never pass file URLs to <img src> directly.
-->
<script setup lang="ts">
import { FileX2, Lock } from '@lucide/vue'
import { computed, toRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSecureImage } from '../composables/useSecureImage'
import { isApiError } from '../services/api'
import AppSpinner from './AppSpinner.vue'

const props = withDefaults(defineProps<{ url: string | null | undefined; label: string; height?: string; fit?: 'cover' | 'contain'; pdf?: boolean }>(), {
  height: '160px',
  fit: 'cover',
})
const emit = defineEmits<{ open: [] }>()

const { t } = useI18n()
const img = useSecureImage(toRef(props, 'url'))
const forbidden = computed(() => isApiError(img.error.value) && img.error.value.status === 403)
</script>

<template>
  <figure class="thumb" :style="{ height }">
    <iframe v-if="pdf && img.src.value" :src="img.src.value" :title="label" class="thumb__img" />
    <button
      v-else-if="img.src.value"
      type="button"
      class="thumb__button"
      :aria-label="`${label} — ${t('viewer.open_viewer')}`"
      @click="emit('open')"
    >
      <img :src="img.src.value" :alt="label" class="thumb__img" :style="{ objectFit: fit }" draggable="false" />
    </button>
    <div v-else class="thumb__placeholder">
      <AppSpinner v-if="img.loading.value" :size="18" />
      <template v-else-if="forbidden">
        <Lock :size="20" aria-hidden="true" />
        <span>{{ t('viewer.no_access') }}</span>
      </template>
      <template v-else-if="img.error.value">
        <FileX2 :size="20" aria-hidden="true" />
        <span>{{ t('viewer.unavailable') }}</span>
        <button type="button" class="jt-link-button" @click="img.reload()">{{ t('common.retry') }}</button>
      </template>
      <template v-else>
        <FileX2 :size="20" aria-hidden="true" />
        <span>{{ t('viewer.missing') }}</span>
      </template>
    </div>
    <figcaption class="thumb__caption">{{ label }}</figcaption>
  </figure>
</template>

<style scoped>
.thumb {
  position: relative;
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow: hidden;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-fill-secondary);
}

.thumb__button {
  flex: 1;
  min-height: 0;
  display: block;
  width: 100%;
  cursor: zoom-in;
}

.thumb__img {
  display: block;
  width: 100%;
  height: 100%;
  background: #f7f7f9;
}

.thumb__placeholder {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: var(--jt-space-3);
  color: var(--jt-color-label-secondary);
  font: var(--jt-font-small);
  text-align: center;
}

.thumb__caption {
  position: absolute;
  left: 8px;
  bottom: 8px;
  max-width: calc(100% - 16px);
  padding: 2px 8px;
  border-radius: var(--jt-radius-round);
  background: rgba(255, 255, 255, 0.88);
  font: 500 12px/18px var(--jt-font-family);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  pointer-events: none;
}
</style>
