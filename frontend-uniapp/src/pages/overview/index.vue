<template>
  <view class="page" :style="glassStyle">
    <TopBar active="overview" :project="projectName" :meta="syncMeta" :pending="unackedAlarms.length" :alpha="alpha"
      @navigate="navigate" @bell="alarmOpen = true" @alpha="setAlpha" />

    <view class="layout" :class="{ 'tree-collapsed': treeCollapsed }">
      <LedgerTree :selected="selectedKey" :devices="ledger" :alarms="session.alarms" @select="selectLayer" @collapse="treeCollapsed = $event" />
      <main class="stage">
        <view class="hero glass">
          <view class="crumb">{{ pathText }}</view>
          <view class="headline"><h1>{{ layerName }}</h1><text class="online">{{ onlineText }}</text></view>
          <view class="metrics">
            <button v-for="(_, index) in 6" :key="metrics[index]?.id || `empty-${index}`" class="metric" :class="{ vacant: !metrics[index] }"
              :disabled="!metrics[index]" :aria-label="metrics[index] ? `查看${metrics[index].label}历史` : `未配置指标 ${index + 1}`"
              :title="metrics[index] ? `${metrics[index].quality} · ${metrics[index].time}` : '未配置指标'" @click="metrics[index] && openHistory(metrics[index].id)">
              <view class="k"><svg viewBox="0 0 24 24" aria-hidden="true"><path :d="metricIcons[index]" /></svg>{{ metrics[index]?.label || '未配置指标' }}</view>
              <view class="v">{{ metrics[index]?.value || '—' }}<text v-if="metrics[index]?.unit">{{ metrics[index].unit }}</text></view>
            </button>
          </view>
        </view>

        <view class="primary-region">
        <FlowScene v-if="!isDevice" :layer-name="layerName" :metrics="flowMetrics" />

        <view class="lower">
          <section class="panel glass">
            <view class="card-title">{{ isDevice ? '设备状态' : '设备清单' }}<text v-if="!isDevice">{{ scopedDevices.length }}</text></view>
            <view v-if="ledgerError" class="error">{{ ledgerError }}</view>
            <view class="table-wrap">
              <table><thead><tr><th>设备</th><th>通信</th><th>运行</th><th>主要值</th><th>质量</th><th>更新</th></tr></thead>
                <tbody v-if="deviceRows.length"><tr v-for="row in deviceRows" :key="row.id">
                  <td><button class="device-link" @click="selectDevice(row.id)">{{ row.name }}</button></td>
                  <td :class="{ warn: row.communication !== '在线' }">{{ row.communication }}</td>
                  <td>{{ row.runState }}</td><td class="num">{{ row.value }}</td><td>{{ row.quality }}</td><td class="num">{{ row.time }}</td>
                </tr></tbody>
                <tbody v-else><tr class="placeholder-row"><td>—</td><td>—</td><td>—</td><td>—</td><td>—</td><td>—</td></tr></tbody>
              </table>
            </view>
            <view v-if="selectedDevice" class="device-facts">
              <span>型号 {{ selectedDevice.model || '—' }}</span><span>Profile {{ selectedDevice.profile_id || '—' }}</span>
              <span>验证 {{ selectedDevice.verified ? `已验证 · ${selectedDevice.verifier || '未记录人员'}` : '未验证' }}</span>
            </view>
          </section>
          <section class="panel glass">
            <view class="card-title">最近数据<button class="link" :disabled="!recentPointId" @click="openHistory()">查看全部</button></view>
            <view v-if="recentLoading" class="empty">正在读取最近 15 分钟数据…</view>
            <view v-else-if="recentError" class="error">{{ recentError }}</view>
            <HistoryTable v-else :records="recent" compact />
          </section>
        </view>
        </view>

        <view class="daily glass">
          <view class="dfig"><text>今日电量</text><b>—<small>kWh</small></b></view>
          <view class="dfig"><text>运行时长</text><b>—<small>h</small></b></view>
          <view class="dfig"><text>通信成功率</text><b>{{ successRate }}</b></view>
          <view class="dfig"><text>数据新鲜度</text><b>—</b></view>
          <view class="bars-wrap"><view class="bcap">24 小时指标趋势</view><view class="bars-empty">—</view></view>
        </view>
        <view class="colophon"><text>FastIOT · {{ projectName }} · {{ layerName }}</text><text>数据来自真实接口；未配置项显示 —</text></view>
      </main>

      <aside class="side glass desktop-only">
        <section>
          <view class="card-title">待处理 <text class="alarm-num">{{ layerPendingAlarms.length }}</text></view>
          <view v-for="alarm in layerPendingAlarms.slice(0, 3)" :key="alarm.id" class="alarm-mini">
            <view class="title">{{ alarm.title }}</view><view class="meta">{{ alarm.severity }} · {{ shortTime(alarm.updated_at) }} · 待接手</view>
            <button class="ack" :disabled="alarmBusy === alarm.id" @click="runAlarm('ack', alarm.id)">接手</button>
          </view>
          <view v-if="!layerPendingAlarms.length" class="side-empty">—</view>
          <view v-if="alarmError" class="error">{{ alarmError }}</view>
          <button class="link" @click="alarmOpen = true">打开告警中心</button>
        </section>
        <section>
          <view class="card-title summary-title">今日执行</view>
          <view class="run"><span>执行记录</span><span class="run-state">—</span></view>
          <view class="summary-note">后端未提供“今日执行”查询 API，不推算触发、完成、跳过或失败记录。</view>
        </section>
        <section class="summary-section">
          <view class="card-title summary-title">运行摘要</view>
          <view class="run"><span>已接入设备</span><b>{{ scopedDevices.length }}</b></view>
          <view class="run"><span>在线设备</span><b>{{ onlineCount }}</b></view>
          <view class="run"><span>有效观测</span><b>{{ goodValueCount }} / {{ scopedValues.length }}</b></view>
          <view class="run"><span>5 分钟通信成功率</span><b>{{ successRate }}</b></view>
          <view class="summary-note">今日电量、运行时长等按日累计暂无对应查询 API，本屏不推算。</view>
        </section>
      </aside>
    </view>

    <AlarmCenter v-if="alarmOpen" :alarms="session.alarms" :busy-id="alarmBusy" :error="alarmError" @close="alarmOpen = false"
      @ack="runAlarm('ack', $event)" @close-alarm="runAlarm('close', $event)" @history="openHistory" @locate="locateDevice" />
    <view v-if="session.error" class="error page-error">{{ session.error }}</view>
  </view>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue"
