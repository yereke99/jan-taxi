<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  Persistent iOS 26 bottom sheet for map-first screens (§15 "Layout", §15.1).
  Below the top snap it floats as an inset glass card (8px from the screen
  edges, above the bottom safe area, all corners 32px); at 'full' it docks
  full-width with a solid white surface. Snap points:
    'peek'     peekHeight of content above the bottom safe area
    'half'     halfRatio of the viewport
    'full'     viewport minus the top safe area and fullTopGap
    'content'  natural height of header + content + footer (clamped to full)
    <number>   absolute occupied height in px
  Drag the grabber/header (or the content when not at the top snap) and
  release with velocity to move between snaps; at the top snap the content
  scrolls natively. The footer slot stays pinned to the visible bottom edge,
  above the keyboard, so a primary button is never hidden.

  v-model:snap  current snap point
  @height       settled height in px measured from the viewport bottom
                (keyboard excluded); use it as map bottom padding
  Slots: header (draggable), default (content), footer (pinned actions).
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onBeforeUpdate, onMounted, ref, useSlots, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { effectiveSafeInsets, useTelegramFullscreen } from '../../telegram/useTelegramFullscreen'
import type { SheetSnap } from './types'

const props = withDefaults(
  defineProps<{
    snap?: SheetSnap
    snapPoints?: SheetSnap[]
    /** Visible content height at 'peek', excluding the bottom safe area. */
    peekHeight?: number
    /** Fraction of the viewport used by 'half'. */
    halfRatio?: number
    /** Gap kept between the top safe area and the sheet at 'full'. */
    fullTopGap?: number
    /** Float as an inset card below the top snap (§15.1). */
    floating?: boolean
    /** Glass surface while floating; the docked sheet is always solid white. */
    glass?: boolean
    ariaLabel?: string
  }>(),
  {
    snap: 'peek',
    snapPoints: () => ['peek', 'half', 'full'],
    peekHeight: 180,
    halfRatio: 0.5,
    fullTopGap: 12,
    floating: true,
    glass: true,
    ariaLabel: undefined,
  },
)

const emit = defineEmits<{
  'update:snap': [snap: SheetSnap]
  snap: [snap: SheetSnap]
  height: [px: number]
}>()

const FLOAT_INSET = 8
const BOTTOM_PAD = 12
/** The glass hairline border (0.5px top + bottom) takes space inside the sheet. */
const GLASS_BORDER_PX = 1

const { t } = useI18n()
const slots = useSlots()
const tg = useTelegramFullscreen()
const safe = computed(() => effectiveSafeInsets(tg.state))

/**
 * Height available to the sheet above the keyboard. While the keyboard is
 * open the layout viewport (innerHeight) is used: it shrinks on Android, and
 * on iOS the overlap is subtracted instead.
 */
const viewportHeight = computed(() => {
  const s = tg.state
  const layout = typeof window === 'undefined' ? 0 : window.innerHeight
  const base = s.keyboardOpen ? layout || s.viewportHeight : s.stableHeight || s.viewportHeight || layout
  return Math.max(0, base - s.keyboardHeight)
})

/** Distance between the floating sheet and the screen (or keyboard) bottom. */
const bottomGap = computed(() => (tg.state.keyboardOpen ? FLOAT_INSET : Math.max(FLOAT_INSET, safe.value.bottom)))
const fullHeight = computed(() => Math.max(0, viewportHeight.value - safe.value.top - props.fullTopGap))

// --- Content measurement for the 'content' snap ---------------------------

const handleEl = ref<HTMLElement | null>(null)
const bodyEl = ref<HTMLElement | null>(null)
const footerEl = ref<HTMLElement | null>(null)
const measured = ref({ handle: 20, body: 0, footer: 0 })
let observer: ResizeObserver | null = null

function measure(): void {
  measured.value = {
    handle: handleEl.value?.offsetHeight ?? 20,
    body: bodyEl.value?.offsetHeight ?? 0,
    footer: footerEl.value?.offsetHeight ?? 0,
  }
}

