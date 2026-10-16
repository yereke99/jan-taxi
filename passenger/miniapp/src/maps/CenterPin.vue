<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  Fixed pin over the map's visual centre for "move the map beneath the pin"
  point selection. Place it inside <MapView>; it lifts while the map moves and
  drops when the camera settles. The tip sits exactly on the padded centre
  (map.getCenter()), so pass the same padding the map uses.
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useMap } from './context'

const props = withDefaults(
  defineProps<{
    /** Same camera padding as the MapView; moves the pin to the padded centre. */
    padding?: { top: number; right: number; bottom: number; left: number }
    /** Forces the lifted state (otherwise driven by map movement). */
    lifted?: boolean
    /** Accessible description of what the pin selects. */
    label?: string
  }>(),
  { padding: () => ({ top: 0, right: 0, bottom: 0, left: 0 }), lifted: undefined, label: '' },
)

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
  left: -11px;
  bottom: 0;
  width: 22px;
  display: flex;
  flex-direction: column;
  align-items: center;
  transition: transform var(--jt-duration-fast) var(--jt-ease);
}

.jt-center-pin__head {
  width: 22px;
  height: 22px;
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
  width: 2px;
  height: 14px;
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

.is-lifted .jt-center-pin__body {
  transform: translateY(-12px);
}

.is-lifted .jt-center-pin__shadow {
  transform: scale(1.6);
  opacity: 0.5;
}

@media (prefers-reduced-motion: reduce) {
  .jt-center-pin__body,
  .jt-center-pin__shadow {
    transition: none;
  }
}
</style>