import { onHide, onLoad, onShow } from "@dcloudio/uni-app"
import TopBar from "../../components/TopBar.vue"
import LedgerTree from "../../components/LedgerTree.vue"
import FlowScene from "../../components/FlowScene.vue"
import AlarmCenter from "../../components/AlarmCenter.vue"
import HistoryTable from "../../components/HistoryTable.vue"
import { request } from "../../api/client"
import {
  acknowledgeAlarm, closeAlarm, currentLayer as currentNode, layerPath, loadHistory, loadScreen,
  nameOf, requireAuth, session, startPolling, stopPolling,
} from "../../stores/session"
import type { HistoryRecord, ScreenConfig } from "../../types/api"
import {
  boundDevicesForLayer, businessName, descendantNodeIds, deviceKey, displaySample, healthLabel, metricForPoint,
  qualityLabel, shortTime, summarizeSuccessRate, unitOf, valueInLayer, alarmInLayer,
} from "./overview"
import type { LedgerDevice, QualityStats } from "./overview"

const alarmOpen = ref(false)
const alarmBusy = ref("")
const alarmError = ref("")
const screen = ref<ScreenConfig | null>(null)
const recent = ref<HistoryRecord[]>([])
const recentLoading = ref(false)
const recentError = ref("")
const ledger = ref<LedgerDevice[]>([])
const ledgerError = ref("")
const selectedDeviceId = ref("")
const treeCollapsed = ref(false)
const alpha = ref(Number(uni.getStorageSync("fastiot-overview-alpha") || 42))
const glassStyle = computed(() => ({ "--glass": `rgba(255,255,255,${(alpha.value / 100).toFixed(2)})` }))
function setAlpha(value: number) { alpha.value = value; uni.setStorageSync("fastiot-overview-alpha", value) }

