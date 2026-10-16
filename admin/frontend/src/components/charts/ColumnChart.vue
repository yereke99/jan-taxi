<!--
  Daily column chart. With `overlay`, the second series is drawn inside the
  first (completed within created: one hue, two shades). Thin columns with a
  rounded data end, hairline grid, per-column tooltip on hover/focus, and a
  table view so no value depends on colour or hovering.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { compactNumber, niceTicks } from '../../utils/chartScale'
import type { ColumnSeries } from '../types'

const props = withDefaults(
  defineProps<{ labels: string[]; series: ColumnSeries[]; format: (n: number) => string; height?: number; caption: string }>(),
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

const PAD = { top: 12, right: 8, bottom: 24, left: 44 }
const max = computed(() => Math.max(0, ...props.series.flatMap((s) => s.values)))
const ticks = computed(() => niceTicks(max.value))
const top = computed(() => ticks.value[ticks.value.length - 1] ?? 1)
const plotW = computed(() => Math.max(10, width.value - PAD.left - PAD.right))
const plotH = computed(() => props.height - PAD.top - PAD.bottom)
const band = computed(() => plotW.value / Math.max(1, props.labels.length))
const barW = computed(() => Math.max(3, Math.min(24, band.value * 0.6)))
const loc = computed(() => (locale.value === 'ru' || locale.value === 'en' ? locale.value : 'kk'))

function y(v: number): number {
  return PAD.top + plotH.value - (v / top.value) * plotH.value
}

/** Column path with a 4px rounded data end and a square base. */
function column(i: number, v: number, w: number): string {
  const cx = PAD.left + band.value * i + band.value / 2
  const x0 = cx - w / 2
  const base = PAD.top + plotH.value
  const yt = y(v)
  const h = base - yt
  if (h <= 0) return ''
  const r = Math.min(4, w / 2, h)
  return `M${x0},${base}V${yt + r}Q${x0},${yt} ${x0 + r},${yt}H${x0 + w - r}Q${x0 + w},${yt} ${x0 + w},${yt + r}V${base}Z`
}

const labelEvery = computed(() => Math.max(1, Math.ceil(props.labels.length / Math.max(1, Math.floor(plotW.value / 56)))))

const tip = computed(() => {
  const i = hover.value
  if (i === null) return null
  const cx = PAD.left + band.value * i + band.value / 2
  return { i, left: Math.min(Math.max(cx, 80), width.value - 80) }
})
</script>

<template>
  <figure class="chart">
    <div ref="host" class="chart__plot" @pointerleave="hover = null">
      <svg :width="width" :height="height" role="img" :aria-label="caption">
        <g class="chart__grid">
          <template v-for="v in ticks" :key="v">
            <line :x1="PAD.left" :x2="width - PAD.right" :y1="y(v)" :y2="y(v)" />
            <text :x="PAD.left - 8" :y="y(v) + 4" text-anchor="end">{{ compactNumber(v, loc) }}</text>
          </template>
        </g>
        <g v-for="(lab, i) in labels" :key="lab">
          <path v-for="s in series" :key="s.key" :d="column(i, s.values[i] ?? 0, s.shade === 'light' ? barW : barW * 0.55)" :class="`bar bar--${s.shade}`" :opacity="hover !== null && hover !== i ? 0.45 : 1" />
          <text v-if="i % labelEvery === 0" class="chart__x" :x="PAD.left + band * i + band / 2" :y="height - 6" text-anchor="middle">{{ lab.slice(5) }}</text>
          <rect
            class="chart__hit"
            :x="PAD.left + band * i"
            :y="PAD.top"
            :width="band"
            :height="plotH"
            tabindex="0"
            :aria-label="`${lab}: ${series.map((s) => `${s.label} ${format(s.values[i] ?? 0)}`).join(', ')}`"
            @pointerenter="hover = i"
            @focus="hover = i"
            @blur="hover = null"
          />
        </g>
      </svg>
      <div v-if="tip" class="chart__tip" :style="{ left: `${tip.left}px` }" role="status">
        <p class="chart__tip-date">{{ labels[tip.i] }}</p>
        <p v-for="s in series" :key="s.key" class="chart__tip-row">
          <span class="chart__key" :class="`chart__key--${s.shade}`" />
          <strong class="jt-num">{{ format(s.values[tip.i] ?? 0) }}</strong>
          <span class="jt-muted">{{ s.label }}</span>
        </p>
      </div>
    </div>
    <figcaption class="chart__foot">
      <span v-for="s in series" :key="s.key" class="chart__legend">
        <span class="chart__swatch" :class="`chart__key--${s.shade}`" />{{ s.label }}
      </span>
      <button type="button" class="jt-link-button chart__table-toggle" :aria-expanded="showTable" @click="showTable = !showTable">
        {{ showTable ? t('dashboard.hide_table') : t('dashboard.show_table') }}
      </button>
    </figcaption>
    <table v-if="showTable" class="chart__table">
      <caption class="jt-visually-hidden">{{ caption }}</caption>
      <thead>
        <tr>
          <th scope="col">{{ t('dashboard.date') }}</th>
          <th v-for="s in series" :key="s.key" scope="col">{{ s.label }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(lab, i) in labels" :key="lab">
          <td>{{ lab }}</td>
          <td v-for="s in series" :key="s.key" class="jt-num">{{ format(s.values[i] ?? 0) }}</td>
        </tr>
      </tbody>
    </table>
  </figure>
</template>

<style scoped src="./chart.css"></style>
