<template>
  <view class="page" :class="appearance.theme === 'light' ? '' : `theme-${appearance.theme}`">
    <TopBar active="overview" :project="projectName" :meta="syncMeta" :pending="unackedAlarms.length" :alpha="appearance.alpha"
      :theme="appearance.theme" themes show-appearance show-bell @navigate="navigate" @bell="alarmOpen = true" @alpha="setAlpha" @theme="setTheme" @reset="resetAppearance" />

    <IndustryGate v-if="industryBlocked" />
    <template v-else>
    <view class="layout">
      <LedgerTree :selected="selectedKey" :devices="ledger" :alarms="session.alarms" @select="selectLayer" />
      <main class="stage">
        <view class="hero glass">
          <view class="crumb">{{ pathText }}</view>
          <view class="headline"><h1>{{ layerName }}</h1><text class="online">{{ onlineText }}</text></view>
        </view>
        <ScreenRenderer v-if="!screenError" :document="screenDocument" :snapshot="session.snapshot" :values="session.values" :quality="session.quality" :alarms="session.alarms" :ledger="ledger" :device-id="selectedDeviceId" :layer-id="session.layer" :last-sync="session.lastSync" :runtime-error="session.runtimeError" @history="openHistory" @select="selectLayer">
          <template #alarm>

          <view class="card-title">本层活动告警 <text class="alarm-num">{{ layerActiveAlarms.length }}</text></view>
          <view v-for="alarm in layerActiveAlarms.slice(0, 3)" :key="alarm.id" class="alarm-mini">
            <view class="title">{{ alarm.title }}</view><view class="meta">{{ alarm.severity }} · {{ shortTime(alarm.updated_at) }} · {{ alarm.state === 'acknowledged' ? '已接手，仍活动' : '待接手' }}</view>
            <button v-if="alarm.state === 'active' && alarm.permissions?.ack !== false" class="ack" :disabled="!!alarmBusy" @click="runAlarm('ack', alarm.id)">接手</button>
          </view>
          <view v-if="!layerActiveAlarms.length" class="side-empty">—</view>
          <view v-if="alarmError" class="error">{{ alarmError }}</view>

          </template>
          <template #after-side><HandoverPanel :layer-id="session.layer" /></template>
        </ScreenRenderer>
        <view class="colophon"><text>FastIOT · {{ projectName }} · {{ layerName }}</text><text>数据来自真实接口；未配置项显示 —</text></view>
      </main>
    </view>

    <AlarmCenter v-if="alarmOpen" :alarms="session.alarms" :busy-id="alarmBusy" :error="alarmError || session.runtimeError" @close="alarmOpen = false"
      @ack="runAlarm('ack', $event)" @close-alarm="runAlarm('close', $event)" @history="openHistory" @locate="locateDevice" />
    <view v-if="screenError" class="error page-error">{{ screenError }}</view>
    </template>
    <view v-if="session.error" class="error page-error">{{ session.error }}</view>
  </view>
</template>

<script setup lang="ts">
import IndustryGate from '../../components/IndustryGate.vue'
import { industryBlocked } from '../../stores/session'
import { currentProject, projectContext } from '../../stores/projects'
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue"
import "./theme.css"
import { onHide, onLoad, onShow } from "@dcloudio/uni-app"
import TopBar from "../../components/TopBar.vue"
import LedgerTree from "../../components/LedgerTree.vue"
import ScreenRenderer from "../../components/design/ScreenRenderer.vue"
import { makeScreenDraft } from "../../components/design/screen"
import type { ScreenDocument } from "../../components/design/screen"
import { kindLabel } from "../../components/design/design"
import { memberRolesOf } from "../../components/design/members"
import AlarmCenter from "../../components/AlarmCenter.vue"
import HandoverPanel from "./HandoverPanel.vue"
import { ApiError, request } from "../../api/client"
import {
  acknowledgeAlarm, closeAlarm, currentLayer as currentNode, layerPath,
  nameOf, requireAuth, session, startPolling, stopPolling,
} from "../../stores/session"
import type { ScreenConfig } from "../../types/api"
import {
  boundDevicesForLayer, descendantNodeIds, deviceKey, healthLabel,
  shortTime, valueInLayer, alarmInLayer, devicePointIds,
} from "./overview"
import type { LedgerDevice, OverviewScreen, QualityStats } from "./overview"
import { DEFAULT_APPEARANCE, applyAppearanceDocument, normalizeAppearance } from "./appearance"
import type { Appearance, AppearanceTheme } from "./appearance"

const alarmOpen = ref(false)
const alarmBusy = ref("")
const alarmError = ref("")
const screen = ref<(ScreenConfig & OverviewScreen) | null>(null)
const screenError = ref("")
const ledger = ref<LedgerDevice[]>([])
const ledgerError = ref("")
const selectedDeviceId = ref("")
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