onMounted(() => {
  measure()
  if (typeof ResizeObserver === 'undefined') return
  observer = new ResizeObserver(measure)
  for (const el of [handleEl.value, bodyEl.value, footerEl.value]) if (el) observer.observe(el)
})

watch(footerEl, (el, old) => {
  if (old) observer?.unobserve(old)
  if (el) observer?.observe(el)
  measure()
})

onBeforeUnmount(() => observer?.disconnect())

/**
 * Whether a footer slot is passed. Slots are not reactive, so a footer added
 * or removed later (e.g. once data has loaded) is picked up before each render.
 */
const hasFooter = ref(Boolean(slots.footer))
onBeforeUpdate(() => {
  hasFooter.value = Boolean(slots.footer)
})

// --- Snap geometry (all heights are measured from the viewport bottom) ----

function heightOf(s: SheetSnap): number {
  let h: number
  if (s === 'peek') h = props.peekHeight + (props.floating ? bottomGap.value : safe.value.bottom)
  else if (s === 'half') h = viewportHeight.value * props.halfRatio
  else if (s === 'full') h = fullHeight.value
  else if (s === 'content') {
    const m = measured.value
    h =
      m.handle +
      m.body +
      (hasFooter.value ? m.footer : BOTTOM_PAD) +
      (props.glass ? GLASS_BORDER_PX : 0) +
      (props.floating ? bottomGap.value : safe.value.bottom)
  } else h = s
  return Math.max(0, Math.min(h, fullHeight.value))
}

const points = computed(() =>
  props.snapPoints.map((snap) => ({ snap, h: heightOf(snap) })).sort((a, b) => a.h - b.h),
)

const current = ref<SheetSnap>(props.snap)
watch(
  () => props.snap,
  (s) => (current.value = s),
)

const settledHeight = computed(() => heightOf(current.value))
const dragHeight = ref<number | null>(null)
const occupiedHeight = computed(() => dragHeight.value ?? settledHeight.value)
const atTop = computed(() => {
  const last = points.value[points.value.length - 1]
  return last !== undefined && settledHeight.value >= last.h - 1
})
/** Docked full-width at the full height; floating inset card otherwise. */
const docked = computed(() => !props.floating || occupiedHeight.value >= fullHeight.value - 0.5)

watch(settledHeight, (h) => emit('height', Math.round(h)), { immediate: true })

const sheetStyle = computed(() => {
  const gap = docked.value ? 0 : bottomGap.value
  const side = docked.value ? 0 : FLOAT_INSET
  return {
    left: `${side + (docked.value ? 0 : safe.value.left)}px`,
    right: `${side + (docked.value ? 0 : safe.value.right)}px`,
    bottom: `calc(var(--jt-keyboard) + ${gap}px)`,
    height: `${Math.max(0, Math.round(occupiedHeight.value - gap))}px`,
    '--jt-sheet-bottom-pad': docked.value
      ? `${(tg.state.keyboardOpen ? 0 : safe.value.bottom) + FLOAT_INSET}px`
      : `${BOTTOM_PAD}px`,
  }
})

function setSnap(s: SheetSnap): void {
  if (s === current.value) return
  current.value = s
  emit('update:snap', s)
  emit('snap', s)
}

function cycle(): void {
  const list = points.value
  if (list.length === 0) return
  if (atTop.value) {
    setSnap(list[0]!.snap)
    return
  }
  const next = list.find((p) => p.h > settledHeight.value + 1)
  if (next) setSnap(next.snap)
}

// --- Dragging --------------------------------------------------------------

interface Drag {
  pointerId: number
  startX: number
  startY: number
  startHeight: number
  active: boolean
  samples: Array<{ t: number; y: number }>
}

const DRAG_SLOP_PX = 6
const VELOCITY_WINDOW_MS = 100
const FLICK_PX_PER_MS = 0.6
const MOMENTUM_MS = 180
const RUBBER = 0.25

