<template>
  <view class="daily-stats" :class="{ 'with-trend': !!bar }" :aria-busy="loading" aria-label="今日统计">
    <view v-for="item in items" :key="item.key" class="dfig" :data-daily-stat="item.key" tabindex="0" :title="detail(item.key)" :aria-label="`${item.label} ${dailyMetricText(stats?.[item.key])} ${item.unit}；${detail(item.key)}`">
      <text>{{ item.label }}</text>
      <b>{{ dailyMetricText(stats?.[item.key]) }}<small>{{ item.unit }}</small></b>
      <view class="stat-note" :role="error ? 'alert' : undefined">{{ error || (loading && !stats ? '读取中…' : dailyMetricDetail(stats?.[item.key])) }}</view>
    </view>
    <OverviewTrend v-if="bar" :records="bar.records" :start="bar.start" :end="bar.end" :unit="bar.unit" :label="bar.label" :caption="`24 小时${bar.label}`" @select="emit('select')" />
  </view>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ApiError } from '../../api/client'
import { loadDailyStats } from './daily-stats-api'
import { projectContext } from '../../stores/projects'
import { dailyMetricDetail, dailyMetricText } from './daily-stats'
import type { DailyStats } from './daily-stats'
import type { HistoryRecord } from '../../types/api'
import OverviewTrend from './OverviewTrend.vue'
type Bar = { records: HistoryRecord[]; start: Date; end: Date; label: string; unit?: string }
const props = defineProps<{ layerId: string; result?: DailyStats | null; bar?: Bar | null }>()
const emit = defineEmits<{ (event: 'select'): void }>()
const fetched = ref<DailyStats | null>(null)
const stats = computed(() => {
  const result = props.result === undefined ? fetched.value : props.result
  return result?.layer_id === props.layerId ? result : null
})
const loading = ref(false)
const error = ref('')
const items = [
  { key: 'energy', label: '今日电量', unit: 'kWh' },
  { key: 'runtime', label: '运行时长', unit: 'h' },
  { key: 'communication', label: '通信成功率', unit: '%' },
  { key: 'freshness', label: '数据新鲜度', unit: '秒' },
] as const
type MetricKey = typeof items[number]['key']
let serial = 0
let timer: ReturnType<typeof setInterval> | undefined
function detail(key: MetricKey) {
  const metric = stats.value?.[key]
  if (!metric) return error.value || '未读取'
  const parts = [dailyMetricDetail(metric)]
  if (key === 'communication') {
    if (metric.start && metric.end) parts.push(`${metric.start} → ${metric.end}，一分钟桶；成功 ${metric.successes} / 尝试 ${metric.attempts}`)
    parts.push('进程内通信窗口，重启后重新累计；不代表全天')
  } else if (key === 'freshness') {
    if (metric.observed_at) parts.push(`观测于 ${metric.observed_at}，${metric.time_basis === 'device' ? '设备时间' : '平台成功采集时间'}；不是页面刷新时间`)
  } else if (stats.value?.window) {
    const w = stats.value.window
    parts.push(`${w.date} · ${w.time_zone}；${w.start} → ${w.through}；有效 ${metric.covered_seconds || 0} 秒；缺失不补零`)
    if (metric.history_lifetime === 'process_memory') parts.push('运行历史仅覆盖本次进程已保留的观测，重启前缺口不补齐')
  }
  return parts.join('；')
}
async function load(clear = false) {
  const current = ++serial
  const layer = props.layerId
  if (clear) fetched.value = null
  error.value = ''
  if (props.result !== undefined) { loading.value = false; return }
  if (!layer) { fetched.value = null; loading.value = false; return }
  loading.value = true
  try {
    const result = await loadDailyStats(layer)
    if (current === serial && result.layer_id === props.layerId) fetched.value = result
  } catch (cause) {
    if (current === serial) { fetched.value = null; error.value = cause instanceof ApiError && cause.status === 403 ? '无权限' : '统计读取失败' }
  } finally { if (current === serial) loading.value = false }
}
watch(() => [props.layerId, projectContext.generation, props.result], () => { void load(true) }, { immediate: true })
onMounted(() => { timer = setInterval(() => { void load() }, 30000) })
onBeforeUnmount(() => { serial++; clearInterval(timer) })
</script>

<style scoped>
.daily-stats { grid-column: span 4; display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); min-width: 0; }
.daily-stats.with-trend { grid-template-columns: repeat(4, minmax(0, 1fr)) 2fr; align-items: end; }
.dfig { min-width: 0; padding: 2px 14px; border-left: 1px solid var(--line); }
.dfig:first-child { border-left: 0; padding-left: 0; }
.dfig > text { display: block; color: var(--muted); font-size: var(--fs-daily-label); letter-spacing: .06em; }
.dfig b { font-size: var(--fs-daily-value); font-weight: 600; }
.dfig b small { margin-left: 2px; color: var(--muted); font-size: var(--fs-daily-label); font-weight: 400; }
.stat-note { color: var(--muted); font-size: var(--fs-micro); overflow-wrap: anywhere; }
@media (max-width: 760px) { .daily-stats, .daily-stats.with-trend { grid-column: 1 / -1; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px 0; } .daily-stats.with-trend :deep(.overview-trend) { grid-column: 1 / -1; margin-top: 8px; padding-left: 0; border-left: 0; } }
</style>
