<!--
  An image from the driver's private files (fetched with the app token, §12).
  `previewUrl` (a local object URL) wins while a new upload is in progress.
-->
<script setup lang="ts">
import { ImageOff } from '@lucide/vue'
import { ref, watch } from 'vue'
import { privateImageUrl } from '../../api/files'
import { JSkeleton } from '../ui'

const props = withDefaults(defineProps<{ url?: string | null; previewUrl?: string | null; alt?: string; round?: boolean; pdf?: boolean }>(), {
  url: null,
  previewUrl: null,
  alt: '',
  round: false,
})

const src = ref<string | null>(null)
const failed = ref(false)
const loading = ref(false)
let seq = 0

watch(
  () => props.url,
  async (url) => {
    const mine = ++seq
    failed.value = false
    if (!url) {
      src.value = null
      return
    }
    loading.value = true
    try {
      const objectUrl = await privateImageUrl(url)
      if (mine === seq) src.value = objectUrl
    } catch {
      if (mine === seq) failed.value = true
    } finally {
      if (mine === seq) loading.value = false
    }
  },
  { immediate: true },
)
</script>

<template>
  <div class="pimg" :class="{ 'is-round': round }">
    <iframe v-if="pdf && src && !previewUrl" :src="src" :title="alt" class="pimg__img" />
    <img v-else-if="previewUrl || src" :src="previewUrl || src || undefined" :alt="alt" class="pimg__img" draggable="false" />
    <JSkeleton v-else-if="loading" width="100%" height="100%" :radius="round ? '50%' : '0'" />
    <span v-else-if="failed" class="pimg__fallback" role="img" :aria-label="alt">
      <ImageOff :size="22" :stroke-width="1.75" aria-hidden="true" />
    </span>
    <slot v-else name="empty" />
  </div>
</template>

<style scoped>
.pimg {
  position: relative;
  overflow: hidden;
  width: 100%;
  height: 100%;
  background: var(--jt-color-fill-secondary);
}

.pimg.is-round {
  border-radius: 50%;
}

.pimg__img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  -webkit-user-drag: none;
}

.pimg__fallback {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
  color: var(--jt-color-label-tertiary);
}
</style>