let drag: Drag | null = null

function onPointerDown(e: PointerEvent, fromContent: boolean): void {
  if (drag || (e.pointerType === 'mouse' && e.button !== 0)) return
  // At the top snap the content owns vertical gestures (native scrolling).
  if (fromContent && atTop.value) return
  // Form controls keep their own gestures (text selection, sliders).
  if (fromContent && (e.target as Element | null)?.closest('input, textarea, select, [data-sheet-no-drag]')) return
  drag = {
    pointerId: e.pointerId,
    startX: e.clientX,
    startY: e.clientY,
    startHeight: occupiedHeight.value,
    active: false,
    samples: [{ t: e.timeStamp, y: e.clientY }],
  }
  window.addEventListener('pointermove', onPointerMove, { passive: false })
  window.addEventListener('pointerup', onPointerUp)
  window.addEventListener('pointercancel', onPointerUp)
}

function detach(): void {
  window.removeEventListener('pointermove', onPointerMove)
  window.removeEventListener('pointerup', onPointerUp)
  window.removeEventListener('pointercancel', onPointerUp)
}

function onPointerMove(e: PointerEvent): void {
  if (!drag || e.pointerId !== drag.pointerId) return
  const dy = e.clientY - drag.startY
  const dx = e.clientX - drag.startX
  if (!drag.active) {
    if (Math.abs(dx) > Math.abs(dy) && Math.abs(dx) > DRAG_SLOP_PX) {
      // Horizontal gesture (e.g. a carousel inside the sheet): not ours.
      drag = null
      detach()
      return
    }
    if (Math.abs(dy) < DRAG_SLOP_PX) return
    drag.active = true
    drag.startY = e.clientY
    drag.startHeight = occupiedHeight.value
  }
  e.preventDefault()
  drag.samples.push({ t: e.timeStamp, y: e.clientY })
  while (drag.samples.length > 2 && e.timeStamp - drag.samples[0]!.t > VELOCITY_WINDOW_MS) drag.samples.shift()
  const list = points.value
  const min = list[0]?.h ?? 0
  const max = list[list.length - 1]?.h ?? fullHeight.value
  let h = drag.startHeight - (e.clientY - drag.startY)
  if (h > max) h = max + (h - max) * RUBBER
  if (h < min) h = min - (min - h) * RUBBER
  dragHeight.value = Math.max(0, Math.min(h, fullHeight.value))
}

/** Finger velocity in px/ms; positive means moving down (shrinking the sheet). */
function velocity(d: Drag): number {
  const first = d.samples[0]
  const last = d.samples[d.samples.length - 1]
  if (!first || !last || last.t === first.t) return 0
  return (last.y - first.y) / (last.t - first.t)
}

function suppressNextClick(): void {
  const stop = (ev: Event): void => {
    ev.stopPropagation()
    ev.preventDefault()
  }
  window.addEventListener('click', stop, { capture: true, once: true })
  setTimeout(() => window.removeEventListener('click', stop, { capture: true }), 0)
}

function onPointerUp(e: PointerEvent): void {
  if (!drag || e.pointerId !== drag.pointerId) return
  const d = drag
  drag = null
  detach()
  if (!d.active) return
  suppressNextClick()
  const h = dragHeight.value ?? settledHeight.value
  const v = velocity(d)
  const list = points.value
  let target = list[0]
  if (Math.abs(v) > FLICK_PX_PER_MS) {
    target = v > 0 ? ([...list].reverse().find((p) => p.h < h - 1) ?? list[0]) : (list.find((p) => p.h > h + 1) ?? list[list.length - 1])
  } else {
    const projected = h - v * MOMENTUM_MS
    for (const p of list) if (target === undefined || Math.abs(p.h - projected) < Math.abs(target.h - projected)) target = p
  }
  dragHeight.value = null
  if (target) setSnap(target.snap)
}

