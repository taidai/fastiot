<template>
  <view class="ui-step">
    <view class="step-head"><b>第 5 步 · 配界面</b><span>查看这一层的块、槽位与真实对象预览。</span></view>
    <view class="cfg-bar">
      <span class="chip">{{ selectedName }} · {{ selectedKind }}</span>
      <span class="chip" :class="screen ? 'ok' : 'warn'">{{ screen ? '简单画面已读取' : '使用默认结构预览' }}</span>
      <span class="src readonly-note">只读：后端没有块编辑、绑定或保存接口</span>
    </view>

    <view class="cfg-grid">
      <view class="cfg-list glass">
        <template v-for="slot in slots" :key="slot.key">
          <h5 class="cfg-slot">{{ slot.label }}</h5>
          <view v-for="block in blocks.filter(item => item.slot === slot.key)" :key="block.id" class="cfg-row">
            <b>{{ block.label }}</b><span class="src">{{ block.binding }}</span><span class="cfg-lock">只读</span>
          </view>
        </template>
        <h5>块目录</h5>
        <view class="catalog"><span v-for="block in catalogBlocks" :key="block.id">{{ block.label }}</span></view>
        <p class="note">块目录封闭；行业包只改默认块与拓扑。当前后端不提供增删、绑定或保存。</p>
      </view>

      <view class="cfg-preview">
        <view class="preview-caption"><span>客户在这一层会看到：</span><span>结构预览 · 非已保存块配置</span></view>
        <view class="cfg-frame customer-preview">
          <view class="cust-head">
            <view><span class="kicker">{{ projectName }} / {{ selectedKind }}</span><h1>{{ selectedName }}</h1></view>
            <view class="cust-head-right"><span class="chip ok">{{ onlineChip }}</span><view class="cust-tools"><span>历史数据</span><span>刷新</span><span>全屏</span></view></view>
          </view>
          <view class="cust-meta">
            <span>更新于 <b>{{ shortTime(lastSync) }}</b></span><span>通信 <b>{{ communicationText }}</b></span><span>数据质量 <b>{{ qualityPercent }}</b></span><span>采样 <b>—</b></span><span>设备 <b>{{ onlineText }}</b> 在线</span><span class="spacer"></span><span>只读布局预览</span>
          </view>
          <view v-if="layerAlarms.length" class="alarm-strip"><b>{{ layerAlarms.length }} 条待处理</b><span>{{ layerAlarms[0].title }}</span><span class="src">{{ shortTime(layerAlarms[0].updated_at) }}</span><span class="spacer"></span><span>当前实例告警</span></view>
          <view class="kpi-row">
            <view v-for="metric in metrics" :key="metric.label" class="kpi"><span>{{ metric.label }}</span><b>{{ metric.value }}<em v-if="metric.unit">{{ metric.unit }}</em></b></view>
          </view>

          <view class="canvas-grid">
            <view class="canvas-main">
              <view class="screen-card">
                <h5>工艺总览</h5>
                <view class="flow">
                  <template v-for="(slot, index) in flowSlots" :key="slot.row?.node.id || `empty-${index}`">
                    <view class="flow-node" :class="{ pcc: index === 2 }">
                      <span>{{ slot.row?.name || '未映射对象' }}</span>
                      <b>{{ slot.metric?.value || '—' }}<em v-if="slot.metric?.unit">{{ slot.metric.unit }}</em></b>
                      <small>{{ flowDetail(slot.metric) }}</small>
                    </view><i v-if="index < flowSlots.length - 1">→</i>
                  </template>
                </view>
              </view>
              <view class="screen-card">
                <h5>设备清单 <span>{{ devices.length }} 个真实对象</span></h5>
                <div v-if="devices.length" class="table-wrap"><table class="mini-table"><thead><tr><th>设备</th><th>类型</th><th>状态</th><th>主要值</th><th>通信</th><th>更新</th></tr></thead><tbody>
                  <tr v-for="row in devices" :key="row.node.id"><td>{{ row.name }}</td><td>{{ row.node.type }}</td><td>—</td><td>{{ deviceValue(row.node) }}</td><td>{{ deviceHealth(row.node) }}</td><td>{{ deviceTime(row.node) }}</td></tr>
                </tbody></table></div>
                <view v-else class="empty">这一层还没有设备对象。</view>
              </view>
              <view class="screen-card">
                <h5>最近数据 <span>{{ recentValues.length }} 条当前值</span></h5>
                <div v-if="recentValues.length" class="table-wrap"><table class="mini-table"><thead><tr><th>点位</th><th>当前值</th><th>质量</th><th>观测时间</th></tr></thead><tbody>
                  <tr v-for="sample in recentValues" :key="sample.point_id"><td>{{ pointName(sample.point_id) }}</td><td>{{ sampleText(sample) }}</td><td>{{ qualityText(sample.quality) }}</td><td>{{ sampleTime(sample) }}</td></tr>
                </tbody></table></div>
                <view v-else class="empty">当前画面没有可显示的点值，保留结构并显示 —。</view>
              </view>
            </view>

            <view class="canvas-side">
              <view class="screen-card"><h5>告警</h5>
                <view v-for="alarm in layerAlarms.slice(0, 3)" :key="alarm.id" class="alarm-mini"><span><b>{{ alarm.title }}</b><small>{{ alarm.severity }} · {{ shortTime(alarm.updated_at) }}</small></span><span class="chip warn">待处理</span></view>
                <view v-if="!layerAlarms.length" class="empty">这一层没有待处理告警。</view>
              </view>
              <view class="screen-card"><h5>今日执行</h5>
                <view class="alarm-mini"><span><b>执行记录</b><small>实际效果以设备回读为准</small></span><span class="chip">—</span></view>
                <p class="note">后端未提供“今日执行”查询 API，不推算触发、完成或跳过记录。</p>
              </view>
              <view class="screen-card"><h5>趋势 · {{ trendMetric?.label || '—' }}</h5><view class="trend-empty">—</view><p class="note">尚未读取历史序列，不生成演示曲线。</p></view>
              <view class="screen-card"><h5>运行摘要</h5><view class="stat-grid">
                <view class="stat"><span>今日电量</span><b>—</b></view>
                <view class="stat"><span>运行时长</span><b>—</b></view>
                <view class="stat"><span>通信成功率</span><b>{{ successRate }}</b></view>
                <view class="stat"><span>数据新鲜度</span><b>{{ latestTime }}</b></view>
              </view></view>
            </view>
          </view>
        </view>
      </view>
    </view>
  </view>
