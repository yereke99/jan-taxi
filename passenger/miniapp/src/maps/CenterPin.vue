<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  Fixed pin over the map's visual centre for "move the map beneath the pin"
  point selection. Place it inside <MapView>; it lifts while the map moves and
  drops when the camera settles. The tip sits exactly on the padded centre
  (map.getCenter()), so pass the same padding the map uses.

  `callout` adds the white bubble above the pin: a small grey caption, the
  address in bold (a loading bar while the map moves or the address is being
  resolved) and a chevron; tapping it emits `callout`.
-->
<script setup lang="ts">
import { ChevronRight } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useMap } from './context'

export interface PinCallout {
  caption: string
  title: string
  loading: boolean
}

const props = withDefaults(
  defineProps<{
    /** Same camera padding as the MapView; moves the pin to the padded centre. */
    padding?: { top: number; right: number; bottom: number; left: number }
    /** Forces the lifted state (otherwise driven by map movement). */
    lifted?: boolean
    /** Accessible description of what the pin selects. */
    label?: string
    callout?: PinCallout | null
  }>(),
  { padding: () => ({ top: 0, right: 0, bottom: 0, left: 0 }), lifted: undefined, label: '', callout: null },
)

const emit = defineEmits<{ callout: [] }>()

const map = useMap()
const moving = ref(false)

watch(
  map,
  (m, _old, onCleanup) => {
    if (!m) return
    const start = (): void => {
      moving.value = true
    }
    const end = (): void => {
      moving.value = false
    }
    m.on('movestart', start)
    m.on('moveend', end)
    onCleanup(() => {
      m.off('movestart', start)
      m.off('moveend', end)
    })
  },
  { immediate: true },
)

const isLifted = computed(() => props.lifted ?? moving.value)
const style = computed(() => ({
  left: `calc(50% + ${(props.padding.left - props.padding.right) / 2}px)`,
  top: `calc(50% + ${(props.padding.top - props.padding.bottom) / 2}px)`,
}))
</script>

<template>
  <div class="jt-center-pin" :class="{ 'is-lifted': isLifted }" :style="style" role="img" :aria-label="label || undefined">
    <div class="jt-center-pin__shadow" />
    <button v-if="callout" type="button" class="jt-center-pin__callout" :aria-busy="callout.loading || isLifted || undefined" @click="emit('callout')">
      <span class="jt-center-pin__text">
        <span class="jt-center-pin__caption">{{ callout.caption }}</span>
        <span v-if="callout.loading || isLifted || !callout.title" class="jt-center-pin__loading" />
        <span v-else class="jt-center-pin__title">{{ callout.title }}</span>
      </span>
      <ChevronRight :size="18" :stroke-width="2.5" aria-hidden="true" />
    </button>
    <div class="jt-center-pin__body">
      <div class="jt-center-pin__head"><div class="jt-center-pin__hole" /></div>
      <div class="jt-center-pin__stem" />
    </div>
  </div>
</template>

<style scoped>
.jt-center-pin {
  position: absolute;
  width: 0;
  height: 0;
  z-index: var(--jt-z-floating);
  pointer-events: none;
}

.jt-center-pin__body {
  position: absolute;
  left: -13px;
  bottom: 0;
  width: 26px;
  display: flex;
  flex-direction: column;
  align-items: center;
  transition: transform var(--jt-duration-fast) var(--jt-ease);
}

.jt-center-pin__head {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: var(--jt-color-primary);
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: var(--jt-shadow-marker);
}

.jt-center-pin__hole {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--jt-color-bg);
}

.jt-center-pin__stem {
  width: 3px;
  height: 16px;
  background: var(--jt-color-primary);
  border-radius: 0 0 1px 1px;
}

.jt-center-pin__shadow {
  position: absolute;
  left: -4px;
  top: -2px;
  width: 8px;
  height: 4px;
  border-radius: 50%;
  background: rgba(0, 0, 0, 0.3);
  transition:
    transform var(--jt-duration-fast) var(--jt-ease),
    opacity var(--jt-duration-fast) var(--jt-ease);
}

.is-lifted .jt-center-pin__body,
.is-lifted .jt-center-pin__callout {
  transform: translateY(-12px);
}

.jt-center-pin__callout {
  position: absolute;
  left: 0;
  bottom: 48px;
  display: flex;
  align-items: center;
  gap: 10px;
  width: max-content;
  min-width: 188px;
  max-width: min(320px, calc(100vw - 48px));
  padding: 8px 12px 9px 16px;
  border-radius: 18px;
  background: var(--jt-color-bg);
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.14), 0 0 0 0.5px rgba(0, 0, 0, 0.05);
  color: var(--jt-color-label);
  text-align: left;
  pointer-events: auto;
  translate: -50% 0;
  transition: transform var(--jt-duration-fast) var(--jt-ease);
}

.jt-center-pin__callout:active {
  opacity: var(--jt-opacity-pressed);
}

.jt-center-pin__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.jt-center-pin__caption {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.jt-center-pin__title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font: var(--jt-font-headline);
}

.jt-center-pin__loading {
  width: 140px;
  height: 14px;
  margin: 4px 0 4px;
  border-radius: 7px;
  background: var(--jt-color-fill-secondary);
  animation: jt-pin-loading 1.1s ease-in-out infinite alternate;
}

@keyframes jt-pin-loading {
  from {
    opacity: 1;
  }
  to {
    opacity: 0.45;
  }
}

.is-lifted .jt-center-pin__shadow {
  transform: scale(1.6);
  opacity: 0.5;
}

@media (prefers-reduced-motion: reduce) {
  .jt-center-pin__body,
  .jt-center-pin__callout,
  .jt-center-pin__shadow {
    transition: none;
  }

  .jt-center-pin__loading {
    animation: none;
  }
}
</style>
