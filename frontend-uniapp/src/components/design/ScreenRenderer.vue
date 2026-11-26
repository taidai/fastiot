<template>
  <view class="screen-renderer" :class="{ 'with-hero': !!hero }" :data-screen-version="document.version" :data-screen-layer="layerId">
    <view v-if="hero" class="hero glass">
      <view class="crumb">{{ hero.crumb }}</view>
      <view class="headline"><h1>{{ hero.title }}</h1><text class="online">{{ hero.online }}</text></view>
      <view v-if="heroMetrics.length" class="metrics">
        <view v-for="(metric, index) in heroMetrics" :key="index" class="metric">
          <view class="k"><svg viewBox="0 0 24 24" aria-hidden="true"><path :d="metricIcons[index]" /></svg>{{ metric.label }}</view><view class="v">{{ metric.value }}<text v-if="metric.unit">{{ metric.unit }}</text></view>
          <view class="metric-quality">{{ metric.quality }} · {{ metric.time }}<text v-if="metric.target"> · 目标 {{ metric.target }}</text></view>
        </view>
      </view>
    </view>
    <view class="screen-body" :class="{ 'without-side': !showSide }">
    <view v-if="document.inherited_from" class="provenance">继承自 {{ nodeName(document.inherited_from) }} · v{{ document.version }}</view>
    <view v-for="slot in visibleSlots" :key="slot" class="screen-slot" :class="[`slot-${slot}`, { side: slot === 'side' }]" :data-slot="slot" :style="{ '--screen-columns': document.columns }">
      <view v-for="block in slotBlocks(slot)" :key="block.type" class="screen-block" :data-block="block.type" :style="{ gridColumn: `span ${Math.min(block.span, document.columns)}` }">
        <view v-if="!applicable(block.type)" class="panel glass empty">{{ definition(block.type)?.label }} · 本层不适用，请重新配置</view>
        <view v-else-if="block.type === 'status'" class="status glass"><span>{{ runtimeError || onlineText }}</span><span>观测于 {{ latestObservation }} · 查询于 {{ shortTime(lastSync) }}</span></view>
        <view v-else-if="block.type === 'kpi'" class="metrics">
          <view v-for="(metric, index) in metrics" :key="index" class="metric">
            <view class="k"><svg viewBox="0 0 24 24" aria-hidden="true"><path :d="metricIcons[index]" /></svg>{{ metric.label }}</view><view class="v">{{ metric.value }}<text v-if="metric.unit">{{ metric.unit }}</text></view>
            <view class="metric-quality">{{ metric.quality }} · {{ metric.time }}<text v-if="metric.target"> · 目标 {{ metric.target }}</text></view>
          </view>
        </view>
        <FlowScene v-else-if="block.type === 'flow'" :layer-name="layerName" :metrics="pointMetrics.slice(0, 3)" :topology="topology.map(item => ({ ...item, detail: block.binding.detail ? item.detail : '' }))" />
        <section v-else-if="block.type === 'children' || block.type === 'members'" class="panel glass">
          <view class="card-title">{{ definition(block.type)?.label }}</view>
          <template v-if="block.type === 'children'"><button v-for="node in children" :key="node.id" class="object-link" @click="emit('select', node.id)">{{ nameOf(node) }} · {{ kindLabel(node) }}</button><view v-if="!children.length" class="empty">没有下级对象</view></template>
          <template v-else><view v-for="member in members" :key="member.role" class="run"><span>{{ member.role }}</span><button v-if="member.node_id && scopeNodes.has(member.node_id)" class="object-link" @click="emit('select', member.node_id)">{{ nodeName(member.node_id) }}</button><span v-else>未绑定或不在本层范围</span></view><view v-if="!members.length" class="empty">没有成员配置</view></template>
        </section>
        <section v-else-if="block.type === 'devices'" class="panel glass">
          <view class="card-title">设备清单<text>{{ devices.length }}</text></view>
          <view class="table-wrap"><table><thead><tr><th>设备</th><th v-for="column in block.binding.columns" :key="column">{{ deviceColumnLabels[column] }}</th></tr></thead><tbody><tr v-for="device in devices" :key="deviceKey(device)"><td><button class="object-link" @click="emit('select', device.business_node_id || '')">{{ device.name }}</button></td><td v-for="column in block.binding.columns" :key="column">{{ deviceColumn(device, column) }}</td></tr><tr v-if="!devices.length"><td :colspan="block.binding.columns.length + 1" class="empty">没有已接入设备</td></tr></tbody></table></view>
        </section>
        <section v-else-if="block.type === 'deviceData'" class="panel glass">
          <DevicePanel v-if="devices[0]" :device="devices[0]" :values="scopedValues" :nodes="nodes" :allowed-tabs="block.binding.tabs" /><view v-else class="empty">没有已接入设备数据</view>
        </section>
        <section v-else-if="block.type === 'recent'" class="panel glass">
          <view class="card-title">最近数据 <button class="link" @click="openRecentHistory">查看全部</button></view>
          <view class="table-wrap"><table><thead><tr><th>时间</th><th v-for="column in recentColumns" :key="column.id">{{ column.label }}<text v-if="column.unit"> {{ column.unit }}</text></th></tr></thead><tbody>
            <tr v-for="row in recentRows" :key="row.time"><td class="num">{{ recentTime(row.time) }}</td><td v-for="(cell, index) in row.cells" :key="index" class="num">{{ cell ?? '—' }}</td></tr>
            <tr v-if="!recentColumns.length"><td class="empty">未绑定点位 · —</td></tr>
            <tr v-else-if="!recentRows.length"><td :colspan="recentColumns.length + 1" class="empty">{{ recentError || '暂无观测 · —' }}</td></tr>
          </tbody></table></view>
        </section>
        <section v-else-if="block.type === 'alarm'" class="panel glass">
          <slot name="alarm" :layer-id="layerId"><view class="card-title">本层告警 <text>{{ layerAlarms.length }}</text></view><view v-for="alarm in layerAlarms.slice(0, 3)" :key="alarm.id" class="alarm-mini"><b>{{ alarm.title }}</b><view class="meta">{{ alarm.severity }} · {{ shortTime(alarm.updated_at) }} · {{ alarm.state === 'acknowledged' ? '已接手，仍活动' : '待接手' }}</view></view><view v-if="!layerAlarms.length" class="empty">没有待处理告警</view></slot>
        </section>
        <section v-else-if="block.type === 'today'" class="panel glass screen-card"><slot name="today" :layer-id="layerId"><h5 class="card-title">今日执行</h5><TodayExecutionSummary :layer-id="layerId" /></slot></section>
        <section v-else-if="block.type === 'summary'" class="panel glass"><slot name="summary" :layer-id="layerId" :result="dailyStats"><view class="card-title">运行摘要</view><view v-if="dailyError" class="error" role="alert">{{ dailyError }}</view><OverviewDailyStats :layer-id="layerId" :result="dailyStats" /></slot></section>
        <section v-else-if="block.type === 'oplog'" class="panel glass"><view class="card-title">操作记录</view><view v-if="operationError" class="error">{{ operationError }}</view><view v-for="record in operations" :key="record.request_id" class="run"><span>{{ shortTime(record.updated_at || record.started_at) }} · {{ record.command_id }}</span><span>{{ commandStateText(record.state) }}</span></view><view v-if="!operationError && !operations.length" class="empty">暂无操作记录</view></section>
        <section v-else-if="block.type === 'trend'" class="panel glass screen-card"><h5 class="card-title">趋势 · {{ trendMetric.label }}</h5><OverviewTrend :records="trendRecords" :start="trendStart" :end="trendEnd" :unit="trendMetric.unit" :label="trendMetric.label" @select="history(trendMetric.id)" /><view v-if="trendError" class="error">{{ trendError }}</view><view v-else-if="!trendMetric.id" class="empty">未绑定或不在本层范围 · —</view></section>
      </view>
      <view v-if="showSide && slot === 'side' && $slots['after-side']" class="screen-peripheral" :style="{ gridColumn: `span ${document.columns}` }"><slot name="after-side" /></view>
    </view>
    </view>
  </view>
