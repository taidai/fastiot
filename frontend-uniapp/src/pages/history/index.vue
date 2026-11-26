<template>
  <view class="page" :class="appearance.theme === 'light' ? '' : `theme-${appearance.theme}`">
    <TopBar active="overview" meta="历史数据" :pending="unackedAlarms.length" :alpha="appearance.alpha"
      :theme="appearance.theme" themes :show-workbench="false" show-appearance show-bell @navigate="navigate" @bell="alarmOpen = true" @alpha="setAlpha" @theme="setTheme" @reset="resetAppearance" />
    <view class="history glass">
      <view class="hist-head">
        <button class="ghost" @click="back">← 返回</button>
        <view class="title-wrap"><h1>{{ pointLabel }}</h1><text v-if="pointUnit" class="unit">{{ pointUnit }}</text></view>
        <view class="spacer"></view>
        <button class="ghost" :disabled="!point || loading || exportBusy || !records.length" @click="exportCsv">{{ exportBusy ? '导出中…' : '导出 CSV' }}</button>
      </view>
      <view class="hist-sub">{{ rangeLabel }} · 请求{{ granLabel }} · {{ metadataText }} · 缺失段不补值</view>
      <view class="hist-bar">
        <label class="point-picker">点位<select v-model="point" aria-label="历史点位"><option v-for="item in pointOptions" :key="item.id" :value="item.id">{{ item.label }}{{ item.unit ? `（${item.unit}）` : '' }}</option></select></label>
        <view class="chips"><button v-for="item in ranges" :key="item.key" :class="{active: range === item.key}" @click="range = item.key">{{ item.label }}</button></view>
        <view class="chips"><button v-for="item in granules" :key="item.key" :class="{active: gran === item.key}" @click="gran = item.key">{{ item.label }}</button></view>
        <view class="chips"><button :class="{active: view === 'table'}" @click="view = 'table'">数据表</button><button :class="{active: view === 'chart'}" @click="view = 'chart'">曲线</button></view>
      </view>
      <view v-if="loadError" class="error" role="alert">历史查询失败：{{ loadError }}</view>
      <view v-else-if="loading" class="empty">正在查询真实历史数据…</view>
      <template v-else>
        <HistoryTable v-if="view === 'table'" :records="records" :cycle-ms="cycleMs" />
        <HistoryTrend v-else :records="records" :start="bounds.start" :end="bounds.end" :unit="pointUnit" :cycle-ms="cycleMs" />
      </template>
      <view v-if="exportError" class="error export-message" role="alert">导出失败：{{ exportError }}</view>
      <view v-else-if="exportMessage" class="export-message">{{ exportMessage }}</view>
      <view class="hist-foot"><span>匹配 {{ matched }} 条 · 第 {{ pageNumber }} 页 {{ records.length }} 条<template v-if="truncated"> · 已截断，尚有更早记录</template></span><span>单页上限 2000 条 · CSV 导出上限 10000 条（含质量事件）</span></view>
      <view class="chips pagination"><button :disabled="loading || pageNumber === 1" @click="load()">最新一页</button><button :disabled="loading || !truncated || !nextCursor" @click="load(true)">更早记录</button></view>
    </view>
    <AlarmCenter v-if="alarmOpen" :alarms="session.alarms" :busy-id="alarmBusy" :error="alarmError" @close="alarmOpen = false"
      @ack="runAlarm('ack', $event)" @close-alarm="runAlarm('close', $event)" @history="selectHistoryPoint" @locate="goDevice" />
  </view>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue"
import "../overview/theme.css"
import { onHide, onLoad, onShow } from "@dcloudio/uni-app"
import TopBar from "../../components/TopBar.vue"
import AlarmCenter from "../../components/AlarmCenter.vue"
import HistoryTable from "../../components/HistoryTable.vue"
import HistoryTrend from "./HistoryTrend.vue"
import { downloadText, request } from "../../api/client"
import { acknowledgeAlarm, closeAlarm, requireAuth, session, startPolling, stopPolling } from "../../stores/session"
import { projectContext } from "../../stores/projects"
import type { HistoryRecord, HistoryPointResult, HistoryQueryResponse } from "../../types/api"
import { buildHistoryQuery } from "./history"
import type { HistoryCursor, HistoryScope } from "./history"
import { isDerivedInput } from '../../components/design/strategy'
import { businessName, unitOf } from "../overview/overview"
import { DEFAULT_APPEARANCE, applyAppearanceDocument, normalizeAppearance } from "../overview/appearance"
import type { Appearance, AppearanceTheme } from "../overview/appearance"

