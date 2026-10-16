<!--
  Stops of the route editor: A pickup, up to three intermediate stops, the
  destination last. Pickup and destination have fixed roles; intermediate
  stops can be removed and reordered with the drag handle (or the move up /
  move down buttons, which become visible on keyboard focus). Nothing is ever
  reordered automatically.
-->
<script setup lang="ts">
import { ChevronDown, ChevronUp, GripVertical, Plus, X } from '@lucide/vue'
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { isIntermediate, type RouteStop } from '../../domain/route'
import { STOP_LETTERS } from '../../maps'
import { haptics } from '../../telegram'
import StopBadge from '../StopBadge.vue'

const props = defineProps<{
  stops: RouteStop[]
  /** Error text per stop index. */
  errors: Record<number, string>
  canAdd: boolean
  maxPoints: number
}>()

const emit = defineEmits<{
  select: [index: number]
  remove: [index: number]
  move: [from: number, to: number]
  add: []
}>()

const { t } = useI18n()

const intermediateCount = computed(() => Math.max(0, props.stops.length - 2))
const reorderable = computed(() => intermediateCount.value >= 2)

function placeholder(index: number): string {
  if (index === 0) return t('booking.pickup_placeholder')
  if (index === props.stops.length - 1) return t('booking.destination_placeholder')
  return t('booking.stop_placeholder')
}

function stopName(index: number): string {
  return props.stops[index]?.place?.address || placeholder(index)
}

// --- Drag to reorder ---------------------------------------------------------

const rowEls = ref<HTMLElement[]>([])
const dragFrom = ref<number | null>(null)
const dragTo = ref<number | null>(null)
const dragOffset = ref(0)
let drag: { pointerId: number; startY: number; rects: DOMRect[]; handle: HTMLElement } | null = null

function setRowEl(el: unknown, i: number): void {
  if (el instanceof HTMLElement) rowEls.value[i] = el
}

function onHandleDown(e: PointerEvent, index: number): void {
  if (!isIntermediate(props.stops, index) || drag) return
  e.preventDefault()
  e.stopPropagation()
  const handle = e.currentTarget as HTMLElement
  drag = { pointerId: e.pointerId, startY: e.clientY, rects: rowEls.value.map((el) => el.getBoundingClientRect()), handle }
  handle.setPointerCapture?.(e.pointerId)
  dragFrom.value = index
  dragTo.value = index
  dragOffset.value = 0
  haptics.impact('light')
}

function onHandleMove(e: PointerEvent): void {
  if (!drag || e.pointerId !== drag.pointerId || dragFrom.value === null) return
  e.preventDefault()
  const from = dragFrom.value
  const first = 1
  const last = props.stops.length - 2
  const own = drag.rects[from]
  if (!own) return
  // Keep the dragged row inside the intermediate block.
  const minY = (drag.rects[first]?.top ?? own.top) - own.top
  const maxY = (drag.rects[last]?.top ?? own.top) - own.top
  dragOffset.value = Math.min(maxY, Math.max(minY, e.clientY - drag.startY))
  const center = own.top + own.height / 2 + dragOffset.value
  let target = from
  for (let i = first; i <= last; i++) {
    const r = drag.rects[i]
    if (r && center >= r.top && center < r.bottom) target = i
  }
  if (target !== dragTo.value) haptics.selection()
  dragTo.value = target
}

function endDrag(e: PointerEvent): void {
  if (!drag || e.pointerId !== drag.pointerId) return
  drag.handle.releasePointerCapture?.(e.pointerId)
  const from = dragFrom.value
  const to = dragTo.value
  drag = null
  dragFrom.value = null
  dragTo.value = null
  dragOffset.value = 0
  if (from !== null && to !== null && from !== to) emit('move', from, to)
}

onBeforeUnmount(() => {
  drag = null
})

function rowStyle(i: number): Record<string, string> | undefined {
  const from = dragFrom.value
  const to = dragTo.value
  if (from === null || to === null) return undefined
  if (i === from) return { transform: `translateY(${dragOffset.value}px)` }
  const h = drag?.rects[from]?.height ?? 0
  if (from < to && i > from && i <= to) return { transform: `translateY(${-h}px)` }
  if (to < from && i >= to && i < from) return { transform: `translateY(${h}px)` }
  return undefined
}

function moveBy(index: number, delta: -1 | 1): void {
  const to = index + delta
  if (isIntermediate(props.stops, index) && isIntermediate(props.stops, to)) emit('move', index, to)
}
</script>

