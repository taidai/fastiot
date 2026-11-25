<template>
  <view class="screen-renderer" :data-screen-version="document.version" :data-screen-layer="layerId">
    <view v-if="document.inherited_from" class="provenance">继承自 {{ nodeName(document.inherited_from) }} · v{{ document.version }}</view>
    <view v-for="slot in ['top', 'main', 'side']" :key="slot" class="screen-slot" :class="`slot-${slot}`" :data-slot="slot" :style="{ '--screen-columns': document.columns }">
      <view v-for="block in slotBlocks(slot)" :key="block.type" class="screen-block" :data-block="block.type" :style="{ gridColumn: `span ${Math.min(block.span, document.columns)}` }">
        <view v-if="!applicable(block.type)" class="panel glass empty">{{ definition(block.type)?.label }} · 本层不适用，请重新配置</view>
        <view v-else-if="block.type === 'status'" class="status glass"><span>{{ runtimeError || onlineText }}</span><span>观测于 {{ latestObservation }} · 查询于 {{ shortTime(lastSync) }}</span></view>
        <view v-else-if="block.type === 'kpi'" class="metrics">
          <view v-for="(metric, index) in metrics" :key="index" class="metric" :role="metric.id ? 'button' : undefined" :tabindex="metric.id ? 0 : -1" @click="history(metric.id)" @keydown.enter="history(metric.id)" @keydown.space.prevent="history(metric.id)">
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
          <view class="card-title">最近数据</view><view class="table-wrap"><table><thead><tr><th>点位</th><th v-for="column in block.binding.columns" :key="column">{{ recentColumnLabels[column] }}</th></tr></thead><tbody><tr v-for="id in block.binding.point_ids.slice(0, block.binding.rows)" :key="id"><td><button class="object-link" @click="history(availablePoints.has(id) ? id : '')">{{ resolvePoint(id).label }}</button></td><td v-for="column in block.binding.columns" :key="column">{{ recentColumn(id, column) }}</td></tr><tr v-if="!block.binding.point_ids.length"><td :colspan="block.binding.columns.length + 1" class="empty">未绑定点位 · —</td></tr></tbody></table></view>
        </section>
        <section v-else-if="block.type === 'alarm'" class="panel glass">
          <slot name="alarm" :layer-id="layerId"><view class="card-title">本层告警 <text>{{ layerAlarms.length }}</text></view><view v-for="alarm in layerAlarms.slice(0, 3)" :key="alarm.id" class="alarm-mini"><b>{{ alarm.title }}</b><view class="meta">{{ alarm.severity }} · {{ shortTime(alarm.updated_at) }}</view></view><view v-if="!layerAlarms.length" class="empty">没有待处理告警</view></slot>
        </section>
        <section v-else-if="block.type === 'today'" class="panel glass"><slot name="today" :layer-id="layerId"><view class="card-title">今日执行</view><view class="empty">暂无可用的执行记录 · 实际效果以设备回读为准</view></slot></section>
        <section v-else-if="block.type === 'summary'" class="panel glass"><slot name="summary" :layer-id="layerId"><view class="card-title">运行摘要</view><view class="empty">暂无可用的统计数据</view></slot></section>
        <section v-else-if="block.type === 'oplog'" class="panel glass"><view class="card-title">操作记录</view><view v-if="operationError" class="error">{{ operationError }}</view><view v-for="record in operations" :key="record.request_id" class="run"><span>{{ shortTime(record.updated_at || record.started_at) }} · {{ record.command_id }}</span><span>{{ commandStateText(record.state) }}</span></view><view v-if="!operationError && !operations.length" class="empty">暂无操作记录</view></section>
        <section v-else-if="block.type === 'trend'" class="panel glass"><view class="card-title">趋势 · {{ trendMetric.label }}</view><OverviewTrend :records="trendRecords" :start="trendStart" :end="trendEnd" :unit="trendMetric.unit" :label="trendMetric.label" @select="history(trendMetric.id)" /><view v-if="trendError" class="error">{{ trendError }}</view><view v-else-if="!trendMetric.id" class="empty">未绑定或不在本层范围 · —</view></section>
      </view>
      <view v-if="slot === 'side' && $slots['after-side']" class="screen-peripheral" :style="{ gridColumn: `span ${document.columns}` }"><slot name="after-side" /></view>
    </view>
  </view>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { EmsAlarm, Snapshot, StateValue, HistoryRecord } from '../../types/api'