const point = ref("")
const range = ref("15m")
const gran = ref("5s")
const view = ref<"table" | "chart">("table")
const records = ref<HistoryRecord[]>([])
const loading = ref(false)
const loadError = ref("")
const matched = ref(0)
const truncated = ref(false)
const metadata = ref<HistoryPointResult>()
const activeScope = ref<HistoryScope>()
const nextCursor = ref<HistoryCursor>()
const pageNumber = ref(1)
const bounds = ref(period("15m"))
const exportBusy = ref(false)
const exportError = ref("")
const exportMessage = ref("")
const alarmOpen = ref(false)
const alarmBusy = ref("")
const alarmError = ref("")
function storedAppearance(): Appearance {
  const stored = uni.getStorageSync("fastiot-overview-appearance")
  if (stored) {
    try { return normalizeAppearance(typeof stored === "string" ? JSON.parse(stored) : stored) }
    catch { return { ...DEFAULT_APPEARANCE } }
  }
  return normalizeAppearance({ alpha: Number(uni.getStorageSync("fastiot-overview-alpha") || 42) })
}
const appearance = ref<Appearance>(storedAppearance())
function persistAppearance() { uni.setStorageSync("fastiot-overview-appearance", JSON.stringify(appearance.value)) }
function setAlpha(value: number) { appearance.value = normalizeAppearance({ ...appearance.value, alpha: value }); persistAppearance(); applyAppearanceDocument(appearance.value) }
function setTheme(value: AppearanceTheme) { appearance.value = normalizeAppearance({ ...appearance.value, theme: value }); persistAppearance(); applyAppearanceDocument(appearance.value) }
function resetAppearance() { appearance.value = { ...DEFAULT_APPEARANCE }; persistAppearance(); applyAppearanceDocument(appearance.value) }
let authorized = false
let querySerial = 0
let exportSerial = 0

const unackedAlarms = computed(() => session.alarms.filter(alarm => alarm.active && alarm.state === "active"))
const pointOptions = computed(() => (session.snapshot?.nodes || []).filter(node => ["variable", "modbusIo", "opcuaPoint", "mqttSub"].includes(node.type) || isDerivedInput(node)).map(node => ({
  id: node.id,
  label: businessName(node),
  unit: unitOf(node),
})))
const currentNode = computed(() => session.snapshot?.nodes.find(node => node.id === point.value))
const pointLabel = computed(() => currentNode.value ? businessName(currentNode.value) : point.value ? "未命名点位" : "请选择点位")
const pointUnit = computed(() => unitOf(currentNode.value) || records.value.find(record => record.unit)?.unit || "")
const rangeLabel = computed(() => ranges.find(item => item.key === range.value)?.label || range.value)
const granLabel = computed(() => granules.find(item => item.key === gran.value)?.label || gran.value)
const cycleMs = computed(() => metadata.value?.actual_interval_ms || 0)
const metadataText = computed(() => {
  const result = metadata.value
  if (!result) return "尚无查询结果"
  const interval = result.actual_interval_ms ? `${result.actual_interval_ms / 1000}秒` : "周期未知"
  const mode = result.aggregation === "mean" ? "窗口平均" : "原始观测"
  const source = result.source === "modbus_runtime" ? "运行历史" : result.source === "siot" ? "点位历史" : result.source
  const retention = result.retention?.kind === "count_per_point_per_kind" ? `保留每点观测/事件各${result.retention.limit}条（非固定天数）` : "保留期未知"
  return `实际${interval} · ${mode} · ${source} · ${retention}`
})
const ranges = [
  { key: "15m", label: "15分钟" }, { key: "1h", label: "1小时" }, { key: "24h", label: "24小时" },
  { key: "3d", label: "3天" }, { key: "7d", label: "7天" },
]
const granules = [{ key: "raw", label: "自动" }, { key: "1s", label: "1秒" }, { key: "5s", label: "5秒" }, { key: "30s", label: "30秒" }, { key: "1m", label: "1分钟" }]
function period(key: string) {
  const minutes = ({ "15m": 15, "1h": 60, "24h": 1440, "3d": 4320, "7d": 10080 } as Record<string, number>)[key] || 15
  const end = new Date()
  return { start: new Date(end.getTime() - minutes * 60 * 1000), end }
}
async function load(older = false) {
  const serial = ++querySerial
  exportSerial += 1
  exportBusy.value = false
  if (!point.value || !authorized) return
  const cursor = older ? nextCursor.value : undefined
  if (older && (!cursor || !activeScope.value)) return
  const scope = older ? activeScope.value! : { point: point.value, type: currentNode.value?.points.some(item => item.type === 'easyemsAggregate' && (item.tombstone || 0) % 2 === 0) ? 'easyemsAggregateValue' : 'value', ...period(range.value), aggregate: gran.value }
  bounds.value = { start: scope.start, end: scope.end }
  loading.value = true
  loadError.value = ""
  exportMessage.value = ""
  exportError.value = ""
  records.value = []
  matched.value = 0
  truncated.value = false
  metadata.value = undefined
  try {
    const result = await request<HistoryQueryResponse>(`/history?${buildHistoryQuery(scope, cursor)}`)
    if (serial !== querySerial) return
    const first = result.results.find(item => item.node_id === scope.point)
    if (!first) throw new Error("历史响应缺少所选点位")
    records.value = first.records || []
    matched.value = first.matched
    truncated.value = first.truncated
    metadata.value = first
    activeScope.value = scope
    nextCursor.value = first.truncated && first.next_before ? { before: first.next_before, count: first.next_before_count ?? 0 } : undefined
    pageNumber.value = older ? pageNumber.value + 1 : 1
  } catch (error) {
    if (serial !== querySerial) return
    records.value = []
    loadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    if (serial === querySerial) loading.value = false
  }
}
async function exportCsv() {
  if (!activeScope.value || loading.value || loadError.value || exportBusy.value) return
  const serial = ++exportSerial
  const snapshot = { ...activeScope.value, label: pointLabel.value }
  exportBusy.value = true
  exportError.value = ""
  exportMessage.value = ""
  try {
    const result = await request<string>(`/history/export?${buildHistoryQuery(snapshot)}`)
    if (serial !== exportSerial) return
    downloadText(`fastiot-history-${snapshot.label}-${snapshot.start.toISOString().slice(0, 10)}.csv`, `﻿${result}`, "text/csv;charset=utf-8")
    exportMessage.value = "CSV 已由后端生成并开始下载。"
  } catch (error) {
    if (serial === exportSerial) exportError.value = error instanceof Error ? error.message : String(error)
  } finally { if (serial === exportSerial) exportBusy.value = false }
}
function back() { uni.navigateBack() }
function navigate(key: string) { if (key !== "overview") uni.navigateTo({ url: `/pages/${key}/index` }); else uni.redirectTo({ url: "/pages/overview/index" }) }
async function runAlarm(action: "ack" | "close", id: string) {
  alarmBusy.value = id
  alarmError.value = ""
  try { await (action === "ack" ? acknowledgeAlarm(id) : closeAlarm(id)) }
  catch (error) { alarmError.value = error instanceof Error ? error.message : String(error) }
  finally { alarmBusy.value = "" }
}
function selectHistoryPoint(id: string) { point.value = id; alarmOpen.value = false }
function goDevice(id: string) { alarmOpen.value = false; uni.redirectTo({ url: `/pages/overview/index?device=${encodeURIComponent(id)}` }) }

