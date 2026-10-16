<!--
  Full-screen viewer for private images: zoom (buttons, +/−, wheel), rotate
  (R), drag to pan, ←/→ between images, Esc to close. Images are loaded as
  blob: URLs through the API client.
-->
<script setup lang="ts">
import { ChevronLeft, ChevronRight, Maximize, RotateCw, X, ZoomIn, ZoomOut } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSecureImage } from '../composables/useSecureImage'
import AppSpinner from './AppSpinner.vue'
import type { ViewerImage } from './types'

const props = defineProps<{ images: ViewerImage[]; index: number | null }>()
const emit = defineEmits<{ 'update:index': [value: number | null] }>()

const { t } = useI18n()
const scale = ref(1)
const rotation = ref(0)
const offset = ref({ x: 0, y: 0 })
const root = ref<HTMLElement | null>(null)
let drag: { x: number; y: number; ox: number; oy: number } | null = null
let returnFocus: HTMLElement | null = null

const current = computed(() => (props.index === null ? null : (props.images[props.index] ?? null)))
const url = computed(() => current.value?.url ?? null)
const img = useSecureImage(url)

function resetView(): void {
  scale.value = 1
  rotation.value = 0
  offset.value = { x: 0, y: 0 }
}

function close(): void {
  emit('update:index', null)
}

function go(step: number): void {
  if (props.index === null || props.images.length < 2) return
  const n = props.images.length
  emit('update:index', (props.index + step + n) % n)
}

function zoom(factor: number): void {
  scale.value = Math.min(8, Math.max(0.25, Math.round(scale.value * factor * 100) / 100))
}

function rotate(): void {
  rotation.value = (rotation.value + 90) % 360
}

function onKey(e: KeyboardEvent): void {
  if (props.index === null) return
  switch (e.key) {
    case 'Escape':
      close()
      break
    case 'ArrowRight':
      go(1)
      break
    case 'ArrowLeft':
      go(-1)
      break
    case '+':
    case '=':
      zoom(1.25)
      break
    case '-':
      zoom(0.8)
      break
    case 'r':
    case 'R':
      rotate()
      break
    case '0':
      resetView()
      break
    default:
      return
  }
  e.preventDefault()
}

function onWheel(e: WheelEvent): void {
  e.preventDefault()
  zoom(e.deltaY < 0 ? 1.1 : 0.9)
}

function onPointerDown(e: PointerEvent): void {
  drag = { x: e.clientX, y: e.clientY, ox: offset.value.x, oy: offset.value.y }
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}

function onPointerMove(e: PointerEvent): void {
  if (!drag) return
  offset.value = { x: drag.ox + e.clientX - drag.x, y: drag.oy + e.clientY - drag.y }
}

function onPointerUp(): void {
  drag = null
}

watch(
  () => props.index,
  async (i, prev) => {
    resetView()
    if (i !== null && prev === null) {
      returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
      document.addEventListener('keydown', onKey)
      await nextTick()
      root.value?.focus()
    } else if (i === null) {
      document.removeEventListener('keydown', onKey)
      returnFocus?.focus()
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => document.removeEventListener('keydown', onKey))

const transform = computed(
  () => `translate(${offset.value.x}px, ${offset.value.y}px) rotate(${rotation.value}deg) scale(${scale.value})`,
)
</script>

<template>
  <Teleport to="body">
    <Transition name="viewer">
      <div v-if="current" ref="root" class="viewer" role="dialog" aria-modal="true" :aria-label="current.label" tabindex="-1">
        <header class="viewer__bar jt-glass">
          <span class="viewer__title">{{ current.label }}</span>
          <span v-if="images.length > 1" class="viewer__count jt-num">{{ (index ?? 0) + 1 }} / {{ images.length }}</span>
          <div class="viewer__tools">
            <button type="button" :title="t('viewer.zoom_out')" :aria-label="t('viewer.zoom_out')" @click="zoom(0.8)"><ZoomOut :size="18" /></button>
            <span class="viewer__zoom jt-num">{{ Math.round(scale * 100) }}%</span>
            <button type="button" :title="t('viewer.zoom_in')" :aria-label="t('viewer.zoom_in')" @click="zoom(1.25)"><ZoomIn :size="18" /></button>
            <button type="button" :title="t('viewer.rotate')" :aria-label="t('viewer.rotate')" @click="rotate"><RotateCw :size="18" /></button>
            <button type="button" :title="t('viewer.fit')" :aria-label="t('viewer.fit')" @click="resetView"><Maximize :size="18" /></button>
            <button type="button" :title="t('common.close')" :aria-label="t('common.close')" @click="close"><X :size="20" /></button>
          </div>
        </header>

        <div
          class="viewer__stage"
          @wheel="onWheel"
          @pointerdown="onPointerDown"
          @pointermove="onPointerMove"
          @pointerup="onPointerUp"
          @pointercancel="onPointerUp"
          @click.self="close"
        >
          <AppSpinner v-if="img.loading.value" :size="24" class="viewer__spinner" />
          <p v-else-if="img.error.value" class="viewer__error">{{ t('viewer.unavailable') }}</p>
          <img v-if="img.src.value" :src="img.src.value" :alt="current.label" class="viewer__img" :style="{ transform }" draggable="false" />
        </div>

        <template v-if="images.length > 1">
          <button type="button" class="viewer__nav viewer__nav--prev jt-glass" :aria-label="t('common.previous')" @click="go(-1)">
            <ChevronLeft :size="22" />
          </button>
          <button type="button" class="viewer__nav viewer__nav--next jt-glass" :aria-label="t('common.next')" @click="go(1)">
            <ChevronRight :size="22" />
          </button>
        </template>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.viewer {
  position: fixed;
  inset: 0;
  z-index: var(--jt-z-viewer);
  background: rgba(16, 16, 18, 0.94);
  outline: none;
}

.viewer__bar {
  position: absolute;
  top: 12px;
  left: 50%;
  z-index: 2;
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  max-width: calc(100% - 24px);
  padding: 6px 8px 6px 18px;
  border-radius: var(--jt-radius-round);
  transform: translateX(-50%);
}

.viewer__title {
  font: var(--jt-font-body-strong);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.viewer__count,
.viewer__zoom {
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
  min-width: 40px;
  text-align: center;
}

.viewer__tools {
  display: flex;
  align-items: center;
  gap: 2px;
}

.viewer__tools button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border-radius: 50%;
}

.viewer__tools button:hover {
  background: var(--jt-color-hover);
}

.viewer__stage {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  cursor: grab;
  touch-action: none;
}

.viewer__stage:active {
  cursor: grabbing;
}

.viewer__img {
  max-width: calc(100% - 160px);
  max-height: calc(100% - 120px);
  user-select: none;
  transition: transform 120ms var(--jt-ease);
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.4);
}

.viewer__spinner,
.viewer__error {
  color: rgba(255, 255, 255, 0.8);
}

.viewer__nav {
  position: absolute;
  top: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  border-radius: 50%;
  transform: translateY(-50%);
}

.viewer__nav--prev {
  left: 20px;
}

.viewer__nav--next {
  right: 20px;
}

.viewer-enter-active,
.viewer-leave-active {
  transition: opacity var(--jt-duration-base) var(--jt-ease);
}

.viewer-enter-from,
.viewer-leave-to {
  opacity: 0;
}
</style>
