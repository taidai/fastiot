<template>
  <view class="trend">
    <view v-if="trend.samples.length" class="trend-frame">
      <svg :viewBox="`0 0 ${trend.width} ${trend.height}`" role="group" tabindex="0" class="trend-svg"
        aria-label="历史趋势；方向键读取样本，数据表提供相同记录"
        @mousemove="selectFromPointer" @mouseleave="selected = -1" @keydown="onKeydown">
        <line v-for="tick in yTicks" :key="`y-${tick.y}`" class="grid" :x1="trend.left" :x2="trend.right" :y1="tick.y" :y2="tick.y" />
        <g v-html="axisMarkup"></g>
        <polyline v-for="(segment, index) in trend.segments" v-show="segment.length > 1" :key="`line-${index}`"
          class="series-line" :points="linePoints(segment)" fill="none" />
        <template v-for="(segment, index) in trend.segments">
          <circle v-if="segment.length === 1" :key="`dot-${index}`" class="series-dot" :cx="segment[0].x" :cy="segment[0].y" r="4" />
        </template>
        <template v-if="selectedPoint">
          <line class="crosshair" :x1="selectedPoint.x" :x2="selectedPoint.x" :y1="trend.top" :y2="trend.bottom" />
          <circle class="selected-dot" :cx="selectedPoint.x" :cy="selectedPoint.y" r="5" />
        </template>
      </svg>
      <view v-if="selectedPoint" class="readout" role="status" aria-live="polite">
        <strong>{{ selectedPoint.value }}<text v-if="unit"> {{ unit }}</text></strong>
        <text>{{ historyTimeText(selectedPoint.record) }} · {{ qualityText(selectedPoint.record.quality) }}</text>
        <text v-if="selectedPoint.record.reason"> · {{ selectedPoint.record.reason }}</text>
      </view>
      <view v-else class="readout muted">移动指针或使用方向键读取观测；单点也会显示。</view>
      <view class="trend-foot">无效观测、质量事件与超过声明采样周期的空档不连线；所有值可在数据表中读取。</view>
    </view>
    <view v-else class="empty">这个时间范围没有可绘制的数值观测。质量事件与无效记录仍可在数据表中查看。</view>
  </view>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue"
import type { HistoryRecord } from "../../types/api"
import { buildTrend, historyTimeText, qualityText } from "./history"
import type { TrendPoint } from "./history"

const props = defineProps<{ records: HistoryRecord[]; start: Date; end: Date; unit?: string; cycleMs?: number }>()
const selected = ref(-1)
const trend = computed(() => buildTrend(props.records, props.start, props.end, 960, 360, props.cycleMs || 0))
const selectedPoint = computed(() => selected.value >= 0 ? trend.value.samples[selected.value] : undefined)
watch(() => props.records, () => { selected.value = -1 })

const yTicks = computed(() => [0, 0.5, 1].map(ratio => ({
  y: trend.value.top + ratio * (trend.value.bottom - trend.value.top),
  label: formatValue(trend.value.yMax - ratio * (trend.value.yMax - trend.value.yMin)),
})))
const xTicks = computed(() => [
  { x: trend.value.left, anchor: "start", time: trend.value.startMs },
  { x: (trend.value.left + trend.value.right) / 2, anchor: "middle", time: (trend.value.startMs + trend.value.endMs) / 2 },
  { x: trend.value.right, anchor: "end", time: trend.value.endMs },
].map(tick => ({ ...tick, label: tickTime(tick.time) })))
const axisMarkup = computed(() => [
  ...yTicks.value.map(tick => `<text class="axis-label" x="4" y="${tick.y + 4}">${tick.label}</text>`),
  ...xTicks.value.map(tick => `<text class="axis-label" x="${tick.x}" y="${trend.value.height - 14}" text-anchor="${tick.anchor}">${tick.label}</text>`),
].join(""))
function formatValue(value: number) {
  return Number(value.toFixed(2)).toString()
}
function tickTime(value: number) {
  const date = new Date(value)
  const day = `${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`
  const time = date.toLocaleTimeString("zh-CN", { hour12: false, hour: "2-digit", minute: "2-digit" })
  return `${day} ${time}`
}
function linePoints(segment: TrendPoint[]) {
  return segment.map(point => `${point.x.toFixed(2)},${point.y.toFixed(2)}`).join(" ")
}
function selectFromPointer(event: MouseEvent) {
  if (!trend.value.samples.length) return
  const svg = event.currentTarget as SVGSVGElement
  const bounds = svg.getBoundingClientRect()
  const x = (event.clientX - bounds.left) * trend.value.width / bounds.width
  let nearest = 0
  for (let index = 1; index < trend.value.samples.length; index++) {
    if (Math.abs(trend.value.samples[index].x - x) < Math.abs(trend.value.samples[nearest].x - x)) nearest = index
  }
  selected.value = nearest
}
function onKeydown(event: KeyboardEvent) {
  const count = trend.value.samples.length
  if (!count) return
  if (event.key === "ArrowRight") selected.value = Math.min(count - 1, selected.value + 1)
  else if (event.key === "ArrowLeft") selected.value = Math.max(0, selected.value < 0 ? count - 1 : selected.value - 1)
  else if (event.key === "Home") selected.value = 0
  else if (event.key === "End") selected.value = count - 1
  else return
  event.preventDefault()
}
</script>

<style scoped>
.trend-frame { border: 1px solid var(--line); border-radius: 12px; background: rgba(255,255,255,.25); padding: 10px 12px; }
.trend-svg { display: block; width: 100%; height: auto; max-height: 52vh; outline: none; touch-action: pan-y; }
.trend-svg:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.grid { stroke: var(--line); stroke-width: 1; }
:deep(.axis-label) { fill: var(--faint); font-size: 10px; font-variant-numeric: tabular-nums; }
.series-line { stroke: var(--accent); stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; }
.series-dot { fill: var(--glass-strong); stroke: var(--accent); stroke-width: 2; }
.crosshair { stroke: var(--faint); stroke-width: 1; }
.selected-dot { fill: var(--glass-strong); stroke: var(--accent); stroke-width: 2; }
.readout { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; min-height: 30px; padding: 7px 4px 0; color: var(--muted); font-size: 11px; }
.readout strong { color: var(--ink); font-size: 17px; }
.trend-foot { padding: 5px 4px 0; color: var(--faint); font-size: 10px; line-height: 1.6; }
</style>