const projectName = computed(() => currentProject.value?.name || "未选择项目")
const isBusinessDevice = computed(() => !!currentNode.value && !session.snapshot?.roots.includes(currentNode.value.id) && ["device", "modbus_device"].includes(currentNode.value.type))
const selectedDevice = computed(() => ledger.value.find(device => selectedDeviceId.value ? deviceKey(device) === selectedDeviceId.value : isBusinessDevice.value && device.business_node_id === session.layer) || null)
const selectedKey = computed(() => selectedDeviceId.value ? `device:${selectedDeviceId.value}` : session.layer)
const layerName = computed(() => selectedDevice.value?.name || (currentNode.value ? nameOf(currentNode.value) : "未选择对象"))
const pathText = computed(() => {
  const path = layerPath(session.layer).map(node => nameOf(node))
  if (selectedDevice.value && !isBusinessDevice.value) path.push(selectedDevice.value.name)
  return path.join(" / ") || "—"
})
const isDevice = computed(() => !!selectedDevice.value || isBusinessDevice.value)
const members = computed(() => memberRolesOf(currentNode.value))
const scopedDevices = computed(() => {
  if (selectedDevice.value) return [selectedDevice.value]
  return currentNode.value && session.snapshot ? boundDevicesForLayer(currentNode.value.id, session.snapshot.nodes, ledger.value, members.value.flatMap(member => member.node_id ? [member.node_id] : [])) : []
})
const scopedDeviceIds = computed(() => new Set(scopedDevices.value.map(device => deviceKey(device))))
const knownDeviceIds = computed(() => new Set(ledger.value.map(device => deviceKey(device))))
const scopeNodeIds = computed(() => currentNode.value && session.snapshot ? descendantNodeIds(currentNode.value.id, session.snapshot.nodes) : new Set<string>())
const scopedValues = computed(() => session.values.filter(value => {
  if (selectedDevice.value) return scopedDeviceIds.value.has(value.device_id)
  if (!currentNode.value) return false
  return valueInLayer(value, scopeNodeIds.value, scopedDeviceIds.value, knownDeviceIds.value)
}))
const availablePointIds = computed(() => new Set([
  ...devicePointIds(session.snapshot?.nodes || [], scopedDevices.value),
  ...(session.snapshot?.nodes || []).filter(node => scopeNodeIds.value.has(node.id) && ['variable', 'calculationPoint'].includes(node.type)).map(node => node.id),
]))
const screenDocument = computed(() => {
  const points = [...availablePointIds.value].map(id => ({ id, label: nameOf(session.snapshot!.nodes.find(node => node.id === id)!) }))
  const document = makeScreenDraft(screen.value, session.layer, layerName.value, currentNode.value ? kindLabel(currentNode.value) : '现场', points)
  const saved = screen.value as ScreenDocument | null
  if (saved?.inherited_from) { document.inherited_from = saved.inherited_from; document.version = saved.version || 0 }
  return document
})
const qualityDevices = computed(() => session.quality.devices as QualityStats[])
const qualityChannels = computed(() => session.quality.channels as QualityStats[])
const scopedChannels = computed(() => {
  const ids = new Set(scopedDevices.value.map(device => device.channel_id))
  return qualityChannels.value.filter(channel => ids.has(channel.channel_id))
})
const onlineCount = computed(() => scopedDevices.value.filter(device => {
  const quality = qualityDevices.value.find(item => item.device_id === deviceKey(device))
  return quality?.health === "healthy"
}).length)
const goodValueCount = computed(() => scopedValues.value.filter(value => value.quality === "good" || value.quality === "有效").length)
const selectedCommunication = computed(() => {
  if (!selectedDevice.value) return "—"
  const id = deviceKey(selectedDevice.value)
  return healthLabel(qualityDevices.value.find(item => item.device_id === id)?.health || 'unknown')
})
const onlineText = computed(() => session.runtimeError ? '通信状态未知 · 实时查询不可用' : isDevice.value ? `通信${selectedCommunication.value} · ${scopedValues.value.length} 个观测` : `在线 ${onlineCount.value} / ${scopedDevices.value.length}`)
const syncMeta = computed(() => {
  const observed = scopedValues.value.map(value => value.source_time || '').filter(Boolean).sort().slice(-1)[0]
  return `最近观测 ${shortTime(observed)} · 查询于 ${shortTime(session.lastSync)} · 通道 ${scopedChannels.value.length} · 有效观测 ${goodValueCount.value}/${scopedValues.value.length}`
})
const unackedAlarms = computed(() => session.alarms.filter(alarm => alarm.active && alarm.state === "active"))
const layerActiveAlarms = computed(() => session.alarms.filter(alarm => alarm.active && alarmInLayer(alarm, scopeNodeIds.value, scopedDeviceIds.value)))
let layerSerial = 0
let ledgerSerial = 0
async function loadLayer() {
  const serial = ++layerSerial
  const layer = session.layer
  screen.value = null
  screenError.value = ""
  if (!layer) return
  try {
    const result = await request<ScreenConfig & OverviewScreen>(`/ems/screen/${encodeURIComponent(layer)}`)
    if (serial !== layerSerial || layer !== session.layer) return
    screen.value = result
  } catch (error) {
    if (serial !== layerSerial || layer !== session.layer) return
    if (!(error instanceof ApiError && error.status === 404 && error.code === 'screen_not_found')) {
      screenError.value = `画面读取失败：${error instanceof Error ? error.message : '服务连接中断'}`
      return
    }
  }
}
async function loadDevices() {
  const serial = ++ledgerSerial
  ledgerError.value = ""
  try {
    const result = await request<{ devices: LedgerDevice[] }>("/device-ledger")
    if (serial !== ledgerSerial) return
    ledger.value = (result.devices || []).filter(device => !!device.business_node_id && (device.point_count || 0) > 0)
    if (selectedDeviceId.value && !ledger.value.some(device => deviceKey(device) === selectedDeviceId.value)) selectedDeviceId.value = ""
  } catch (error) {
    if (serial !== ledgerSerial) return
    ledger.value = []
    ledgerError.value = `设备台账读取失败：${error instanceof Error ? error.message : '服务连接中断'}`
  }
}
function selectLayer(key: string) {
  if (key.startsWith("device:")) {
    const id = key.slice(7)
    const device = ledger.value.find(item => deviceKey(item) === id)
    const business = session.snapshot?.nodes.find(node => node.id === device?.business_node_id)
    selectedDeviceId.value = business && ['device', 'modbus_device'].includes(business.type) ? '' : id
    if (device?.business_node_id) session.layer = device.business_node_id
  } else {
    selectedDeviceId.value = ""
    session.layer = key
  }
}
function selectDevice(id: string) { selectLayer(`device:${id}`) }
function openHistory(pointId?: string) {
  const id = pointId
  if (id) {
    alarmOpen.value = false
    uni.navigateTo({ url: `/pages/history/index?point=${encodeURIComponent(id)}` })
  }
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

function invalidateReads() { layerSerial++; ledgerSerial++ }
watch(() => projectContext.generation, () => {
  invalidateReads()
  ledger.value = []; selectedDeviceId.value = ''; screen.value = null
  ledgerError.value = ''; screenError.value = ''
}, { flush: 'sync' })
watch(() => session.snapshot, snapshot => { if (snapshot) void loadDevices() })
watch(() => session.layer, () => { void loadLayer() }, { flush: 'sync' })
onLoad(options => { selectedDeviceId.value = (options?.device as string) || "" })
onMounted(() => {
  if (!requireAuth()) return
  applyAppearanceDocument(appearance.value)
  if (session.snapshot) void loadDevices()
  void loadLayer()
})
onShow(() => { if (requireAuth()) { startPolling(); void loadLayer() } })
onHide(() => { stopPolling(); invalidateReads() })
onBeforeUnmount(() => { stopPolling(); invalidateReads() })
</script>

<style scoped>
.page { min-height:100vh; padding:16px; overflow-x:hidden; }
.layout { display:grid; grid-template-columns:200px minmax(0,1fr); gap:14px; margin-top:14px; }
.stage { display:flex; flex-direction:column; gap:14px; min-width:0; }
.hero { padding:16px 20px; }
.crumb { color:var(--muted); font-size:11px; letter-spacing:.06em; }
.headline { display:flex; align-items:baseline; justify-content:space-between; gap:16px; margin-top:4px; }
h1 { margin:0; font-size:34px; line-height:normal; letter-spacing:-.02em; }
.online { color:var(--muted); font-size:12px; text-align:right; }
.alarm-num { color:var(--amber); font-size:25px; }
.alarm-mini { position:relative; border-top:1px solid var(--line); padding:10px 0; font-size:12.5px; }
.alarm-mini .title { padding-right:52px; font-weight:600; }
.alarm-mini .meta { margin-top:3px; color:var(--muted); font-size:10.5px; }
.ack { position:absolute; top:10px; right:0; min-height:0; padding:3px 8px; border:1px solid var(--glass-border); border-radius:8px; background:rgba(255,255,255,.06); color:var(--muted); font-size:10.5px; line-height:normal; }
.side-empty { color:var(--muted); padding:6px 0; }
.page-error { margin-top:12px; }
.colophon { display:flex; justify-content:space-between; padding:2px 4px 0; color:var(--muted); font-size:9.5px; }
@media(max-width:1150px) { .layout { grid-template-columns:170px minmax(0,1fr); } }
@media(max-width:760px) { .page { padding:10px; } .layout { display:block; margin-top:10px; } h1 { font-size:29px; } .hero { padding:14px; } .colophon { flex-direction:column; gap:3px; } }
</style>
