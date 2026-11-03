<!--
  The registration photo (the driver's portrait): preview of the current
  server copy (or the photo just picked), Telegram / camera / gallery choices,
  client-side downscale to a JPEG, upload progress and retry. Locked fields
  show the photo without the pickers.
-->
<script setup lang="ts">
import { Camera, ImagePlus, Lock, RefreshCw } from '@lucide/vue'
import { computed, onScopeDispose, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FileRef, UploadKind } from '../../../api/types'
import { AVATAR_MAX_BYTES, ImageError } from '../../../features/media/image'
import { useUploadTask } from '../../../features/media/useUploadTask'
import { useDriverErrorText } from '../../../composables/useDriverErrorText'
import { useRegistrationStore } from '../../../stores/registration'
import { useSessionStore } from '../../../stores/session'
import { isAbortError, isApiError } from '../../../services/http'
import { getRawInitData, getTelegramPhotoUrl, haptics } from '../../../telegram'
import { JSpinner } from '../../ui'
import PrivateImage from '../PrivateImage.vue'

const props = withDefaults(
  defineProps<{
    kind: UploadKind
    file: FileRef | null
    label: string
    hint?: string
    locked?: boolean
    /** Round avatar preview instead of a 4:3 photo. */
    round?: boolean
    /** Camera to suggest for "take photo". */
    facing?: 'user' | 'environment'
  }>(),
  { hint: '', locked: false, round: false, facing: 'environment' },
)

const emit = defineEmits<{ uploaded: [file: FileRef] }>()

const { t } = useI18n()
const errorText = useDriverErrorText()
const registration = useRegistrationStore()
const session = useSessionStore()
const cameraInput = ref<HTMLInputElement | null>(null)
const galleryInput = ref<HTMLInputElement | null>(null)
const importing = ref(false)
const importError = shallowRef<unknown>(null)
let importController: AbortController | null = null
const telegramPhoto = computed(() => (props.kind === 'avatar' ? getTelegramPhotoUrl(session.me?.telegram_user_id) : null))

const task = useUploadTask<FileRef>(
  (blob, onProgress, signal) => registration.upload(props.kind, blob, onProgress, signal),
  (file) => {
    haptics.notification('success')
    emit('uploaded', file)
  },
  AVATAR_MAX_BYTES,
)

const busy = computed(() => importing.value || task.state.value === 'preparing' || task.state.value === 'uploading')
const hasError = computed(() => importError.value !== null || task.state.value === 'error')
const percent = computed(() => Math.round(task.progress.value * 100))
const progressLabel = computed(() => {
  if (importing.value) return t('registration.upload.telegram_importing')
  if (task.state.value === 'preparing') return t('registration.upload.preparing')
  return t('registration.upload.uploading', { percent: percent.value })
})

const errorMessage = computed(() => {
  const err = importError.value ?? task.error.value
  if (!err) return ''
  if (isApiError(err) && (err.code === 'telegram_photo_unavailable' || err.code === 'telegram_photo_failed')) {
    return t(`registration.upload.${err.code}`)
  }
  if (err instanceof ImageError) return t(`registration.upload.${err.code}`)
  return errorText(err)
})

function pick(input: HTMLInputElement | null): void {
  if (props.locked || busy.value) return
  input?.click()
}

function onFile(e: Event): void {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  // Reset so picking the same file again still fires "change".
  input.value = ''
  if (file && !props.locked && !busy.value) {
    importError.value = null
    void task.run(file)
  }
}

async function useTelegramPhoto(): Promise<void> {
  if (props.locked || busy.value || !telegramPhoto.value) return
  const initData = getRawInitData()
  if (!initData) return
  task.reset()
  importError.value = null
  const controller = new AbortController()
  importController = controller
  importing.value = true
  try {
    const file = await registration.useTelegramAvatar(initData, controller.signal)
    if (controller.signal.aborted) return
    haptics.notification('success')
    emit('uploaded', file)
  } catch (err) {
    if (!controller.signal.aborted && !isAbortError(err)) importError.value = err
  } finally {
    if (importController === controller) {
      importController = null
      importing.value = false
    }
  }
}

function retry(): void {
  if (props.locked || busy.value) return
  if (importError.value) void useTelegramPhoto()
  else void task.retry()
}

onScopeDispose(() => importController?.abort())
</script>

<template>
  <div class="photo" :class="{ 'is-round': round, 'is-locked': locked, 'has-error': hasError }">
    <div class="photo__head">
      <span class="photo__label">{{ label }}</span>
      <Lock v-if="locked" :size="15" :stroke-width="2" class="photo__lock" :aria-label="t('registration.locked')" />
    </div>

    <div class="photo__frame">
      <PrivateImage :url="file?.url ?? null" :preview-url="task.previewUrl.value" :alt="label" :round="round">
        <template #empty>
          <button type="button" class="photo__empty" :disabled="locked || busy" :aria-label="t('registration.personal.take_photo')" @click="pick(cameraInput)">
            <Camera :size="round ? 28 : 32" :stroke-width="1.6" aria-hidden="true" />
          </button>
        </template>
      </PrivateImage>
      <div v-if="busy" class="photo__overlay" role="status" aria-live="polite">
        <JSpinner color="light" size="large" />
        <span class="photo__progress jt-tabular">
          {{ progressLabel }}
        </span>
        <span v-if="!importing" class="photo__bar" :style="{ transform: `scaleX(${task.progress.value})` }" />
      </div>
    </div>

    <p v-if="hint && !locked" class="photo__hint">{{ hint }}</p>
    <p v-if="locked" class="photo__hint">{{ t('registration.locked') }}</p>

    <div v-if="hasError" class="photo__error" role="alert">
      <span>{{ errorMessage || t('registration.upload.failed') }}</span>
      <button v-if="!locked" type="button" class="photo__retry" :disabled="busy" @click="retry">
        <RefreshCw :size="16" :stroke-width="2.25" aria-hidden="true" />
        {{ t('registration.upload.retry') }}
      </button>
    </div>

    <div v-if="!locked" class="photo__actions">
      <button v-if="telegramPhoto" type="button" class="photo__action photo__action--telegram" :disabled="busy" @click="useTelegramPhoto">
        <img :src="telegramPhoto" class="photo__telegram-avatar" alt="" draggable="false" referrerpolicy="no-referrer" />
        {{ t('registration.personal.use_telegram_photo') }}
      </button>
      <button type="button" class="photo__action" :disabled="busy" @click="pick(cameraInput)">
        <Camera :size="18" :stroke-width="2" aria-hidden="true" />
        {{ t('registration.personal.take_photo') }}
      </button>
      <button type="button" class="photo__action" :disabled="busy" @click="pick(galleryInput)">
        <ImagePlus :size="18" :stroke-width="2" aria-hidden="true" />
        {{ t('registration.personal.choose_photo') }}
      </button>
    </div>

    <input ref="cameraInput" class="jt-visually-hidden" type="file" accept="image/*" :capture="facing" :disabled="locked || busy" tabindex="-1" @change="onFile" />
    <input ref="galleryInput" class="jt-visually-hidden" type="file" accept="image/*" :disabled="locked || busy" tabindex="-1" @change="onFile" />
  </div>
</template>

<style scoped>
.photo {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

.photo__head {
  display: flex;
  align-items: center;
  gap: var(--jt-space-1);
  font: var(--jt-font-headline);
}

.photo__lock {
  color: var(--jt-color-label-secondary);
}

.photo__frame {
  position: relative;
  width: 100%;
  aspect-ratio: 4 / 3;
  border-radius: var(--jt-radius-card);
  overflow: hidden;
  background: var(--jt-color-fill-secondary);
}

.photo.is-round .photo__frame {
  width: 128px;
  height: 128px;
  aspect-ratio: 1;
  border-radius: 50%;
  align-self: center;
}

.photo__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
  color: var(--jt-color-label-tertiary);
}

.photo__overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--jt-space-2);
  background: rgba(0, 0, 0, 0.45);
  color: #fff;
}

.photo__progress {
  font: var(--jt-font-footnote);
}

.photo__bar {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 3px;
  background: #fff;
  transform-origin: left center;
  transition: transform var(--jt-duration-fast) linear;
}

.photo__hint {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.photo.is-round .photo__hint,
.photo.is-round .photo__head {
  justify-content: center;
  text-align: center;
}

.photo__error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}

.photo__retry {
  display: inline-flex;
  align-items: center;
  gap: var(--jt-space-1);
  min-height: var(--jt-tap-target);
  font: var(--jt-font-subheadline);
  font-weight: 600;
  color: var(--jt-color-label);
}

.photo__actions {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--jt-space-2);
}

.photo__action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--jt-space-2);
  height: var(--jt-button-height-medium);
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-subheadline);
  font-weight: 600;
}

.photo__action:active {
  opacity: var(--jt-opacity-pressed);
}

.photo__action--telegram {
  grid-column: 1 / -1;
}

.photo__telegram-avatar {
  flex: none;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  object-fit: cover;
}

.photo__action:disabled {
  opacity: var(--jt-opacity-disabled);
}
</style>