const projectName = computed(() => {
  const root = session.snapshot?.roots[0]
  const node = session.snapshot?.nodes.find(item => item.id === root)
  return node ? nameOf(node) : "未选择项目"
})
const selectedDevice = computed(() => ledger.value.find(device => deviceKey(device) === selectedDeviceId.value) || null)
const selectedKey = computed(() => selectedDeviceId.value ? `device:${selectedDeviceId.value}` : session.layer)
const layerName = computed(() => selectedDevice.value?.name || (currentNode.value ? nameOf(currentNode.value) : "未选择对象"))
const pathText = computed(() => {
  const path = layerPath(session.layer).map(node => nameOf(node))
  if (selectedDevice.value) path.push(selectedDevice.value.name)
  return path.join(" / ") || "—"
})
const isDevice = computed(() => !!selectedDevice.value || !!currentNode.value && ["device", "modbus_device"].includes(currentNode.value.type))
const scopedDevices = computed(() => {
  if (selectedDevice.value) return [selectedDevice.value]
  return currentNode.value && session.snapshot ? boundDevicesForLayer(currentNode.value.id, session.snapshot.nodes, ledger.value) : []
})
const scopedDeviceIds = computed(() => new Set(scopedDevices.value.map(device => deviceKey(device))))
const knownDeviceIds = computed(() => new Set(ledger.value.map(device => deviceKey(device))))
const scopeNodeIds = computed(() => currentNode.value && session.snapshot ? descendantNodeIds(currentNode.value.id, session.snapshot.nodes) : new Set<string>())
const scopedValues = computed(() => session.values.filter(value => {
  if (selectedDevice.value) return scopedDeviceIds.value.has(value.device_id)
  if (!currentNode.value) return false
  return valueInLayer(value, scopeNodeIds.value, scopedDeviceIds.value, knownDeviceIds.value)
}))
const pointIds = computed(() => {
  if (!selectedDevice.value && screen.value?.point_ids?.length) return screen.value.point_ids.filter(id => scopeNodeIds.value.has(id)).slice(0, 6)
  return [...new Set(scopedValues.value.map(value => value.point_id))].slice(0, 6)
})
const metrics = computed(() => pointIds.value.map(id => metricForPoint(id, session.snapshot, scopedValues.value)))
const metricIcons = [
  "M4 12a8 8 0 1 1 16 0M12 12l4-4M12 4v2M4 12h2M18 12h2",
  "M13 2L5 13h6l-1 9 8-12h-6z",
  "M4 20V7l8-4 8 4v13M8 20v-6h8v6M8 9h2M14 9h2M8 12h2M14 12h2",
  "M4 7h15v11H4zM19 10h2v5h-2M8 10v5M12 10v5M16 10v5",
  "M6 16v-5a6 6 0 0 1 12 0v5l2 3H4zM10 21h4",
  "M4 18v-4M9 18V9M14 18V5M19 18V2",
]
const flowMetrics = computed(() => metrics.value.slice(0, 3))
const recentPointId = computed(() => pointIds.value[0] || "")
const qualityDevices = computed(() => session.quality.devices as QualityStats[])
const qualityChannels = computed(() => session.quality.channels as QualityStats[])
const scopedChannels = computed(() => {
  const ids = new Set(scopedDevices.value.map(device => device.channel_id))
  return qualityChannels.value.filter(channel => ids.has(channel.channel_id))
})
const onlineCount = computed(() => scopedDevices.value.filter(device => {
  const quality = qualityDevices.value.find(item => item.device_id === deviceKey(device))
  return (quality?.health || device.health) === "healthy"
}).length)
const goodValueCount = computed(() => scopedValues.value.filter(value => value.quality === "good" || value.quality === "有效").length)
const scopedQualityDevices = computed(() => qualityDevices.value.filter(item => !!item.device_id && scopedDeviceIds.value.has(item.device_id)))
const successRate = computed(() => summarizeSuccessRate(scopedQualityDevices.value))
const selectedCommunication = computed(() => {
  if (!selectedDevice.value) return "—"
  const id = deviceKey(selectedDevice.value)
  return healthLabel(qualityDevices.value.find(item => item.device_id === id)?.health || selectedDevice.value.health)
})
const onlineText = computed(() => isDevice.value ? `通信${selectedCommunication.value} · ${scopedValues.value.length} 个观测` : `在线 ${onlineCount.value} / ${scopedDevices.value.length}`)
const syncMeta = computed(() => `更新于 ${shortTime(session.lastSync)} · 通道 ${scopedChannels.value.length} · 有效观测 ${goodValueCount.value}/${scopedValues.value.length}`)
const unackedAlarms = computed(() => session.alarms.filter(alarm => alarm.active && alarm.state === "active"))
const layerPendingAlarms = computed(() => unackedAlarms.value.filter(alarm => alarmInLayer(alarm, scopeNodeIds.value, scopedDeviceIds.value)))
const deviceRows = computed(() => scopedDevices.value.map(device => {
  const id = deviceKey(device)
  const quality = qualityDevices.value.find(item => item.device_id === id)
  const samples = session.values.filter(value => value.device_id === id)
  const primary = samples[0]
  const primaryNode = session.snapshot?.nodes.find(node => node.id === primary?.point_id)
  const primaryLabel = businessName(primaryNode)
  const runSample = samples.find(sample => /运行反馈|运行状态/.test(businessName(session.snapshot?.nodes.find(node => node.id === sample.point_id))))
  const runNode = session.snapshot?.nodes.find(node => node.id === runSample?.point_id)
  const unit = unitOf(primaryNode)
  return {
    id,
    name: device.name,
    model: device.model || device.profile_id || "—",
    communication: healthLabel(quality?.health || device.health),
    runState: runSample ? displaySample(runSample, businessName(runNode)) : "—",
    value: `${displaySample(primary, primaryLabel)}${unit ? ` ${unit}` : ""}`,
    quality: qualityLabel(primary?.quality),
    time: shortTime(primary?.source_time || primary?.received_time || quality?.last_success),
  }
}))