</template>

<script setup lang="ts">
import { computed } from "vue"
import { nameOf, point, valueText } from "../../types/api"
import type { EmsAlarm, Node, ScreenConfig, Snapshot, StateValue } from "../../types/api"
import { healthLabel, metricForPoint, shortTime, summarizeSuccessRate } from "../../pages/overview/overview"
import type { QualityStats } from "../../pages/overview/overview"
import { flattenLedger, kindLabel } from "./design"

const props = withDefaults(defineProps<{ snapshot: Snapshot | null; values: StateValue[]; alarms: EmsAlarm[]; quality: { channels: unknown[]; devices: unknown[] }; screen: ScreenConfig | null; selectedNodeId?: string; lastSync?: string }>(), { selectedNodeId: "", lastSync: "" })
const slots = [{ key: "top", label: "顶部" }, { key: "main", label: "主区" }, { key: "side", label: "右栏" }]
const nodes = computed(() => props.snapshot?.nodes || [])
const selected = computed(() => nodes.value.find(node => node.id === props.selectedNodeId) || nodes.value.find(node => node.id === props.snapshot?.roots[0]) || null)
const selectedName = computed(() => selected.value ? nameOf(selected.value) : "未选择对象")
const selectedKind = computed(() => selected.value ? kindLabel(selected.value) : "—")
const projectName = computed(() => {
  const root = nodes.value.find(node => node.id === props.snapshot?.roots[0])
  return root ? nameOf(root) : "未选择项目"
})
const layerIds = computed(() => {
  const ids = new Set<string>()
  const walk = (id: string) => {
    if (ids.has(id)) return
    ids.add(id)
    nodes.value.filter(node => node.parent === id).forEach(node => walk(node.id))
  }
  if (selected.value) walk(selected.value.id)
  return ids
})
const allRows = computed(() => flattenLedger(props.snapshot))
const layerRows = computed(() => allRows.value.filter(row => layerIds.value.has(row.node.id)))
const semanticNodes = computed(() => layerRows.value.filter(row => row.node.id !== selected.value?.id).slice(0, 3))
const devices = computed(() => layerRows.value.filter(row => row.kind === "设备"))
const layerValues = computed(() => props.values.filter(sample => layerIds.value.has(sample.point_id) || layerIds.value.has(sample.device_id)))
const recentValues = computed(() => {
  const configured = props.screen?.point_ids.length ? props.values.filter(sample => props.screen!.point_ids.includes(sample.point_id)) : []
  return (configured.length ? configured : layerValues.value).slice(0, 5)
})
const layerAlarms = computed(() => props.alarms.filter(alarm => alarm.active && (selected.value?.id === props.snapshot?.roots[0] || layerIds.value.has(alarm.device_id || "") || layerIds.value.has(alarm.point_id || ""))))
const pointMetrics = computed(() => {
  const configured = props.screen?.point_ids.filter(id => layerIds.value.has(id)) || []
  const ids = configured.length ? configured : [...new Set(layerValues.value.map(sample => sample.point_id))]
  return ids.slice(0, 6).map(id => metricForPoint(id, props.snapshot, layerValues.value))
})
const powerMetric = computed(() => pointMetrics.value.find(metric => /总功率|有功功率|功率|power/i.test(metric.label)))
const trendMetric = computed(() => pointMetrics.value.find(metric => /压力|趋势/i.test(metric.label)) || pointMetrics.value[0])
const qualityDevices = computed(() => props.quality.devices as QualityStats[])
const scopedQualityDevices = computed(() => qualityDevices.value.filter(item => selected.value?.id === props.snapshot?.roots[0] || !!item.device_id && layerIds.value.has(item.device_id)))
const onlineText = computed(() => scopedQualityDevices.value.length ? `${scopedQualityDevices.value.filter(item => item.health === "healthy").length} / ${scopedQualityDevices.value.length}` : "—")
const onlineChip = computed(() => onlineText.value === "—" ? "在线 —" : `${onlineText.value} 在线`)
const successRate = computed(() => summarizeSuccessRate(scopedQualityDevices.value))
const communicationText = computed(() => {
  if (!scopedQualityDevices.value.length) return "—"
  const unhealthy = scopedQualityDevices.value.find(item => item.health !== "healthy")
  return unhealthy ? healthLabel(unhealthy.health) : "正常"
})
const qualityPercent = computed(() => {
  if (!layerValues.value.length) return "—"
  const good = layerValues.value.filter(sample => sample.quality === "good" || sample.quality === "有效").length
  return `${(good / layerValues.value.length * 100).toFixed(1)}%`
})
const latestTime = computed(() => pointMetrics.value.map(metric => metric.time).find(time => time && time !== "—") || "—")
const metrics = computed(() => [
  { label: "总功率", value: powerMetric.value?.value || "—", unit: powerMetric.value?.unit || "" },
  { label: "今日电量", value: "—", unit: "kWh" },
  { label: "在线设备", value: onlineText.value, unit: "" },
  { label: "活动告警", value: layerAlarms.value.length, unit: "条" },
  { label: "今日供水", value: "—", unit: "m³" },
  { label: "通信成功率", value: successRate.value, unit: "" },
])
const flowSlots = computed(() => Array.from({ length: 3 }, (_, index) => ({ row: semanticNodes.value[index], metric: pointMetrics.value[index] })))
const catalogBlocks = computed(() => selectedKind.value === "设备" ? [{ id: "deviceData", label: "设备数据" }, { id: "oplog", label: "操作记录" }] : selectedKind.value === "单元" ? [{ id: "members", label: "成员" }] : [{ id: "children", label: "下级对象" }])
const blocks = computed(() => [
  { id: "status", label: "状态条", slot: "top", binding: "自动" },
  { id: "kpi", label: "指标组", slot: "top", binding: "6 个指标" },
  { id: "flow", label: "工艺图 / 能流", slot: "main", binding: "含次要信息" },
  { id: "devices", label: "设备清单", slot: "main", binding: "5 列" },
  { id: "recent", label: "最近数据", slot: "main", binding: "3 列 · 5 行" },
  { id: "alarm", label: "告警", slot: "side", binding: "自动" },
  { id: "today", label: "今日执行", slot: "side", binding: "自动" },
  { id: "trend", label: "趋势", slot: "side", binding: trendMetric.value?.label || "—" },
  { id: "summary", label: "运行摘要", slot: "side", binding: "自动" },
])