import { nameOf } from '../../types/api'
import type { LedgerDevice, QualityStats } from '../../pages/overview/overview'
import { alarmInLayer, boundDevicesForLayer, businessName, commandStateText, descendantNodeIds, displaySample, deviceKey, devicePointIds, healthLabel, isBusinessLayer, metricForPoint, shortTime, valueInLayer } from '../../pages/overview/overview'
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

const props = withDefaults(defineProps<{ document: ScreenDocument; snapshot: Snapshot | null; values: StateValue[]; quality: { devices: unknown[]; channels: unknown[] }; alarms: EmsAlarm[]; ledger: LedgerDevice[]; layerId: string; deviceId?: string; lastSync?: string; runtimeError?: string }>(), { lastSync: '', runtimeError: '' })
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
const availablePoints = computed(() => new Set([...devicePointIds(nodes.value, devices.value), ...nodes.value.filter(node => scopeNodes.value.has(node.id) && ['variable', 'calculationPoint'].includes(node.type)).map(node => node.id)]))
const children = computed(() => nodes.value.filter(node => node.parent === props.layerId && isBusinessLayer(node) && (kindLabel(node) !== '设备' || devices.value.some(device => device.business_node_id === node.id))))
const layerAlarms = computed(() => props.alarms.filter(alarm => alarm.active && alarmInLayer(alarm, scopeNodes.value, deviceIds.value)))
const qualityDevices = computed(() => props.quality.devices as QualityStats[])
const onlineText = computed(() => `在线 ${devices.value.filter(device => qualityDevices.value.some(item => item.device_id === deviceKey(device) && item.health === 'healthy')).length} / ${devices.value.length}`)
const latestObservation = computed(() => shortTime(scopedValues.value.map(value => value.source_time || '').filter(Boolean).sort().at(-1)))
const definition = screenBlockDefinition
function applicable(type: string) { const def = definition(type); return !!def && !!selected.value && (!('kinds' in def) || def.kinds.includes(kindLabel(selected.value) as never)) }
function slotBlocks(slot: string) { return (props.document.slots[slot as keyof ScreenDocument['slots']] || []).map(type => props.document.blocks.find(block => block.type === type)).filter((block): block is ScreenBlock => !!block && !!definition(block.type)) }
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
const metrics = computed(() => ((props.document.blocks.find(block => block.type === 'kpi')?.binding.metrics || []) as ScreenMetric[]).map(metric => {
 const actual = metric.point_id ? resolvePoint(metric.point_id) : { id:'', label:automaticMetricLabel(metric.metric), value: props.runtimeError ? '—' : metric.metric === 'online_devices' ? onlineText.value : metric.metric === 'active_alarms' ? String(layerAlarms.value.length) : '—', unit:'', quality: metric.metric === 'online_devices' || metric.metric === 'active_alarms' ? (props.runtimeError ? '查询不可用' : '当前查询') : '暂无统计', time:'—' }
 return { ...actual, label: metric.label || actual.label, target:metric.target || '' }
}))
const pointMetrics = computed(() => devices.value.flatMap(device => devicePointIds(nodes.value, [device]).slice(0, 1)).map(resolvePoint))
const topology = computed(() => [...devices.value.map(device => { const id = devicePointIds(nodes.value, [device])[0]; const metric = id ? resolvePoint(id) : null; return { id:deviceKey(device), label:device.name, relation:`${layerName.value} → 设备绑定`, detail:metric ? `${metric.label} ${metric.value} ${metric.unit} · ${metric.quality}` : '无数据' } }), ...members.value.map(member => ({ id:`member:${member.role}`, label:member.node_id ? nodeName(member.node_id) : '未绑定成员', relation:member.role, detail:'成员引用，不代表能量流向' }))])
const deviceColumnLabels: Record<string, string> = { type:'类型', state:'运行 / 维护', value:'主要值', communication:'通信', updated:'更新' }
const recentColumnLabels: Record<string, string> = { value:'当前值', quality:'质量', time:'观测时间' }
function deviceColumn(device: LedgerDevice, column: string) {
 if (column === 'type') return device.model || device.profile_id || '—'
 if (column === 'state') {
  const sample = scopedValues.value.find(value => value.device_id === deviceKey(device) && /运行反馈|运行状态/.test(businessName(nodes.value.find(node => node.id === value.point_id))))
  return `${!props.runtimeError && sample?.quality === 'good' ? displaySample(sample, businessName(nodes.value.find(node => node.id === sample.point_id))) : '未知'} / 未知`
 }
 if (column === 'communication') return props.runtimeError ? '查询不可用' : healthLabel(qualityDevices.value.find(item => item.device_id === deviceKey(device))?.health)
 const metric = resolvePoint(devicePointIds(nodes.value, [device])[0] || '')
 return column === 'updated' ? metric.time : `${metric.value} ${metric.unit}`
}
function recentColumn(id: string, column: string) { const metric = resolvePoint(id); return column === 'value' ? `${metric.value} ${metric.unit}` : column === 'quality' ? metric.quality : metric.time }
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
.screen-renderer { display:grid; grid-template-columns:minmax(0,1fr) 280px; gap:14px; min-width:0; }
.provenance, .slot-top { grid-column:1 / -1; }
.provenance { font-size:12px; color:var(--muted); }
.screen-slot { display:grid; grid-template-columns:repeat(var(--screen-columns),minmax(0,1fr)); gap:14px; align-content:start; min-width:0; }
.screen-block { min-width:0; }
.status { display:flex; justify-content:space-between; gap:8px; flex-wrap:wrap; padding:8px 14px; font-size:11px; color:var(--muted); }
.metrics { display:grid; grid-template-columns:repeat(auto-fit,minmax(95px,1fr)); gap:10px; }
.metric { min-width:0; border:1px solid rgba(255,255,255,.9); border-radius:14px; padding:10px 12px 7px; background:rgba(255,255,255,.58); box-shadow:0 8px 24px rgba(54,92,120,.07),inset 0 0 12px rgba(255,255,255,.3); color:var(--ink); }
.metric[role=button] { cursor:pointer; }
.k, .metric-quality { color:var(--muted); font-size:10.5px; overflow-wrap:anywhere; }
.k { display:flex; align-items:center; gap:7px; }
.k svg { flex:none; width:16px; height:16px; fill:none; stroke:#3b8aa8; stroke-width:1.6; stroke-linecap:round; stroke-linejoin:round; }
.v { margin-top:4px; font-size:24px; font-weight:600; }
.v text { margin-left:3px; color:var(--muted); font-size:10px; font-weight:400; }
.panel { padding:14px 16px; min-width:0; height:100%; box-sizing:border-box; }
.table-wrap { overflow:auto; }
table { width:100%; font-size:12px; }
th, td { padding:7px; text-align:left; }
.object-link { display:inline; min-height:0; margin:0; border:0; padding:0; background:transparent; color:var(--ink); font-size:12px; text-align:left; line-height:1.6; }
.object-link::after { border:0; }
.run, .alarm-mini { padding:8px 0; border-top:1px solid var(--line); font-size:12px; }
.run { display:flex; justify-content:space-between; gap:8px; }
.meta, .empty { color:var(--muted); font-size:11px; line-height:1.7; }
.tabs { display:flex; gap:5px; margin-bottom:10px; }
.tabs button { min-height:0; padding:4px 8px; font-size:11px; }
.tabs .active { color:var(--accent); }
@media(max-width:1150px) { .screen-renderer { grid-template-columns:minmax(0,1fr); } .slot-top { grid-column:auto; } }
@media(max-width:760px) { .screen-slot { grid-template-columns:1fr; } .screen-block, .screen-peripheral { grid-column:1 !important; } }
</style>
