<template>
  <view class="page" :style="glassStyle">
    <TopBar active="overview" :project="projectName" meta="历史数据" :pending="unackedAlarms.length" :alpha="alpha"
      @navigate="navigate" @bell="alarmOpen = true" @alpha="setAlpha" />
    <view class="history glass">
      <view class="hist-head">
        <button class="ghost" @click="back">← 返回</button>
        <view class="title-wrap"><h1>{{ pointLabel }}</h1><text v-if="pointUnit" class="unit">{{ pointUnit }}</text></view>
        <view class="spacer"></view>
        <button class="ghost" :disabled="!point || loading || exportBusy || !records.length" @click="exportCsv">{{ exportBusy ? '导出中…' : '导出 CSV' }}</button>
      </view>
      <view class="hist-sub">{{ rangeLabel }} · {{ granLabel }} · 保留期未由查询接口返回 · 缺失段不补值</view>
      <view class="hist-bar">
        <label class="point-picker">点位<select v-model="point" aria-label="历史点位"><option v-for="item in pointOptions" :key="item.id" :value="item.id">{{ item.label }}{{ item.unit ? `（${item.unit}）` : '' }}</option></select></label>
        <view class="chips"><button v-for="item in ranges" :key="item.key" :class="{active: range === item.key}" @click="range = item.key">{{ item.label }}</button></view>
        <view class="chips"><button v-for="item in granules" :key="item.key" :class="{active: gran === item.key}" @click="gran = item.key">{{ item.label }}</button></view>
        <view class="chips"><button :class="{active: view === 'table'}" @click="view = 'table'">数据表</button><button :class="{active: view === 'chart'}" @click="view = 'chart'">曲线</button></view>
      </view>
      <view v-if="loadError" class="error" role="alert">历史查询失败：{{ loadError }}</view>
      <view v-else-if="loading" class="empty">正在查询真实历史数据…</view>
      <template v-else>
        <HistoryTable v-if="view === 'table'" :records="records" />
        <HistoryTrend v-else :records="records" :start="bounds.start" :end="bounds.end" :unit="pointUnit" :cycle-ms="cycleMs" />
      </template>
      <view v-if="exportError" class="error export-message" role="alert">导出失败：{{ exportError }}</view>
      <view v-else-if="exportMessage" class="export-message">{{ exportMessage }}</view>
      <view class="hist-foot"><span>匹配 {{ matched }} 条 · 本页 {{ records.length }} 条<template v-if="truncated"> · 超出单页，请缩小范围或导出</template></span><span>单页上限 2000 条 · CSV 导出上限 10000 条</span></view>
    </view>
    <AlarmCenter v-if="alarmOpen" :alarms="session.alarms" :busy-id="alarmBusy" :error="alarmError" @close="alarmOpen = false"
      @ack="runAlarm('ack', $event)" @close-alarm="runAlarm('close', $event)" @history="selectHistoryPoint" @locate="goDevice" />
  </view>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue"
import { onHide, onLoad, onShow } from "@dcloudio/uni-app"
import TopBar from "../../components/TopBar.vue"
import AlarmCenter from "../../components/AlarmCenter.vue"
import HistoryTable from "../../components/HistoryTable.vue"
import HistoryTrend from "./HistoryTrend.vue"
import { downloadText, request } from "../../api/client"
import { acknowledgeAlarm, closeAlarm, loadHistory, nameOf, requireAuth, session, startPolling, stopPolling } from "../../stores/session"
import type { HistoryRecord } from "../../types/api"
import { businessName, unitOf } from "../overview/overview"

const point = ref("")
const range = ref("15m")
const gran = ref("raw")
const view = ref<"table" | "chart">("table")
const records = ref<HistoryRecord[]>([])
const loading = ref(false)
const loadError = ref("")
const matched = ref(0)
const truncated = ref(false)
const sampleIntervalMs = ref(0)
const bounds = ref(period("15m"))
const exportBusy = ref(false)
const exportError = ref("")
const exportMessage = ref("")
const alarmOpen = ref(false)
const alarmBusy = ref("")
const alarmError = ref("")
const alpha = ref(Number(uni.getStorageSync("fastiot-overview-alpha") || 42))
const glassStyle = computed(() => ({ "--glass": `rgba(255,255,255,${(alpha.value / 100).toFixed(2)})` }))
let authorized = false
let querySerial = 0
let exportSerial = 0