function flowDetail(metric?: ReturnType<typeof metricForPoint>) { return metric ? `${metric.quality} · ${metric.time}` : "未映射实时点位" }
function pointName(id: string) { const node = nodes.value.find(item => item.id === id); return node ? nameOf(node) : id }
function sampleText(sample: StateValue) { const node = nodes.value.find(item => item.id === sample.point_id); const unit = node ? point(node, "units")?.text : ""; return `${valueText(sample)}${unit ? ` ${unit}` : ""}` }
function qualityText(quality: string) { return ({ good: "有效", stale: "过期", offline: "离线", invalid: "无效", uninitialized: "无值" } as Record<string, string>)[quality] || quality }
function sampleTime(sample: StateValue) { const time = sample.source_time || sample.received_time; return time ? shortTime(time) : "—" }
function deviceSample(node: Node) {
  const childIds = new Set(nodes.value.filter(item => item.parent === node.id).map(item => item.id))
  return props.values.find(sample => sample.device_id === node.id || childIds.has(sample.point_id))
}
function deviceValue(node: Node) { const sample = deviceSample(node); return sample ? sampleText(sample) : "—" }
function deviceHealth(node: Node) { return healthLabel(qualityDevices.value.find(item => item.device_id === node.id)?.health) }
function deviceTime(node: Node) { const sample = deviceSample(node); return sample ? sampleTime(sample) : "—" }
</script>