</template>

<script setup lang="ts">
import { isDerivedInput } from './strategy'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { projectContext } from '../../stores/projects'
import { ApiError } from '../../api/client'
import { loadDailyStats } from '../../pages/overview/daily-stats-api'
import { dailyMetricDetail, dailyMetricText } from '../../pages/overview/daily-stats'
import type { DailyStats } from '../../pages/overview/daily-stats'
import TodayExecutionSummary from '../../pages/overview/TodayExecutionSummary.vue'
import OverviewDailyStats from '../../pages/overview/OverviewDailyStats.vue'
import type { EmsAlarm, Snapshot, StateValue, HistoryRecord } from '../../types/api'
import { nameOf } from '../../types/api'
import type { LedgerDevice, QualityStats } from '../../pages/overview/overview'
import { alarmInLayer, boundDevicesForLayer, buildRecentTable, businessName, commandStateText, descendantNodeIds, deviceStatus, deviceKey, devicePointIds, healthLabel, isBusinessLayer, metricForPoint, shortTime, valueInLayer } from '../../pages/overview/overview'
import type { RecentTableRow } from '../../pages/overview/overview'
import { loadHistory } from '../../stores/session'
import { loadOperationRecords } from '../../pages/overview/operations'
import type { OperationRecord } from '../../pages/overview/operations'
import { kindLabel } from './design'
import { memberRolesOf } from './members'
import { automaticMetricLabel, screenBlockDefinition } from './screen'
import type { ScreenBlock, ScreenDocument, ScreenMetric } from './screen'
import FlowScene from '../FlowScene.vue'
import DevicePanel from '../../pages/overview/DevicePanel.vue'
import OverviewTrend from '../../pages/overview/OverviewTrend.vue'