const projectName = computed(() => {
  const root = session.snapshot?.roots[0]
  const node = session.snapshot?.nodes.find(item => item.id === root)
  return node ? nameOf(node) : "未选择项目"
})
const unackedAlarms = computed(() => session.alarms.filter(alarm => alarm.active && alarm.state === "active"))
const pointOptions = computed(() => (session.snapshot?.nodes || []).filter(node => ["variable", "modbusIo", "opcuaPoint", "mqttSub"].includes(node.type)).map(node => ({
  id: node.id,
  label: businessName(node),
  unit: unitOf(node),
})))
const currentNode = computed(() => session.snapshot?.nodes.find(node => node.id === point.value))
const pointLabel = computed(() => currentNode.value ? businessName(currentNode.value) : point.value ? "未命名点位" : "请选择点位")
const pointUnit = computed(() => unitOf(currentNode.value) || records.value.find(record => record.unit)?.unit || "")
const rangeLabel = computed(() => ranges.find(item => item.key === range.value)?.label || range.value)
const granLabel = computed(() => granules.find(item => item.key === gran.value)?.label || gran.value)
const cycleMs = computed(() => gran.value === "5m" ? 5 * 60_000 : gran.value === "1h" ? 60 * 60_000 : sampleIntervalMs.value)
const ranges = [
  { key: "15m", label: "15分钟" }, { key: "1h", label: "1小时" }, { key: "24h", label: "24小时" },
  { key: "3d", label: "3天" }, { key: "7d", label: "7天" },
]
const granules = [{ key: "raw", label: "原始" }, { key: "5m", label: "5分钟平均" }, { key: "1h", label: "1小时平均" }]
function setAlpha(value: number) { alpha.value = value; uni.setStorageSync("fastiot-overview-alpha", value) }
function period(key: string) {
  const minutes = ({ "15m": 15, "1h": 60, "24h": 1440, "3d": 4320, "7d": 10080 } as Record<string, number>)[key] || 15
  const end = new Date()
  return { start: new Date(end.getTime() - minutes * 60 * 1000), end }
}
async function load() {
  if (!point.value || !authorized) return
  const serial = ++querySerial
  const currentBounds = period(range.value)
  bounds.value = currentBounds
  loading.value = true
  loadError.value = ""
  exportMessage.value = ""
  exportError.value = ""
  records.value = []
  matched.value = 0
  truncated.value = false
  try {
    const result = await loadHistory(point.value, currentBounds.start, currentBounds.end, gran.value)
    if (serial !== querySerial) return
    const first = result.results[0]
    records.value = first?.records || []
    matched.value = first?.matched ?? result.total_matched ?? records.value.length
    truncated.value = !!first?.truncated
    sampleIntervalMs.value = first?.sample_interval_ms || 0
  } catch (error) {
    if (serial !== querySerial) return
    records.value = []
    loadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    if (serial === querySerial) loading.value = false
  }
}
function exportQuery(pointId: string, start: Date, end: Date, aggregate: string) {
  const params = new URLSearchParams({ type: "value", start: start.toISOString(), end: end.toISOString(), aggregate })
  params.append("node_id", pointId)
  return params.toString()
}
async function exportCsv() {
  if (!point.value || exportBusy.value) return
  const serial = ++exportSerial
  const snapshot = { point: point.value, label: pointLabel.value, start: bounds.value.start, end: bounds.value.end, aggregate: gran.value }
  exportBusy.value = true
  exportError.value = ""
  exportMessage.value = ""
  try {
    const result = await request<string>(`/history/export?${exportQuery(snapshot.point, snapshot.start, snapshot.end, snapshot.aggregate)}`)
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
watch(pointOptions, options => { if (!point.value && options.length) point.value = options[0].id })
onLoad(options => { point.value = (options?.point as string) || "" })
onMounted(() => {
  authorized = requireAuth()
  if (!authorized) return
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
.page { padding: 16px; overflow-x: hidden; }
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
