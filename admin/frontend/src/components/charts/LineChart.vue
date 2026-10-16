<!--
  Single-series daily line (2px, area wash) with a crosshair that snaps to the
  nearest day and a tooltip; keyboard focus walks the days. A table view
  lists every value.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { compactNumber, niceTicks } from '../../utils/chartScale'

const props = withDefaults(
  defineProps<{ labels: string[]; values: number[]; label: string; format: (n: number) => string; height?: number; caption: string }>(),
  { height: 200 },
)

const { t, locale } = useI18n()
const host = ref<HTMLElement | null>(null)
const width = ref(600)
const hover = ref<number | null>(null)
const showTable = ref(false)
let ro: ResizeObserver | null = null

onMounted(() => {
  if (!host.value) return
  width.value = host.value.clientWidth || 600
  if (typeof ResizeObserver !== 'undefined') {
    ro = new ResizeObserver(() => {
      if (host.value) width.value = host.value.clientWidth || width.value
    })
    ro.observe(host.value)
  }
})
onBeforeUnmount(() => ro?.disconnect())

const PAD = { top: 12, right: 12, bottom: 24, left: 52 }
const loc = computed(() => (locale.value === 'ru' || locale.value === 'en' ? locale.value : 'kk'))
const ticks = computed(() => niceTicks(Math.max(0, ...props.values)))
const top = computed(() => ticks.value[ticks.value.length - 1] ?? 1)
const plotW = computed(() => Math.max(10, width.value - PAD.left - PAD.right))
const plotH = computed(() => props.height - PAD.top - PAD.bottom)
const n = computed(() => props.values.length)

function x(i: number): number {
  return n.value <= 1 ? PAD.left + plotW.value / 2 : PAD.left + (plotW.value * i) / (n.value - 1)
}
function y(v: number): number {
  return PAD.top + plotH.value - (v / top.value) * plotH.value
}

const linePath = computed(() => props.values.map((v, i) => `${i === 0 ? 'M' : 'L'}${x(i)},${y(v)}`).join(''))
const areaPath = computed(() =>
  n.value === 0 ? '' : `${linePath.value}L${x(n.value - 1)},${PAD.top + plotH.value}L${x(0)},${PAD.top + plotH.value}Z`,
)
const labelEvery = computed(() => Math.max(1, Math.ceil(n.value / Math.max(1, Math.floor(plotW.value / 56)))))

function onMove(e: PointerEvent): void {
  if (n.value === 0) return
  const rect = (e.currentTarget as SVGElement).getBoundingClientRect()
  const px = e.clientX - rect.left
  const i = n.value <= 1 ? 0 : Math.round(((px - PAD.left) / plotW.value) * (n.value - 1))
  hover.value = Math.min(n.value - 1, Math.max(0, i))
}

function onKey(e: KeyboardEvent): void {
  if (n.value === 0) return
  if (e.key === 'ArrowRight') hover.value = Math.min(n.value - 1, (hover.value ?? -1) + 1)
  else if (e.key === 'ArrowLeft') hover.value = Math.max(0, (hover.value ?? n.value) - 1)
  else return
  e.preventDefault()
}

const last = computed(() => (n.value > 0 ? n.value - 1 : null))
</script>

<template>
  <figure class="chart">
    <div ref="host" class="chart__plot">
      <svg
        :width="width"
        :height="height"
        role="img"
        :aria-label="caption"
        tabindex="0"
        @pointermove="onMove"
        @pointerleave="hover = null"
        @keydown="onKey"
        @blur="hover = null"
      >
        <g class="chart__grid">
          <template v-for="v in ticks" :key="v">
            <line :x1="PAD.left" :x2="width - PAD.right" :y1="y(v)" :y2="y(v)" />
            <text :x="PAD.left - 8" :y="y(v) + 4" text-anchor="end">{{ compactNumber(v, loc) }}</text>
          </template>
        </g>
        <path v-if="areaPath" class="area" :d="areaPath" />
        <path v-if="linePath" class="line" :d="linePath" />
        <template v-for="(lab, i) in labels" :key="lab">
          <text v-if="i % labelEvery === 0" class="chart__x" :x="x(i)" :y="height - 6" text-anchor="middle">{{ lab.slice(5) }}</text>
        </template>
        <circle v-if="last !== null && hover === null" class="dot" :cx="x(last)" :cy="y(values[last] ?? 0)" r="4" />
        <template v-if="hover !== null">
          <line class="crosshair" :x1="x(hover)" :x2="x(hover)" :y1="PAD.top" :y2="PAD.top + plotH" />
          <circle class="dot" :cx="x(hover)" :cy="y(values[hover] ?? 0)" r="4" />
        </template>
      </svg>
      <div v-if="hover !== null" class="chart__tip" :style="{ left: `${Math.min(Math.max(x(hover), 80), width - 80)}px` }" role="status">
        <p class="chart__tip-date">{{ labels[hover] }}</p>
        <p class="chart__tip-row">
          <span class="chart__key chart__key--strong" />
          <strong class="jt-num">{{ format(values[hover] ?? 0) }}</strong>
          <span class="jt-muted">{{ label }}</span>
        </p>
      </div>
    </div>
    <figcaption class="chart__foot">
      <span v-if="last !== null" class="jt-num">{{ labels[last] }}: {{ format(values[last] ?? 0) }}</span>
      <button type="button" class="jt-link-button chart__table-toggle" :aria-expanded="showTable" @click="showTable = !showTable">
        {{ showTable ? t('dashboard.hide_table') : t('dashboard.show_table') }}
      </button>
    </figcaption>
    <table v-if="showTable" class="chart__table">
      <caption class="jt-visually-hidden">{{ caption }}</caption>
      <thead>
        <tr>
          <th scope="col">{{ t('dashboard.date') }}</th>
          <th scope="col">{{ label }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(lab, i) in labels" :key="lab">
          <td>{{ lab }}</td>
          <td class="jt-num">{{ format(values[i] ?? 0) }}</td>
        </tr>
      </tbody>
    </table>
  </figure>
</template>

<style scoped src="./chart.css"></style>