const props = withDefaults(defineProps<{ document: ScreenDocument; snapshot: Snapshot | null; values: StateValue[]; quality: { devices: unknown[]; channels: unknown[] }; alarms: EmsAlarm[]; ledger: LedgerDevice[]; layerId: string; deviceId?: string; lastSync?: string; runtimeError?: string; hero?: { crumb: string; title: string; online: string } | null; showSide?: boolean }>(), { lastSync: '', runtimeError: '', hero: null, showSide: true })
const emit = defineEmits<{ (event: 'history', id: string): void; (event: 'select', id: string): void }>()
const nodes = computed(() => props.snapshot?.nodes || [])
const selected = computed(() => nodes.value.find(node => node.id === props.layerId) || null)
const layerName = computed(() => selected.value ? nameOf(selected.value) : '未选择对象')
const scopeNodes = computed(() => new Set([props.layerId, ...memberRolesOf(selected.value).flatMap(member => member.node_id ? [member.node_id] : [])].flatMap(id => [...descendantNodeIds(id, nodes.value)])))
const members = computed(() => memberRolesOf(selected.value))
const devices = computed(() => boundDevicesForLayer(props.layerId, nodes.value, props.ledger.filter(device => !!device.business_node_id && (device.point_count || 0) > 0 && (!props.deviceId || deviceKey(device) === props.deviceId)), members.value.flatMap(member => member.node_id ? [member.node_id] : [])))
const deviceIds = computed(() => new Set(devices.value.map(deviceKey)))
const knownIds = computed(() => new Set(props.ledger.map(deviceKey)))
const scopedValues = computed(() => props.values.filter(value => props.deviceId ? value.device_id === props.deviceId : valueInLayer(value, scopeNodes.value, deviceIds.value, knownIds.value)))
const bindingScopeNodes = computed(() => props.document.inherited_from ? descendantNodeIds(props.document.inherited_from, nodes.value) : scopeNodes.value)
const availablePoints = computed(() => new Set([...devicePointIds(nodes.value, devices.value), ...nodes.value.filter(node => bindingScopeNodes.value.has(node.id) && (node.type === 'variable' || isDerivedInput(node))).map(node => node.id)]))
const children = computed(() => nodes.value.filter(node => node.parent === props.layerId && isBusinessLayer(node) && (kindLabel(node) !== '设备' || devices.value.some(device => device.business_node_id === node.id))))
const layerAlarms = computed(() => props.alarms.filter(alarm => alarm.active && alarmInLayer(alarm, scopeNodes.value, deviceIds.value)))
const qualityDevices = computed(() => props.quality.devices as QualityStats[])
const onlineCount = computed(() => devices.value.filter(device => qualityDevices.value.some(item => item.device_id === deviceKey(device) && item.health === 'healthy')).length)
const onlineText = computed(() => `在线 ${onlineCount.value} / ${devices.value.length}`)
const latestObservation = computed(() => shortTime(scopedValues.value.map(value => value.source_time || '').filter(Boolean).sort().at(-1)))
const definition = screenBlockDefinition
function applicable(type: string) { const def = definition(type); return !!def && !!selected.value && (!('kinds' in def) || def.kinds.includes(kindLabel(selected.value) as never)) }
function slotBlocks(slot: string) { return (props.document.slots[slot as keyof ScreenDocument['slots']] || []).map(type => props.document.blocks.find(block => block.type === type)).filter((block): block is ScreenBlock => !!block && !!definition(block.type) && !(props.hero && slot === 'top' && block.type === 'kpi')) }
function nodeName(id: string) { const node = nodes.value.find(node => node.id === id); return node ? nameOf(node) : '不可见对象' }
function resolvePoint(id: string) {
 if (!availablePoints.value.has(id)) return { id: '', label: '未绑定或不在本层范围', value: '—', unit: '', quality: '未绑定', time: '—' }
 const actual = metricForPoint(id, props.snapshot, scopedValues.value)
 return props.runtimeError ? { ...actual, value:'—', quality: props.runtimeError.startsWith('无权') ? '无权限' : '查询不可用' } : actual
}
function history(id: string) { if (id && availablePoints.value.has(id)) emit('history', id) }
const metricIcons = [
 'M4 12a8 8 0 1 1 16 0M12 12l4-4M12 4v2M4 12h2M18 12h2',
 'M13 2L5 13h6l-1 9 8-12h-6z',
 'M4 20V7l8-4 8 4v13M8 20v-6h8v6M8 9h2M14 9h2M8 12h2M14 12h2',
 'M4 7h15v11H4zM19 10h2v5h-2M8 10v5M12 10v5M16 10v5',
 'M6 16v-5a6 6 0 0 1 12 0v5l2 3H4zM10 21h4',
 'M4 18v-4M9 18V9M14 18V5M19 18V2',
]
const dailyStats = ref<DailyStats | null>(null)
const dailyError = ref('')
let dailySerial = 0
let dailyTimer: ReturnType<typeof setInterval> | undefined
async function refreshDaily(clear = false) {
 const serial = ++dailySerial
 const layer = props.layerId
 const generation = projectContext.generation
 if (clear) dailyStats.value = null
 dailyError.value = ''
 if (!layer) return
 try {
  const result = await loadDailyStats(layer)
  if (serial === dailySerial && generation === projectContext.generation && layer === props.layerId && result.layer_id === layer) dailyStats.value = result
 } catch (error) {
  if (serial === dailySerial && generation === projectContext.generation && layer === props.layerId) { dailyStats.value = null; dailyError.value = error instanceof ApiError && error.status === 403 ? '无权限' : '统计读取失败' }
 }
}
watch(() => [props.layerId, projectContext.generation], () => { void refreshDaily(true) }, { immediate: true, flush: 'sync' })
onMounted(() => { dailyTimer = setInterval(() => { void refreshDaily() }, 30000) })
onBeforeUnmount(() => { dailySerial++; clearInterval(dailyTimer) })
const dailyBindings = { today_energy: 'energy', run_duration: 'runtime', communication_success_rate: 'communication', data_freshness: 'freshness' } as const
const automaticFillKeys = ['online_devices', 'active_alarms', 'communication_success_rate', 'data_freshness'] as const
function automaticMetric(key = '') {
 const field = dailyBindings[key as keyof typeof dailyBindings]
 if (field) {
  const value = dailyStats.value?.[field]
  return { id: '', label: automaticMetricLabel(key), value: dailyMetricText(value), unit: value?.unit || '', quality: dailyError.value || dailyMetricDetail(value), time: shortTime(value?.observed_at || dailyStats.value?.as_of) }
 }
 return { id: '', label: automaticMetricLabel(key), value: props.runtimeError ? '—' : key === 'online_devices' ? `${onlineCount.value} / ${devices.value.length}` : key === 'active_alarms' ? String(layerAlarms.value.length) : '—', unit: '', quality: key === 'online_devices' || key === 'active_alarms' ? (props.runtimeError ? '查询不可用' : '当前查询') : '暂无统计', time: '—' }
}
type HeroMetric = { id: string; label: string; value: string; unit: string; quality: string; time: string; target: string; metric: string }
const metrics = computed<HeroMetric[]>(() => {
 const configured = ((props.document.blocks.find(block => block.type === 'kpi')?.binding.metrics || []) as ScreenMetric[]).slice(0, 6).map(metric => {
  const actual = metric.point_id ? resolvePoint(metric.point_id) : automaticMetric(metric.metric)
  return { ...actual, label: metric.label || actual.label, target: metric.target || '', metric: metric.metric || '' }
 })
 // The prototype's hero always carries six cards; pad the layer's configured
 // metrics with the automatic ones that have real sources. Never invent a
 // value: anything unavailable stays "—". The design preview (no hero) keeps
 // exactly the configured metrics.
 if (props.hero) {
  const used = new Set(configured.map(metric => metric.metric).filter(Boolean))
  for (const key of automaticFillKeys) {
   if (configured.length >= 6) break
   if (used.has(key)) continue
   configured.push({ ...automaticMetric(key), target: '', metric: key })
  }
 }
 return configured
})
const heroMetrics = computed(() => props.hero && (props.document.slots.top || []).includes('kpi') ? metrics.value : [])
const visibleSlots = computed(() => ['top', 'main', 'side'].filter(slot => slotBlocks(slot).length))
const pointMetrics = computed(() => devices.value.flatMap(device => devicePointIds(nodes.value, [device]).slice(0, 1)).map(resolvePoint))
const topology = computed(() => [...devices.value.map(device => { const id = devicePointIds(nodes.value, [device])[0]; const metric = id ? resolvePoint(id) : null; return { id:deviceKey(device), label:device.name, relation:`${layerName.value} → 设备绑定`, detail:metric ? `${metric.label} ${metric.value} ${metric.unit} · ${metric.quality}` : '无数据' } }), ...members.value.map(member => ({ id:`member:${member.role}`, label:member.node_id ? nodeName(member.node_id) : '未绑定成员', relation:member.role, detail:'成员引用，不代表能量流向' }))])
const deviceColumnLabels: Record<string, string> = { type:'类型', state:'状态', value:'主要值', communication:'通信', updated:'更新' }
function runFeedback(device: LedgerDevice) {
 const sample = scopedValues.value.find(value => value.device_id === deviceKey(device) && /运行反馈|运行状态/.test(businessName(nodes.value.find(node => node.id === value.point_id))))
 return sample && sample.quality === 'good' && sample.value.bool !== undefined ? sample.value.bool : undefined
}
function deviceColumn(device: LedgerDevice, column: string) {
 if (column === 'type') return device.model || device.profile_id || '—'
 if (column === 'state') {
  if (props.runtimeError) return '—'
  const health = qualityDevices.value.find(item => item.device_id === deviceKey(device))?.health
  return deviceStatus(health, runFeedback(device))
 }
 if (column === 'communication') return props.runtimeError ? '查询不可用' : healthLabel(qualityDevices.value.find(item => item.device_id === deviceKey(device))?.health)
 const metric = resolvePoint(devicePointIds(nodes.value, [device])[0] || '')
 return column === 'updated' ? metric.time : `${metric.value} ${metric.unit}`
}
const recentPointIds = computed(() => {
 const recent = (props.document.blocks.find(block => block.type === 'recent')?.binding.point_ids || []) as string[]
 const kpi = ((props.document.blocks.find(block => block.type === 'kpi')?.binding.metrics || []) as ScreenMetric[]).map(metric => metric.point_id).filter((id): id is string => !!id)
 return [...new Set((recent.length ? recent : kpi))].slice(0, 5)
})
const recentColumns = computed(() => recentPointIds.value.map(id => { const metric = resolvePoint(id); return { id, label: metric.label, unit: metric.unit } }))
const recentRows = ref<RecentTableRow[]>([])
const recentError = ref('')
let recentSerial = 0
let recentTimer: ReturnType<typeof setInterval> | undefined
function openRecentHistory() { emit('history', recentColumns.value.find(column => availablePoints.value.has(column.id))?.id || '') }
function recentTime(time: number) { return shortTime(new Date(time).toISOString()) }
async function refreshRecent() {
 const serial = ++recentSerial
 const block = props.document.blocks.find(item => item.type === 'recent')
 const columns = recentColumns.value
 recentError.value = ''
 if (!block || !columns.length || props.runtimeError) { recentRows.value = []; return }
 const now = new Date(); const start = new Date(now.getTime() - 10 * 60 * 1000)
 const results = await Promise.all(columns.map(async column => {
  if (!availablePoints.value.has(column.id)) return [] as HistoryRecord[]
  try { const result = await loadHistory(column.id, start, now, 'raw'); return result.results[0]?.records || [] }
  catch (error) { if (serial === recentSerial) recentError.value = `最近数据查询不可用：${error instanceof Error ? error.message : String(error)}`; return [] as HistoryRecord[] }
 }))
 if (serial !== recentSerial) return
 recentRows.value = buildRecentTable(columns.map((column, index) => ({ id: column.id, unit: column.unit, records: results[index] })), Math.min(5, Number(block.binding.rows) || 5))
}
watch(() => [props.layerId, recentPointIds.value.join(','), projectContext.generation, props.runtimeError, props.document.blocks.some(block => block.type === 'recent')], () => { void refreshRecent() }, { immediate: true, flush: 'sync' })
onMounted(() => { recentTimer = setInterval(() => { void refreshRecent() }, 10000) })
onBeforeUnmount(() => { recentSerial++; clearInterval(recentTimer) })
const operations = ref<OperationRecord[]>([])
const operationError = ref('')
let operationSerial = 0
watch(() => [props.layerId, devices.value[0] && deviceKey(devices.value[0]), props.document.blocks.some(block => block.type === 'oplog'), props.lastSync], async () => {
 const serial = ++operationSerial; operations.value = []; operationError.value = ''
 if (!devices.value[0] || !props.document.blocks.some(block => block.type === 'oplog')) return
 try { const rows = await loadOperationRecords(devices.value[0]); if (serial === operationSerial) operations.value = rows.slice(0, 5) }
 catch (error) { if (serial === operationSerial) operationError.value = `操作记录查询不可用：${error instanceof Error ? error.message : String(error)}` }
}, { immediate: true })
const trendMetric = computed(() => resolvePoint(props.document.blocks.find(block => block.type === 'trend')?.binding.point_id || ''))
const trendRecords = ref<HistoryRecord[]>([])
const trendStart = ref(new Date())
const trendEnd = ref(new Date())
const trendError = ref('')
let trendSerial = 0
watch(() => [props.layerId, trendMetric.value.id] as const, async ([, id]) => {
 const serial = ++trendSerial; trendRecords.value = []; trendError.value = ''
 if (!id) return
 const end = new Date(); const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
 trendStart.value = start; trendEnd.value = end
 try { const result = await loadHistory(id, start, end, '1h'); if (serial === trendSerial) trendRecords.value = result.results[0]?.records || [] }
 catch (error) { if (serial === trendSerial) trendError.value = `历史查询不可用：${error instanceof Error ? error.message : String(error)}` }
}, { immediate:true })
</script>

