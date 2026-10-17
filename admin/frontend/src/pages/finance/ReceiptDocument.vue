<!--
  A receipt file shown inline: the PDF in the browser's viewer, or an image.
  The file is fetched with the session cookie into a blob: URL
  (useSecureFile), never loaded from the API URL directly. Framing blob:
  needs `frame-src blob:` in the admin CSP; when the policy blocks it the
  viewer says so and the file still opens in a new tab or downloads.
-->
<script setup lang="ts">
import { Download, ExternalLink, FileX2, Lock } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, toRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppSpinner } from '../../components'
import { useSecureFile } from '../../composables/useSecureImage'
import { isApiError } from '../../services/api'

const props = withDefaults(defineProps<{ url: string | null | undefined; label: string; filename?: string; height?: string }>(), {
  filename: 'receipt',
  height: '560px',
})
const emit = defineEmits<{ 'open-image': [] }>()

const { t } = useI18n()
const file = useSecureFile(toRef(props, 'url'))
const isPdf = computed(() => file.type.value === 'application/pdf')
const isImage = computed(() => file.type.value.startsWith('image/'))
const forbidden = computed(() => isApiError(file.error.value) && file.error.value.status === 403)
const downloadName = computed(() => {
  const ext = isPdf.value ? 'pdf' : file.type.value === 'image/png' ? 'png' : file.type.value === 'image/webp' ? 'webp' : 'jpg'
  return `${props.filename.replace(/[^\w.-]+/g, '_')}.${ext}`
})

/** The page's Content-Security-Policy refused to frame the blob: URL. */
const frameBlocked = ref(false)
watch(file.src, () => (frameBlocked.value = false))

function onViolation(e: SecurityPolicyViolationEvent): void {
  const framing = ['frame-src', 'child-src', 'default-src'].includes(e.effectiveDirective)
  if (framing && isPdf.value && (e.blockedURI === 'blob' || e.blockedURI.startsWith('blob:'))) frameBlocked.value = true
}

onMounted(() => document.addEventListener('securitypolicyviolation', onViolation))
onBeforeUnmount(() => document.removeEventListener('securitypolicyviolation', onViolation))

function openTab(): void {
  if (file.src.value) window.open(file.src.value, '_blank', 'noopener')
}
</script>

<template>
  <figure class="doc">
    <div class="doc__frame" :style="{ height }">
      <div v-if="file.src.value && isPdf && frameBlocked" class="doc__placeholder" role="status">
        <FileX2 :size="20" aria-hidden="true" />
        <span>{{ t('finance.pdf_blocked') }}</span>
      </div>
      <iframe v-else-if="file.src.value && isPdf" :src="`${file.src.value}#view=FitH&navpanes=0`" :title="label" class="doc__pdf" />
      <button v-else-if="file.src.value && isImage" type="button" class="doc__image" :aria-label="`${label} — ${t('viewer.open_viewer')}`" @click="emit('open-image')">
        <img :src="file.src.value" :alt="label" draggable="false" />
      </button>
      <div v-else class="doc__placeholder" role="status">
        <template v-if="file.loading.value">
          <AppSpinner :size="18" />
          <span>{{ t('finance.file_loading') }}</span>
        </template>
        <template v-else-if="forbidden">
          <Lock :size="20" aria-hidden="true" />
          <span>{{ t('viewer.no_access') }}</span>
        </template>
        <template v-else-if="file.error.value || file.src.value">
          <FileX2 :size="20" aria-hidden="true" />
          <span>{{ t('finance.file_unavailable') }}</span>
          <button type="button" class="jt-link-button" @click="file.reload()">{{ t('common.retry') }}</button>
        </template>
        <template v-else>
          <FileX2 :size="20" aria-hidden="true" />
          <span>{{ t('viewer.missing') }}</span>
        </template>
      </div>
    </div>
    <figcaption v-if="file.src.value" class="doc__bar">
      <button type="button" class="jt-link-button doc__action" @click="openTab">
        <ExternalLink :size="14" aria-hidden="true" />{{ t('finance.open_file') }}
      </button>
      <a :href="file.src.value" :download="downloadName" class="jt-link-button doc__action">
        <Download :size="14" aria-hidden="true" />{{ t('finance.download_file') }}
      </a>
    </figcaption>
  </figure>
</template>

<style scoped>
.doc {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  min-width: 0;
}

.doc__frame {
  display: flex;
  overflow: hidden;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-fill-secondary);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.doc__pdf {
  flex: 1;
  width: 100%;
  height: 100%;
  border: 0;
  background: #fff;
}

.doc__image {
  flex: 1;
  display: block;
  cursor: zoom-in;
}

.doc__image img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: contain;
}

.doc__placeholder {
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

.doc__bar {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  align-items: center;
  gap: var(--jt-space-2) var(--jt-space-4);
  font: var(--jt-font-small);
}

.doc__action {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
</style>