<style scoped>
.ui-step { min-width: 0; }
.step-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; margin-bottom: 12px; }
.step-head b { font-size: 16px; }
.step-head span, .src, .note { color: var(--faint); font-size: 12px; }
.cfg-bar { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 12px; }
.cfg-bar .chip { min-height: 33px; height: 33px; }
.readonly-note { margin-left: auto; }
.cfg-grid { display: grid; grid-template-columns: 290px minmax(0, 1fr); gap: 12px; align-items: start; }
.cfg-list { min-width: 0; min-height: 519px; padding: 10px 12px; border-radius: 12px; }
.cfg-list h5 { margin: 10px 4px 6px; color: var(--faint); font-size: 11px; letter-spacing: .08em; }
.cfg-list h5:first-child { margin-top: 2px; }
.cfg-row { display: flex; align-items: center; gap: 5px; min-height: 34px; border-bottom: 1px dashed rgba(72,111,139,.12); font-size: 12px; }
.cfg-row b { flex: none; font-weight: 600; }
.cfg-row .src { flex: 1; min-width: 0; font-size: 10px; text-align: right; }
.cfg-lock { flex: none; border: 1px solid var(--glass-border); border-radius: 6px; padding: 2px 5px; color: var(--faint); font-size: 9px; }
.catalog { display: flex; flex-wrap: wrap; gap: 4px; }
.catalog span { border: 1px solid var(--glass-border); border-radius: 999px; padding: 3px 7px; background: rgba(255,255,255,.4); color: var(--muted); font-size: 10px; }
.note { line-height: 1.7; }
.cfg-preview { min-width: 0; }
.preview-caption { display: flex; justify-content: space-between; gap: 10px; height: 21px; margin-bottom: 6px; color: var(--faint); font-size: 12px; line-height: 21px; }
.cfg-frame { max-height: 720px; overflow: auto; border: 1px solid rgba(255,255,255,.75); border-radius: 12px; padding: 10px; background: linear-gradient(145deg,rgba(231,241,247,.7),rgba(215,231,239,.65)); }
.cust-head { display: flex; align-items: flex-end; justify-content: space-between; gap: 16px; margin-bottom: 10px; }
.kicker { color: var(--faint); font-size: 11px; }
.cust-head h1 { margin: 4px 0 0; font-size: 20px; }
.cust-head-right { display: flex; align-items: flex-end; flex-direction: column; gap: 8px; text-align: right; }
.cust-head .chip { min-height: 23px; padding: 2px 8px; font-size: 11px; }
.cust-tools { display: flex; gap: 6px; }
.cust-tools span { border: 1px solid var(--glass-border); border-radius: 8px; padding: 5px 10px; background: rgba(255,255,255,.55); color: var(--muted); font-size: 12px; line-height: 14px; }
.cust-meta { display: flex; gap: 10px; flex-wrap: wrap; margin-bottom: 10px; border: 1px solid rgba(255,255,255,.7); border-radius: 10px; padding: 7px 12px; background: rgba(255,255,255,.35); color: var(--faint); font-size: 10.5px; }
.spacer { flex: 1; }
.alarm-strip { display: flex; align-items: center; gap: 10px; margin-bottom: 10px; border: 1px solid rgba(238,221,185,.75); border-radius: 10px; padding: 7px 12px; background: rgba(255,244,228,.68); color: var(--amber); font-size: 12px; }
.kpi-row { display: grid; grid-template-columns: repeat(6,minmax(0,1fr)); gap: 6px; margin-bottom: 12px; }
.kpi { min-width: 0; height: 61px; border: 1px solid rgba(255,255,255,.74); border-radius: 12px; padding: 7px 8px; background: rgba(255,255,255,.44); box-shadow: 0 8px 20px rgba(45,83,110,.06); }
.kpi span { display: block; color: var(--faint); font-size: 9.5px; }
.kpi b { display: block; margin-top: 4px; font-size: 15px; font-variant-numeric: tabular-nums; }
.kpi em, .flow-node em { margin-left: 3px; color: var(--faint); font-size: 9px; font-style: normal; font-weight: 400; }
.canvas-grid { display: grid; grid-template-columns: minmax(0,1.5fr) minmax(180px,1fr); gap: 8px; align-items: start; }
.canvas-main, .canvas-side { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.screen-card { min-width: 0; border: 1px solid rgba(255,255,255,.74); border-radius: 12px; padding: 11px; background: rgba(255,255,255,.44); box-shadow: 0 8px 22px rgba(45,83,110,.07); }
.screen-card h5 { display: flex; justify-content: space-between; gap: 8px; margin: 0 0 10px; color: var(--faint); font-size: 12px; letter-spacing: .05em; }
.screen-card h5 span { font-size: 9px; font-weight: 400; letter-spacing: 0; }
.flow { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; padding: 8px 0; }
.flow i { color: #b9c8d4; font-size: 18px; font-style: normal; }
.flow-node { display: flex; flex: 1; flex-direction: column; gap: 6px; min-width: 120px; border: 1px solid rgba(255,255,255,.72); border-radius: 12px; padding: 14px; background: rgba(255,255,255,.42); }
.flow-node.pcc { border-color: rgba(12,125,132,.35); background: rgba(241,250,249,.6); }
.flow-node span { display: block; color: var(--faint); font-size: 11.5px; }
.flow-node b { display: block; margin: 0; font-size: 18px; }
.flow-node small { display: block; color: var(--faint); font-size: 10.5px; line-height: 1.4; }
.mini-table { min-width: 0; font-size: 10.5px; }
.mini-table th, .mini-table td { padding: 6px; }
.alarm-mini { display: flex; align-items: center; justify-content: space-between; gap: 7px; border-bottom: 1px dashed rgba(72,111,139,.12); padding: 7px 0; }
.alarm-mini b, .alarm-mini small { display: block; }
.alarm-mini b { font-size: 12.5px; }
.alarm-mini small { margin-top: 3px; color: var(--faint); font-size: 10.5px; }
.trend-empty { display: grid; place-items: center; height: 54px; border-radius: 8px; background: rgba(255,255,255,.28); color: var(--faint); }
.stat-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
.stat { border: 1px solid rgba(255,255,255,.72); border-radius: 10px; padding: 8px 10px; background: rgba(255,255,255,.42); }
.stat span { display: block; color: var(--faint); font-size: 10.5px; }
.stat b { display: block; margin-top: 3px; font-size: 15px; }
.empty { padding: 10px 4px; font-size: 11px; }
@media (max-width: 1180px) {
  .cfg-grid { grid-template-columns: 240px minmax(0,1fr); }
  .kpi-row { grid-template-columns: repeat(3,minmax(0,1fr)); }
  .canvas-grid { grid-template-columns: 1fr; }
  .canvas-side { display: grid; grid-template-columns: 1fr 1fr; }
  .readonly-note { flex-basis: 100%; margin-left: 0; }
}
@media (max-width: 760px) {
  .cfg-grid { grid-template-columns: 1fr; }
  .preview-caption, .cust-head { align-items: flex-start; flex-direction: column; height: auto; }
  .cust-head-right { align-items: flex-start; text-align: left; }
  .canvas-side { grid-template-columns: 1fr; }
}
</style>
