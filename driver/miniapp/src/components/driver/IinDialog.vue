<!--
  «ЖСН енгізіңіз» on the profile of a driver who has no IIN yet (registered
  before it was asked for): an iOS alert-style dialog with the IIN field,
  Cancel and Save (PUT /driver/profile/iin). Working never depends on it.
  With the keyboard up the dialog moves to the top so the field stays visible.
-->
<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDriverErrorText } from '../../composables/useDriverErrorText'
import { isValidIin } from '../../features/registration/iin'
import { isApiError } from '../../services/http'
import { useProfileStore } from '../../stores/profile'
import { haptics, useTelegramFullscreen } from '../../telegram'
import { JButton, toast } from '../ui'
import IinField from './registration/IinField.vue'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [open: boolean] }>()

const { t } = useI18n()
const errorText = useDriverErrorText()
const store = useProfileStore()
const tg = useTelegramFullscreen()
const titleId = useId()
const panel = ref<HTMLElement | null>(null)
const value = ref('')
const tried = ref(false)
const saving = ref(false)
const serverError = ref('')

function close(): void {
  if (!saving.value) emit('update:open', false)
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Escape') close()
}

watch(
  () => props.open,
  async (open) => {
    if (!open) {
      document.removeEventListener('keydown', onKeydown)
      return
    }
    value.value = ''
    tried.value = false
    serverError.value = ''
    document.addEventListener('keydown', onKeydown)
    await nextTick()
    panel.value?.querySelector('input')?.focus()
  },
)

watch(value, () => (serverError.value = ''))

onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown))

async function save(): Promise<void> {
  if (saving.value) return
  tried.value = true
  if (!isValidIin(value.value)) {
    haptics.notification('error')
    return
  }
  saving.value = true
  try {
    await store.saveIin(value.value)
    toast.success(t('profile.iin_saved'))
    emit('update:open', false)
  } catch (err) {
    if (isApiError(err) && err.code === 'conflict') {
      // Already stored (another device, or support): show what the profile holds now.
      toast.show(errorText(err))
      emit('update:open', false)
      void store.refresh()
    } else if (isApiError(err) && (err.code === 'iin_taken' || err.code === 'validation_failed')) {
      serverError.value = errorText(err)
      haptics.notification('error')
    } else {
      toast.error(errorText(err))
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Teleport to="body">
    <Transition name="jt-fade">
      <div v-if="open" class="iin-dlg" :class="{ 'is-keyboard': tg.state.keyboardOpen }">
        <div class="iin-dlg__backdrop" aria-hidden="true" @click="close" />
        <div ref="panel" class="iin-dlg__panel" role="dialog" aria-modal="true" :aria-labelledby="titleId">
          <h2 :id="titleId" class="iin-dlg__title">{{ t('profile.iin_title') }}</h2>
          <p class="iin-dlg__text">{{ t('profile.iin_text') }}</p>
          <IinField v-model="value" :label="t('profile.iin')" :show-errors="tried" :server-error="serverError" @enter="save" />
          <div class="iin-dlg__actions">
            <JButton variant="secondary" size="medium" block :disabled="saving" @click="close">{{ t('common.cancel') }}</JButton>
            <JButton size="medium" block :loading="saving" @click="save">{{ t('profile.iin_save') }}</JButton>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.iin-dlg {
  position: fixed;
  inset: 0;
  z-index: var(--jt-z-overlay);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: calc(var(--jt-safe-top) + var(--jt-space-4)) var(--jt-side-margin) calc(var(--jt-safe-bottom) + var(--jt-space-4));
}

/* The keyboard covers the lower half: keep the field above it. */
.iin-dlg.is-keyboard {
  align-items: flex-start;
}

.iin-dlg__backdrop {
  position: absolute;
  inset: 0;
  background: var(--jt-color-overlay);
}

.iin-dlg__panel {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  width: 100%;
  max-width: 360px;
  padding: var(--jt-space-5) var(--jt-space-4) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  box-shadow: var(--jt-shadow-floating);
}

.iin-dlg__title {
  font: var(--jt-font-headline);
  text-align: center;
}

.iin-dlg__text {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  text-align: center;
}

.iin-dlg__actions {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--jt-space-2);
  margin-top: var(--jt-space-1);
}
</style>