<template>
  <div class="stops">
    <ol class="stops__list">
      <li
        v-for="(stop, i) in stops"
        :key="stop.key"
        :ref="(el) => setRowEl(el, i)"
        class="stops__row"
        :class="{ 'is-dragging': dragFrom === i, 'is-shifting': dragFrom !== null && dragFrom !== i }"
        :style="rowStyle(i)"
      >
        <button type="button" class="stops__main" @click="emit('select', i)">
          <StopBadge :letter="STOP_LETTERS[i] ?? ''" :empty="!stop.place" />
          <span class="stops__text">
            <span class="stops__title" :class="{ 'is-placeholder': !stop.place }">{{ stopName(i) }}</span>
            <span v-if="errors[i]" class="stops__error" role="alert">{{ errors[i] }}</span>
            <span v-else-if="stop.place?.subtitle" class="stops__subtitle">{{ stop.place.subtitle }}</span>
          </span>
        </button>

        <template v-if="isIntermediate(stops, i)">
          <span v-if="reorderable" class="stops__keys">
            <button type="button" class="stops__key" :disabled="i <= 1" :aria-label="t('booking.move_up')" @click="moveBy(i, -1)">
              <ChevronUp :size="18" aria-hidden="true" />
            </button>
            <button
              type="button"
              class="stops__key"
              :disabled="i >= stops.length - 2"
              :aria-label="t('booking.move_down')"
              @click="moveBy(i, 1)"
            >
              <ChevronDown :size="18" aria-hidden="true" />
            </button>
          </span>
          <span
            v-if="reorderable"
            class="stops__handle"
            data-sheet-no-drag
            :title="t('booking.reorder')"
            aria-hidden="true"
            @pointerdown="onHandleDown($event, i)"
            @pointermove="onHandleMove"
            @pointerup="endDrag"
            @pointercancel="endDrag"
          >
            <GripVertical :size="20" />
          </span>
          <button type="button" class="stops__remove" :aria-label="t('booking.remove_stop', { name: stopName(i) })" @click="emit('remove', i)">
            <X :size="16" :stroke-width="2.5" aria-hidden="true" />
          </button>
        </template>
      </li>
    </ol>

    <button type="button" class="stops__add" :disabled="!canAdd" @click="emit('add')">
      <span class="stops__add-icon"><Plus :size="18" :stroke-width="2.25" aria-hidden="true" /></span>
      <span>{{ t('booking.add_stop') }}</span>
      <span v-if="!canAdd" class="stops__add-note">{{ t('booking.max_stops', { n: maxPoints }) }}</span>
    </button>
  </div>
</template>

<style scoped>
.stops {
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
  overflow: hidden;
}

.stops__list {
  position: relative;
}

.stops__row {
  position: relative;
  display: flex;
  align-items: center;
  min-height: 56px;
  padding-right: 6px;
  background: var(--jt-color-fill-secondary);
}

.stops__row.is-shifting {
  transition: transform var(--jt-duration-base) var(--jt-ease);
}

.stops__row.is-dragging {
  z-index: 2;
  background: var(--jt-color-bg);
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.14);
  border-radius: 14px;
}

.stops__row + .stops__row::before {
  content: '';
  position: absolute;
  top: 0;
  left: 54px;
  right: 0;
  height: var(--jt-hairline);
  background: var(--jt-color-separator);
}

.stops__main {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 14px;
  min-height: 56px;
  padding: 10px 6px 10px 14px;
  text-align: left;
}

.stops__main:active {
  opacity: var(--jt-opacity-pressed);
}

.stops__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.stops__title {
  font: var(--jt-font-body);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.stops__title.is-placeholder {
  color: var(--jt-color-label-secondary);
}

.stops__subtitle {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.stops__error {
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}

/* Move buttons stay out of sight until reached with the keyboard. */
.stops__keys {
  display: flex;
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
}

.stops__keys:focus-within {
  position: static;
  width: auto;
  height: auto;
  clip: auto;
}

.stops__key {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
}

.stops__key:disabled {
  opacity: var(--jt-opacity-disabled);
}

.stops__handle {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 44px;
  color: var(--jt-color-label-tertiary);
  cursor: grab;
  touch-action: none;
}

.stops__remove {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 44px;
  color: var(--jt-color-label-secondary);
}

.stops__remove:active {
  opacity: var(--jt-opacity-pressed);
}

.stops__add {
  position: relative;
  display: flex;
  align-items: center;
  gap: 14px;
  width: 100%;
  min-height: 52px;
  padding: 0 14px;
  font: var(--jt-font-body);
  text-align: left;
}

.stops__add::before {
  content: '';
  position: absolute;
  top: 0;
  left: 54px;
  right: 0;
  height: var(--jt-hairline);
  background: var(--jt-color-separator);
}

.stops__add:active:not(:disabled) {
  opacity: var(--jt-opacity-pressed);
}

.stops__add:disabled {
  color: var(--jt-color-label-tertiary);
}

.stops__add-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
}

.stops__add-note {
  margin-left: auto;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}
</style>