let layerSerial = 0
let recentSerial = 0
async function loadLayer() {
  if (!session.layer) return
  const serial = ++layerSerial
  const layer = session.layer
  screen.value = null
  const result = await loadScreen(layer)
  if (serial !== layerSerial || layer !== session.layer) return
  screen.value = result
  await loadRecent()
}
async function loadRecent() {
  const id = recentPointId.value
  const serial = ++recentSerial
  recentError.value = ""
  if (!id) { recent.value = []; recentLoading.value = false; return }
  recentLoading.value = true
  const end = new Date()
  const start = new Date(end.getTime() - 15 * 60 * 1000)
  try {
    const result = await loadHistory(id, start, end)
    if (serial !== recentSerial) return
    recent.value = (result.results[0]?.records || []).slice(-5).reverse()
  } catch (error) {
    if (serial !== recentSerial) return
    recent.value = []
    recentError.value = `最近数据读取失败：${error instanceof Error ? error.message : String(error)}`
  } finally {
    if (serial === recentSerial) recentLoading.value = false
  }
}
async function loadDevices() {
  ledgerError.value = ""
  try {
    const result = await request<{ devices: LedgerDevice[] }>("/device-ledger")
    ledger.value = result.devices || []
    if (selectedDeviceId.value && !ledger.value.some(device => deviceKey(device) === selectedDeviceId.value)) selectedDeviceId.value = ""
  } catch (error) {
    ledger.value = []
    ledgerError.value = `设备台账读取失败：${error instanceof Error ? error.message : String(error)}`
  }
}
function selectLayer(key: string) {
  if (key.startsWith("device:")) {
    const id = key.slice(7)
    const device = ledger.value.find(item => deviceKey(item) === id)
    selectedDeviceId.value = id
    if (device?.business_node_id) session.layer = device.business_node_id
  } else {
    selectedDeviceId.value = ""
    session.layer = key
  }
}
function selectDevice(id: string) { selectLayer(`device:${id}`) }
function openHistory(pointId?: string) {
  const id = pointId || recentPointId.value
  if (id) uni.navigateTo({ url: `/pages/history/index?point=${encodeURIComponent(id)}` })
}
function navigate(key: string) { if (key !== "overview") uni.navigateTo({ url: `/pages/${key}/index` }) }
async function runAlarm(action: "ack" | "close", id: string) {
  alarmBusy.value = id
  alarmError.value = ""
  try { await (action === "ack" ? acknowledgeAlarm(id) : closeAlarm(id)) }
  catch (error) { alarmError.value = error instanceof Error ? error.message : String(error) }
  finally { alarmBusy.value = "" }
}
function locateDevice(id: string) {
  alarmOpen.value = false
  if (session.snapshot?.nodes.some(node => node.id === id)) selectLayer(id)
  else selectDevice(id)
}

