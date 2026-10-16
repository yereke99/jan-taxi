<!--
  Up to four extra vehicle photos: thumbnails with a remove button and an
  "add" tile that downsizes and uploads the picked photo with progress.
-->
<script setup lang="ts">
import { Plus, RefreshCw, X } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FileRef, VehiclePhoto } from '../../../api/types'
import { ImageError } from '../../../features/media/image'
import { useUploadTask } from '../../../features/media/useUploadTask'
import { MAX_EXTRA_PHOTOS } from '../../../features/registration/form'
import { useDriverErrorText } from '../../../composables/useDriverErrorText'
import { useRegistrationStore } from '../../../stores/registration'
import { haptics } from '../../../telegram'
import { confirmDestructive, JSpinner, toast } from '../../ui'
import PrivateImage from '../PrivateImage.vue'

const props = defineProps<{ photos: readonly VehiclePhoto[]; locked: boolean }>()

const { t } = useI18n()
const errorText = useDriverErrorText()
const reg = useRegistrationStore()
const input = ref<HTMLInputElement | null>(null)
const removing = ref<string | null>(null)

const task = useUploadTask<FileRef>(
  (blob, onProgress, signal) => reg.upload('vehicle_extra', blob, onProgress, signal),
  () => {
    haptics.notification('success')
    task.reset()
  },
)

const busy = computed(() => task.state.value === 'preparing' || task.state.value === 'uploading')
const canAdd = computed(() => !props.locked && props.photos.length < MAX_EXTRA_PHOTOS)
const errorMessage = computed(() => {
  const err = task.error.value
  if (!err) return ''
  return err instanceof ImageError ? t(`registration.upload.${err.code}`) : errorText(err)
})

function onFile(e: Event): void {
  const el = e.target as HTMLInputElement
  const file = el.files?.[0]
  el.value = ''
  if (file) void task.run(file)
}

async function remove(photo: VehiclePhoto): Promise<void> {
  if (props.locked || removing.value) return
  const ok = await confirmDestructive({ title: t('registration.photos.remove_confirm'), confirmLabel: t('registration.photos.remove') })
  if (!ok) return
  removing.value = photo.id
  try {
    await reg.removeExtraPhoto(photo.id)
  } catch (err) {
    toast.error(errorText(err))
  } finally {
    removing.value = null
  }
}
</script>

<template>
  <div class="extra">
    <div class="extra__grid">
      <div v-for="p in photos" :key="p.id" class="extra__item">
        <PrivateImage :url="p.url" :alt="t('registration.photos.extra')" />
        <button
          v-if="!locked"
          type="button"
          class="extra__remove jt-glass"
          :aria-label="t('registration.photos.remove')"
          :disabled="removing === p.id"
          @click="remove(p)"
        >
          <JSpinner v-if="removing === p.id" size="small" />
          <X v-else :size="16" :stroke-width="2.5" aria-hidden="true" />
        </button>
      </div>

      <button v-if="canAdd || busy" type="button" class="extra__add" :disabled="busy" @click="input?.click()">
        <template v-if="busy">
          <img v-if="task.previewUrl.value" :src="task.previewUrl.value" alt="" class="extra__preview" />
          <span class="extra__progress">
            <JSpinner color="light" />
            <span class="jt-tabular">{{ Math.round(task.progress.value * 100) }}%</span>
          </span>
        </template>
        <template v-else>
          <Plus :size="26" :stroke-width="2" aria-hidden="true" />
          <span>{{ t('registration.photos.add_extra') }}</span>
        </template>
      </button>
    </div>

    <div v-if="task.state.value === 'error'" class="extra__error" role="alert">
      <span>{{ errorMessage || t('registration.upload.failed') }}</span>
      <button type="button" class="extra__retry" @click="task.retry()">
        <RefreshCw :size="16" :stroke-width="2.25" aria-hidden="true" />
        {{ t('registration.upload.retry') }}
      </button>
    </div>

    <input ref="input" class="jt-visually-hidden" type="file" accept="image/*" tabindex="-1" @change="onFile" />
  </div>
</template>

<style scoped>
.extra__grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--jt-space-2);
}

.extra__item,
.extra__add {
  position: relative;
  aspect-ratio: 4 / 3;
  border-radius: 14px;
  overflow: hidden;
}

.extra__remove {
  position: absolute;
  top: 6px;
  right: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 50%;
}

.extra__add {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--jt-space-1);
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-footnote);
  font-weight: 600;
  color: var(--jt-color-label);
}

.extra__add:active {
  opacity: var(--jt-opacity-pressed);
}

.extra__preview {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.extra__progress {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--jt-space-1);
  background: rgba(0, 0, 0, 0.45);
  color: #fff;
}

.extra__error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
  margin-top: var(--jt-space-2);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}

.extra__retry {
  display: inline-flex;
  align-items: center;
  gap: var(--jt-space-1);
  min-height: var(--jt-tap-target);
  font: var(--jt-font-subheadline);
  font-weight: 600;
  color: var(--jt-color-label);
}
</style>