<style scoped>
.screen-renderer { display:flex; flex-direction:column; gap:var(--overview-gap); min-width:0; }
.screen-body { display:grid; grid-template-columns:minmax(0,1fr) 280px; gap:var(--overview-gap); min-width:0; }
.screen-body.without-side { grid-template-columns:minmax(0,1fr); }
.provenance, .slot-top { grid-column:1 / -1; }
.hero { padding:var(--hero-pad); }
.hero .crumb { color:var(--muted); font-size:var(--fs-crumb); letter-spacing:.06em; }
.hero .headline { display:flex; align-items:baseline; justify-content:space-between; gap:16px; margin:4px 0 0; }
.hero h1 { margin:0; font-size:var(--fs-hero); font-weight:700; line-height:normal; letter-spacing:-.02em; }
.hero .online { color:var(--muted); font-size:var(--fs-online); text-align:right; white-space:nowrap; }
.provenance { font-size:12px; color:var(--muted); }
.screen-slot { display:grid; grid-template-columns:repeat(var(--screen-columns),minmax(0,1fr)); gap:var(--overview-gap); align-content:start; min-width:0; }
.screen-block[data-block="summary"] :deep(.daily-stats) { grid-template-columns:repeat(2,minmax(0,1fr)); gap:10px 0; }
.screen-block { min-width:0; }
.status { display:flex; justify-content:space-between; gap:8px; flex-wrap:wrap; padding:8px 14px; font-size:11px; color:var(--muted); }
.metrics { display:grid; grid-template-columns:repeat(auto-fit,minmax(95px,1fr)); gap:10px; }
.with-hero .metrics { grid-template-columns:repeat(6,minmax(0,1fr)); gap:0; margin-top:var(--overview-gap); padding:12px 0 10px; border-top:1px solid var(--line); }
.metric { min-width:0; border:var(--metric-border); border-radius:var(--metric-radius); padding:var(--metric-pad); background:var(--metric-bg); box-shadow:var(--metric-shadow); color:var(--metric-ink); }
.with-hero .metric { height:70px; box-sizing:border-box; overflow:hidden; margin:0 var(--metric-gap); }
.with-hero .metric:first-child { margin-left:0; padding-left:var(--metric-first-pad-left); }
.with-hero .metric:last-child { margin-right:0; }
.with-hero .metric .k { white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
.with-hero .metric .k svg { display:var(--metric-icon-display); }
.with-hero .metric-quality { display:none; }
.with-hero .metric .v { margin-top:4px; font-size:var(--metric-value-size); letter-spacing:-.01em; color:var(--metric-value-color); white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
.k, .metric-quality { color:var(--muted); font-size:var(--metric-label-size); overflow-wrap:anywhere; }
.k { display:flex; align-items:center; gap:7px; }
.k svg { flex:none; width:var(--metric-icon-size); height:var(--metric-icon-size); fill:none; stroke:var(--metric-icon-stroke); stroke-width:1.6; stroke-linecap:round; stroke-linejoin:round; }
.v { margin-top:4px; font-size:var(--metric-value-size); font-weight:600; color:var(--metric-value-color); }
.v text { margin-left:3px; color:var(--muted); font-size:var(--metric-label-size); font-weight:400; }
.panel { padding:var(--panel-pad); min-width:0; height:100%; box-sizing:border-box; }
.table-wrap { overflow:auto; }
table { width:100%; font-size:var(--fs-ledger); }
th, td { padding:7px; text-align:left; }
.object-link { display:inline; min-height:0; margin:0; border:0; padding:0; background:transparent; color:var(--ink); font-size:var(--fs-ledger); text-align:left; line-height:1.6; }
.object-link::after { border:0; }
.run, .alarm-mini { padding:8px 0; border-top:1px solid var(--line); font-size:var(--fs-ledger); }
.run { display:flex; justify-content:space-between; gap:8px; }
.screen-card h5 { margin:0 0 10px; }
.preview-run { display:flex; align-items:center; justify-content:space-between; gap:7px; }
.preview-run b, .preview-run small { display:block; }
.preview-run small, .preview-note { color:var(--muted); font-size:var(--fs-label); line-height:1.7; }
.prototype-chart { display:flex; align-items:flex-end; gap:5px; height:96px; padding:6px 0; }
.prototype-chart i { flex:1; border-radius:4px 4px 0 0; background:linear-gradient(0deg,var(--chart-from),var(--chart-to)); }
.preview-actions { margin-top:10px; }
.preview-actions uni-button { min-height:25px; padding:2px 7px; font-size:11px; }
.meta, .empty { color:var(--muted); font-size:11px; line-height:1.7; }
.tabs { display:flex; gap:5px; margin-bottom:10px; }
.tabs button { min-height:0; padding:4px 8px; font-size:11px; }
.tabs .active { color:var(--accent); }
@media(max-width:1150px) { .screen-body { grid-template-columns:minmax(0,1fr); } .slot-top { grid-column:auto; } }
@media(max-width:760px) { .screen-slot { grid-template-columns:1fr; } .screen-block, .screen-peripheral { grid-column:1 !important; } .hero { padding:14px; } .hero h1 { font-size:29px; } .with-hero .metrics { grid-template-columns:repeat(3,minmax(0,1fr)); padding-top:10px; } .with-hero .metric { padding:2px 8px; } .with-hero .metric:nth-child(4) { border-left:0; padding-left:0; } .with-hero .metric .v { font-size:20px; } }
</style>