watch(() => session.snapshot, snapshot => {
  if (selectedDeviceId.value && snapshot?.nodes.some(node => node.id === selectedDeviceId.value)) {
    session.layer = selectedDeviceId.value
    selectedDeviceId.value = ""
  }
})
watch(() => session.layer, () => { void loadLayer() })
watch(recentPointId, () => { void loadRecent() })
watch(selectedDeviceId, () => { void loadRecent() })
onLoad(options => { selectedDeviceId.value = (options?.device as string) || "" })
onMounted(() => {
  if (!requireAuth()) return
  void loadDevices()
  void loadLayer()
})
onShow(() => { if (requireAuth()) startPolling() })
onHide(stopPolling)
</script>

<style scoped>
.page { --muted: rgba(23,44,59,.56); padding: 16px; overflow-x: hidden; }
.layout { display: grid; grid-template-columns: 200px minmax(0, 1fr) 320px; gap: 14px; margin-top: 14px; transition: grid-template-columns .2s ease; }
.layout.tree-collapsed { grid-template-columns: 58px minmax(0, 1fr) 320px; }
.stage { display: flex; flex-direction: column; gap: 14px; min-width: 0; overflow: auto; padding-right: 2px; }
.hero { padding: 16px 20px 4px; }
.crumb { color: var(--muted); font-size: 11px; letter-spacing: .06em; }
.headline { display: flex; align-items: baseline; justify-content: space-between; gap: 16px; margin-top: 4px; }
h1 { margin: 0; font-size: 34px; line-height: normal; letter-spacing: -.02em; }
.online { color: var(--muted); font-size: 12px; text-align: right; white-space: nowrap; }
.metrics { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 0; margin-top: 14px; padding: 12px 0 10px; border-top: 1px solid var(--line); }
.metric { min-width: 0; min-height: 0; height: 70px; margin: 0 6px; padding: 10px 12px; border: 1px solid rgba(255,255,255,.9); border-radius: 14px; background: rgba(255,255,255,.58); box-shadow: 0 8px 24px rgba(54,92,120,.07), inset 0 0 0 1px rgba(255,255,255,.2), inset 0 0 12px rgba(255,255,255,.3); color: var(--ink); font-size: 16px; line-height: normal; overflow: visible; text-align: left; }
.metric:first-child { margin-left: 0; }
.metric:last-child { margin-right: 0; }
.metric:disabled { cursor: default; opacity: 1; }
.metric.vacant { background: rgba(255,255,255,.3); box-shadow: inset 0 0 12px rgba(255,255,255,.24); }
.metric .k { display: flex; align-items: center; gap: 7px; color: var(--muted); font-size: 10.5px; line-height: normal; overflow: visible; white-space: nowrap; }
.metric .k svg { flex: none; width: 16px; height: 16px; fill: none; stroke: #3b8aa8; stroke-width: 1.6; stroke-linecap: round; stroke-linejoin: round; }
.metric .v { margin-top: 4px; color: #183247; font-size: 24px; line-height: normal; letter-spacing: -.01em; font-weight: 600; overflow: visible; white-space: nowrap; }
.metric .v text { margin-left: 3px; color: var(--muted); font-size: 10px; font-weight: 400; }
.metric.vacant .v { color: var(--faint); }
.lower { display: grid; grid-template-columns: minmax(0, 1.3fr) minmax(0, 1fr); gap: 14px; min-height: 307px; }
.panel { padding: 14px 16px; min-width: 0; }
.side { display: flex; flex-direction: column; gap: 18px; padding: 16px 18px; align-self: stretch; overflow: auto; }
.alarm-num { color: var(--amber); font-size: 25px; line-height: normal; }
.side section:first-child .card-title { height: 36px; }
.alarm-mini { position: relative; border-top: 1px solid var(--line); padding: 10px 0; font-size: 12.5px; }
.card-title + .alarm-mini { border-top: 0; padding-top: 2px; }
.alarm-mini .title { padding-right: 52px; font-size: 12.5px; font-weight: 600; }
.alarm-mini .meta { margin-top: 3px; color: var(--muted); font-size: 10.5px; }
.ack { position: absolute; top: 10px; right: 0; min-height: 0; padding: 3px 8px; border: 1px solid var(--glass-border); border-radius: 8px; background: rgba(255,255,255,.06); color: var(--muted); font-size: 10.5px; line-height: normal; overflow: visible; }
.side .link { min-height: 26px; font-size: 10.5px; text-decoration: none; }
.summary-title { margin-top: 0; }
.run { display: flex; justify-content: space-between; gap: 10px; padding: 7px 0; border-top: 1px solid var(--line); font-size: 12px; }
.run:first-of-type { border-top: 0; }
.run b { font-weight: 600; font-variant-numeric: tabular-nums; }
.run-state, .side-empty { color: var(--muted); }
.side-empty { padding: 6px 0; }
.summary-section { margin-top: auto; padding-top: 10px; border-top: 1px solid var(--line); font-size: 12px; line-height: 1.75; }
.summary-note { margin-top: 8px; color: var(--muted); font-size: 10.5px; line-height: 1.7; }
.device-link { display: inline; min-height: 0; padding: 0; border: 0; border-radius: 0; background: transparent; color: var(--ink); font-size: 12.5px; font-weight: 600; line-height: normal; overflow: visible; text-align: left; }
.device-link::after { border: 0; }
.lower .panel:first-child td:first-child { font-weight: 600; }
.panel .link { min-height: 28px; font-size: 12px; text-decoration: none; }
.device-facts { display: flex; flex-wrap: wrap; gap: 8px 16px; margin-top: 10px; color: var(--muted); font-size: 10px; }
.placeholder-row td { color: var(--faint); text-align: center; }
.warn { color: var(--amber); }
.page-error { margin-top: 12px; }
.daily { display: grid; grid-template-columns: repeat(4,minmax(0,1fr)) minmax(0,2fr); gap: 0; align-items: end; margin-top: auto; padding: 12px 18px; }
.dfig { padding: 2px 14px; border-left: 1px solid var(--line); }
.dfig:first-child { border-left: 0; padding-left: 0; }
.dfig > text { display: block; color: var(--muted); font-size: 10px; letter-spacing: .06em; }
.dfig b { font-size: 21px; font-weight: 600; }
.dfig b small { margin-left: 2px; color: var(--muted); font-size: 10px; font-weight: 400; }
.bars-wrap { padding-left: 14px; border-left: 1px solid var(--line); }
.bcap { margin-bottom: 6px; color: var(--muted); font-size: 10px; }
.bars-empty { display: flex; align-items: center; justify-content: center; height: 54px; color: var(--muted); }
.colophon { display: flex; justify-content: space-between; padding: 2px 4px 0; color: var(--muted); font-size: 9.5px; }
@media (max-width: 1150px) {
  .layout, .layout.tree-collapsed { grid-template-columns: 170px minmax(0, 1fr); }
  .layout.tree-collapsed { grid-template-columns: 58px minmax(0, 1fr); }
}
@media (max-width: 760px) {
  .page { padding: 8px; }
  .layout, .layout.tree-collapsed { display: block; }
  .stage { margin-top: 10px; }
  h1 { font-size: 29px; }
  .hero { padding: 14px; }
  .metrics { grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .metric .v { font-size: 20px; }
  .lower { grid-template-columns: 1fr; gap: 10px; min-height: 0; }
  .daily { grid-template-columns: 1fr 1fr; gap: 8px 0; margin-top: 0; }
  .bars-wrap { grid-column: 1 / -1; border-left: 0; padding-left: 0; }
  .colophon { flex-direction: column; gap: 3px; }
}
</style>