watch([point, range, gran], () => {
  exportSerial += 1
  exportBusy.value = false
  void load()
})
watch(() => projectContext.id, (_, previous) => {
  if (!previous) return
  querySerial += 1; exportSerial += 1
  point.value = ""; records.value = []; metadata.value = undefined; activeScope.value = undefined
  nextCursor.value = undefined; matched.value = 0; truncated.value = false; loading.value = false; exportBusy.value = false
})
watch(pointOptions, options => { if (!point.value && options.length) point.value = options[0].id })
onLoad(options => { point.value = (options?.point as string) || "" })
onMounted(() => {
  authorized = requireAuth()
  if (!authorized) return
  applyAppearanceDocument(appearance.value)
  if (!point.value && pointOptions.value.length) point.value = pointOptions.value[0].id
  void load()
})
onShow(() => {
  authorized = requireAuth()
  if (authorized) { startPolling(); void load() }
})
onHide(() => { querySerial += 1; exportSerial += 1; exportBusy.value = false; stopPolling() })
</script>

<style scoped>
.page { min-height: 100vh; padding: 16px; overflow-x: hidden; }
.history { margin-top: 14px; padding: 18px 20px; min-width: 0; }
.hist-head { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.title-wrap { display: flex; align-items: baseline; gap: 8px; min-width: 0; }
h1 { margin: 0; font-size: 24px; overflow-wrap: anywhere; }
.unit { color: var(--faint); font-size: 12px; }
.hist-sub { margin: 5px 0 12px; color: var(--faint); font-size: 11px; }
.hist-bar { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; margin-bottom: 12px; }
.point-picker { display: flex; align-items: center; gap: 7px; color: var(--faint); font-size: 11px; }
.point-picker select { max-width: 230px; min-height: 36px; border: 1px solid var(--glass-border); border-radius: 9px; background: rgba(255,255,255,.6); padding: 5px 8px; }
.hist-foot { display: flex; justify-content: space-between; gap: 12px; flex-wrap: wrap; margin-top: 8px; color: var(--faint); font-size: 10.5px; }
.export-message { margin-top: 8px; color: var(--accent); font-size: 11px; }
.error.export-message { color: var(--danger); }
:deep(.table-wrap) { max-height: calc(100vh - 300px); border: 1px solid var(--line); border-radius: 12px; background: rgba(255,255,255,.2); }
:deep(th) { position: sticky; top: 0; z-index: 1; background: rgba(237,244,247,.94); backdrop-filter: blur(16px); }
@media (max-width: 760px) { .page { padding: 8px; } .history { padding: 14px; } .hist-bar { align-items: flex-start; } }
</style>