onBeforeUnmount(detach)

defineExpose({
  snapTo: (s: SheetSnap) => setSnap(s),
  /** Current settled height in px (from the viewport bottom). */
  height: (): number => Math.round(settledHeight.value),
})
</script>

<template>
  <section
    class="jt-sheet"
    :class="{ 'is-docked': docked, 'is-floating': !docked, 'jt-glass': glass && !docked, 'is-dragging': dragHeight !== null }"
    :style="sheetStyle"
    :aria-label="ariaLabel"
  >
    <div ref="handleEl" class="jt-sheet__handle" @pointerdown="onPointerDown($event, false)">
      <button
        type="button"
        class="jt-sheet__grabber"
        :aria-label="atTop ? t('common.collapse') : t('common.expand')"
        :aria-expanded="atTop"
        @click="cycle"
      >
        <span class="jt-sheet__grabber-bar" />
      </button>
      <slot name="header" />
    </div>
    <div
      class="jt-sheet__content"
      :class="{ 'is-scrollable': atTop, 'has-footer': hasFooter }"
      @pointerdown="onPointerDown($event, true)"
    >
      <div ref="bodyEl" class="jt-sheet__body">
        <slot />
      </div>
    </div>
    <div v-if="hasFooter" ref="footerEl" class="jt-sheet__footer">
      <slot name="footer" />
    </div>
  </section>
</template>

<style scoped>
.jt-sheet {
  position: fixed;
  z-index: var(--jt-z-sheet);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: var(--jt-radius-sheet);
  transition:
    height var(--jt-duration-spring) var(--jt-ease-spring),
    left var(--jt-duration-spring) var(--jt-ease-spring),
    right var(--jt-duration-spring) var(--jt-ease-spring),
    bottom var(--jt-duration-spring) var(--jt-ease-spring),
    border-radius var(--jt-duration-spring) var(--jt-ease-spring);
  contain: layout paint;
}

/* Solid surface unless the glass material (global .jt-glass) is applied. */
.jt-sheet:not(.jt-glass) {
  background: var(--jt-color-bg);
  box-shadow: var(--jt-glass-fallback-shadow);
}

.jt-sheet.is-docked {
  border-radius: var(--jt-radius-sheet) var(--jt-radius-sheet) 0 0;
  box-shadow: var(--jt-shadow-sheet);
}

.jt-sheet.is-dragging {
  transition:
    left var(--jt-duration-fast) var(--jt-ease),
    right var(--jt-duration-fast) var(--jt-ease),
    bottom var(--jt-duration-fast) var(--jt-ease),
    border-radius var(--jt-duration-fast) var(--jt-ease);
}

.jt-sheet__handle {
  flex: none;
  touch-action: none;
  padding: 0 var(--jt-side-margin);
  user-select: none;
  -webkit-user-select: none;
}

.jt-sheet__grabber {
  display: flex;
  justify-content: center;
  align-items: flex-start;
  width: 100%;
  height: 20px;
  padding-top: 6px;
  cursor: grab;
}

.jt-sheet__grabber-bar {
  width: var(--jt-grabber-width);
  height: var(--jt-grabber-height);
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-tertiary);
}

.jt-sheet__content {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  touch-action: none;
  padding: 0 var(--jt-side-margin) var(--jt-sheet-bottom-pad);
}

.jt-sheet__content.has-footer {
  padding-bottom: 0;
}

.jt-sheet__content.is-scrollable {
  overflow-y: auto;
  overscroll-behavior: contain;
  touch-action: pan-y;
  -webkit-overflow-scrolling: touch;
}

.jt-sheet__body {
  display: flow-root;
}

.jt-sheet__footer {
  flex: none;
  padding: var(--jt-space-2) var(--jt-side-margin) var(--jt-sheet-bottom-pad);
}

@media (prefers-reduced-motion: reduce) {
  .jt-sheet,
  .jt-sheet.is-dragging {
    transition: none;
  }
}
</style>
