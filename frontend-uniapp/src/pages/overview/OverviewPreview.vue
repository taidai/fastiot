<template>
  <view class="page preview-page" :class="appearance.theme === 'light' ? '' : `theme-${appearance.theme}`">
    <view class="bg" aria-hidden="true"><i class="a"></i><i class="b"></i><i class="c"></i><StationArt class="bg-scene" /></view>
    <TopBar active="overview" :project="projectName" meta="更新于 21:34:02 · 通信正常 · 数据质量 99.2% · 采样 5 秒"
      :pending="pendingAlarms.length" :alpha="appearance.alpha" :theme="appearance.theme" themes show-workbench show-appearance show-bell
      @navigate="navigate" @bell="alarmOpen = true" @alpha="setAlpha" @theme="setTheme" @reset="resetAppearance" />
    <view class="frame">
      <nav class="index glass">
        <view class="idx-head"><text>台账</text><text>{{ String(PREVIEW_LAYERS.length).padStart(2, '0') }}</text></view>
        <button v-for="layer in PREVIEW_LAYERS" :key="layer.key" :data-layer="layer.key" :class="{ on: current === layer.key }" @click="selectLayer(layer.key)"><i>{{ OVERVIEW_DATA[layer.key].no }}</i>{{ layer.kind }} · {{ layer.name }}<em v-if="layer.alarm">{{ layer.alarm }}</em></button>
        <view class="gap"></view><view class="layer-meta">设备 {{ DEVICES.length }} · 未绑定 {{ DEVICES.filter(d => d[2] === '未绑定').length }}<br>告警 {{ pendingAlarms.length }} · 今日执行 3<br>按配界面显示</view>
      </nav>
      <main class="stage">
        <section class="hero glass">
          <view class="crumb">{{ currentData.crumb }}</view><view class="headline"><h1>{{ currentData.title }}</h1><view class="online">在线 <b>{{ currentData.online }}</b></view></view>
          <section class="metrics"><view v-for="(kpi, index) in currentData.kpis" :key="kpi[0]" class="metric" :class="{ alert: kpi[3] }" role="button" tabindex="0" @click="openHistory(`${currentData.title} · ${kpi[0]}`)" @keydown.enter="openHistory(`${currentData.title} · ${kpi[0]}`)"><view class="k"><svg viewBox="0 0 24 24" aria-hidden="true"><path :d="METRIC_ICONS[index]" /></svg>{{ kpi[0] }}</view><view class="v num">{{ kpi[1] }}<small v-if="kpi[2]">{{ kpi[2] }}</small></view></view></section>
        </section>
        <template v-if="currentData.device">
          <section class="panel glass"><view class="tabs"><button :class="{ on: deviceTab === 'live' }" @click="deviceTab = 'live'">实时数据</button><button :class="{ on: deviceTab === 'params' }" @click="deviceTab = 'params'">设备参数</button></view><table v-if="deviceTab === 'live'"><thead><tr><th>点位</th><th>值</th><th>单位</th><th>质量</th></tr></thead><tbody><tr v-for="row in POINTS" :key="row[0]"><td class="name">{{ row[0] }}</td><td class="num">{{ row[1] }}</td><td>{{ row[2] }}</td><td class="ok">{{ row[3] }}</td></tr></tbody></table><table v-else><tbody><tr v-for="row in PARAMS" :key="row[0]"><td class="name">{{ row[0] }}</td><td>{{ row[1] }}</td></tr></tbody></table></section>
          <view class="lower"><section class="panel glass"><h2>操作</h2><button class="link" data-op-open @click="openOperation">打开操作抽屉</button><p class="operation-copy">启动、停止、下发目标值与接管自动都在抽屉里完成；下发后等待回读。</p><h2>操作记录</h2><view class="run"><span>10:42 · 启动</span><span class="r done">已回读</span></view><view class="run"><span>10:40 · 确认报警 2号泵</span><span class="r done">已接手</span></view><view class="run"><span>09:58 · 下发目标值 0.45 MPa</span><span class="r done">已回读</span></view></section><section class="panel glass"><h2>最近数据 <button class="link" @click="openHistory()">查看全部</button></h2><RecentTable /></section></view>
        </template>
        <template v-else>
          <FlowScene v-if="isPump" class="chain-wrap" layer-name="教学泵房 · 供水" :metrics="flowMetrics" prototype />
          <section v-else-if="project.requiresEms" class="panel glass energy-flow"><template v-for="(item, index) in flowMetrics" :key="item.label"><i v-if="index">{{ index === 1 ? '→' : '←' }}</i><div class="flow-node"><span>{{ item.label }}</span><b>{{ item.value }} {{ item.unit }}</b><small>{{ item.detail }}</small></div></template></section>
          <view class="lower"><section class="panel glass"><template v-if="currentData.list === 'members'"><h2>成员</h2><table><thead><tr><th>角色</th><th>成员</th><th>主要值</th><th>状态</th></tr></thead><tbody><tr v-for="row in MEMBERS" :key="row[0]"><td class="name">{{ row[0] }}</td><td>{{ row[1] }}</td><td class="num">{{ row[2] }}</td><td class="ok">{{ row[3] }}</td></tr></tbody></table></template><template v-else><h2>设备清单</h2><table class="device-table"><thead><tr><th>设备</th><th>类型</th><th>状态</th><th>主要值</th><th>通信</th><th>更新</th></tr></thead><tbody><tr v-for="row in DEVICES" :key="row[0]"><td class="name">{{ row[0] }}</td><td>{{ row[1] }}</td><td class="ok">{{ row[2] }}</td><td class="num">{{ row[3] }}</td><td>{{ row[4] }}</td><td>{{ row[5] }}</td></tr></tbody></table><h2 class="recent-title">最近变化</h2><view v-for="row in RECENT_CHANGES" :key="row[0]" class="run"><span>{{ row[0] }}</span><span class="r">{{ row[1] }}</span></view></template></section><section class="panel glass"><h2>最近数据 <button class="link" @click="openHistory()">查看全部</button></h2><RecentTable /></section></view>
        </template>
        <section class="daily glass"><view class="dfig"><span>今日电量</span><b>124 <small>kWh</small></b></view><view class="dfig"><span>运行时长</span><b>6.2 <small>h</small></b></view><view class="dfig"><span>通信成功率</span><b>99.2 <small>%</small></b></view><view class="dfig"><span>数据新鲜度</span><b>2 <small>秒</small></b></view><view class="bars-wrap"><view class="bcap">24 小时出口压力</view><view class="bars"><i v-for="(value, index) in TREND_BARS" :key="index" :class="{ on: index === 17 }" :style="{ height: `${value}%` }"></i></view></view></section>
        <view class="colophon"><span>FastIOT · {{ projectName }} · 第 {{ currentData.no }} 层</span><span>原型预览数据；后端接入后替换</span></view>
      </main>
      <aside class="side glass"><section><h2>待处理 <b>{{ pendingAlarms.length }}</b></h2><view v-for="alarm in pendingAlarms" :key="alarm.id" class="alarm"><view class="t">{{ alarm.title }}</view><view class="m">{{ alarm.level }} · {{ alarm.at }} · 待接手</view><button class="ack" @click="ack(alarm.id)">接手</button></view><view v-if="!pendingAlarms.length" class="alarm"><view class="t">没有待处理报警</view><view class="m">设备与数据正常。</view></view></section><section class="today-runs"><h2>今日执行</h2><view v-for="row in TODAY_RUNS" :key="row[0]" class="run"><span>{{ row[0] }}</span><span class="r" :class="{ done: row[1] === '完成', skip: row[1] === '跳过' }">{{ row[1] }}</span></view></section><section class="handover"><h2>交接</h2><view>{{ isPump ? '上一班 张工：2号泵检修后已恢复；夜间注意液位。' : '—' }}</view></section></aside>
    </view>
    <view class="drawer glass" :class="{ open: alarmOpen }" role="dialog" aria-label="告警中心"><h2><span>告警中心 · <b>{{ pendingAlarms.length }}</b> 条待处理</span><button class="x" @click="alarmOpen = false">关闭</button></h2><view v-for="alarm in pendingAlarms" :key="alarm.id" class="alarm"><view class="t">{{ alarm.title }}<template v-if="alarm.occurrences > 1">（第 {{ alarm.occurrences }} 次）</template></view><view class="m">{{ alarm.level }} · {{ alarm.at }} · {{ alarm.device }}</view><view class="act"><button @click="ack(alarm.id)">确认接手</button><button @click="recoverAlarm(alarm.id)">标记恢复</button><button @click="locate(alarm.device)">定位到设备</button><button @click="openHistory(`${alarm.device} · 出口压力`)">看历史</button></view></view><p v-if="!pendingAlarms.length" class="note">没有待处理报警。</p><h2 class="history-title">历史</h2><view v-for="alarm in alarmHistory" :key="alarm.id" class="alarm done"><view class="t">{{ alarm.title }}<template v-if="alarm.occurrences > 1">（第 {{ alarm.occurrences }} 次）</template></view><view class="m">{{ alarm.level }} · {{ alarm.at }} · 已恢复 {{ alarm.recoveredAt }}</view><view class="act"><button @click="retrigger(alarm.id)">再次触发</button></view></view><p class="note">确认接手不等于设备恢复；超时未回读显示“结果未知”，不伪装成功。</p></view>
    <view class="drawer glass" :class="{ open: operationOpen }" role="dialog" aria-label="设备操作"><h2><span>操作 · {{ currentData.title }}</span><button class="x" @click="operationOpen = false">关闭</button></h2><view class="op-rows"><view class="row"><span>当前值</span><span class="num">{{ opScenario === 'offline' ? '—（无最新值）' : '0.42 MPa' }}</span></view><view class="row"><span>目标值</span><span class="num">0.45 MPa</span></view><view class="row"><span>运行状态</span><span>{{ opScenario === 'offline' ? '离线' : '运行 · 自动' }}</span></view></view><view class="chips op-scenario"><span>场景</span><button v-for="item in scenarios" :key="item.key" :data-scenario="item.key" :class="{ on: opScenario === item.key }" @click="opScenario = item.key">{{ item.label }}</button></view><view class="act op-actions"><button v-for="action in ['启动','停止','下发目标值','接管自动']" :key="action" :data-op="action" @click="runOperation(action)">{{ action }}</button></view><view class="op-result">{{ operationResult }}</view><p class="note">下发只是请求；回读确认后才算完成。接管不等于设备恢复。</p></view>
    <view class="hist glass" :class="{ open: historyOpen }"><view class="hist-head"><button class="link hist-back" @click="historyOpen = false">← 返回</button><h1>{{ historyTitle }}</h1><span class="spacer"></span><button class="link" @click="exportHistory">导出 CSV</button></view><view class="hist-sub">最近 {{ historyRange }} · {{ historyGran === '自动' ? '自动粒度' : `${historyGran}/行` }} · 保留期 30 天 · 缺失段不补值</view><view class="hist-bar"><view class="chips"><button v-for="item in ['15分钟','1小时','24小时','3天','7天']" :key="item" :class="{ on: historyRange === item }" @click="historyRange = item">{{ item }}</button></view><view class="chips"><button v-for="item in ['自动','1秒','5秒','30秒','1分钟']" :key="item" :class="{ on: historyGran === item }" @click="historyGran = item">{{ item }}</button></view><view class="chips"><button data-view="数据表" :class="{ on: historyView === '数据表' }" @click="historyView = '数据表'">数据表</button><button data-view="曲线" :class="{ on: historyView === '曲线' }" @click="historyView = '曲线'">曲线</button></view></view><view v-if="historyView === '数据表'" class="hist-table"><table><thead><tr><th v-for="column in historyColumns" :key="column">{{ column }}</th></tr></thead><tbody><tr v-for="row in historyRows" :key="row[0]"><td v-for="(cell, index) in row" :key="index" :class="{ num: index >= 1 && index <= 3, off: index === row.length - 1 && cell !== '有效' }">{{ cell }}</td></tr></tbody></table></view><view v-else class="hist-chart"><svg viewBox="0 0 960 340" preserveAspectRatio="none"><g class="grid"><line x1="20" y1="80" x2="940" y2="80"/><line x1="20" y1="170" x2="940" y2="170"/><line x1="20" y1="260" x2="940" y2="260"/></g><line class="target" x1="20" y1="174" x2="940" y2="174"/><polyline v-for="segment in historySegments" :key="segment[0]" :points="segment.join(' ')" /></svg><view class="hist-foot"><span v-for="tick in (isPump ? ['0.35','0.40','0.45','0.50'] : currentData.device ? ['7.5','8.0','8.5','9.0'] : ['11.7','12.2','12.7','13.2'])" :key="tick">{{ tick }}</span></view></view><view class="hist-foot"><span>数据质量 99.2% · 共 30 行</span><span>导出上限 2000 行</span></view></view>
    <view class="toast" :class="{ show: !!toastText }">{{ toastText }}</view>
  </view>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, ref, watch, toRef } from "vue"
const props = defineProps<{ initialHistory?: boolean }>()
import TopBar from "../../components/TopBar.vue"
import FlowScene from "../../components/FlowScene.vue"
import StationArt from "../../components/StationArt.vue"
import { downloadText } from "../../api/client"
import { DEFAULT_APPEARANCE, applyAppearanceDocument, normalizeAppearance } from "./appearance"
import type { Appearance, AppearanceTheme } from "./appearance"
import { OVERVIEW_DATA as PUMP_DATA, PREVIEW_LAYERS as PUMP_LAYERS, DEVICES as PUMP_DEVICES, POINTS as PUMP_POINTS, PARAMS as PUMP_PARAMS, MEMBERS, RECENT_CHANGES as PUMP_CHANGES, TODAY_RUNS as PUMP_RUNS, TREND_BARS, METRIC_ICONS } from "./preview-data"
import { overviewPreviewState } from './preview-state'
import { previewWorkspace, previewProjects } from '../../api/preview/workspace'
import { resolveDesignPreview, selectDesignPreviewProject } from '../../api/preview/design'
import { projectOverview } from './project-overview'
import type { Snapshot } from '../../types/api'
import "./theme.css"
import "./preview.css"

const RecentTable = defineComponent({ setup() {
  const rows = Array.from({ length: 5 }, (_, index) => { const time = new Date(new Date("2026-11-22T21:34:00").getTime() - index * 5000).toLocaleTimeString("zh-CN", { hour12: false }); return isPump ? [time, (0.42 + Math.sin(index / 3) * 0.02).toFixed(2), "0.45", (12.4 + Math.cos(index / 4) * 0.8).toFixed(1), (68 - index * 0.2).toFixed(1)] : [time, '8.0', '76', '32.4', '运行'] })
  return () => h("table", [
    h("thead", [h("tr", (isPump ? ["时间", "出口压力", "目标", "电流 A", "液位 %"] : ['时间','功率 kW','SOC %','温度 °C','运行状态']).map(label => h("th", label)))]),
    h("tbody", rows.map(row => h("tr", row.map((cell, index) => h("td", { class: index > 0 ? "num" : "" }, cell))))),
  ])
} })
const project = previewProjects.find(p => p.id === previewWorkspace.projectId)!
const projectName = project.name
const isPump = project.id === 'site-pump'
selectDesignPreviewProject(project.id, project.name)
const projectData = projectOverview(resolveDesignPreview('/snapshot', 'GET').data as Snapshot, project.name, project.requiresEms)
const OVERVIEW_DATA = isPump ? PUMP_DATA : projectData.data
const PREVIEW_LAYERS = isPump ? PUMP_LAYERS : projectData.layers
const DEVICES = isPump ? PUMP_DEVICES : projectData.devices
const POINTS = isPump ? PUMP_POINTS : [['有功功率','8.0','kW','有效'],['SOC','76','%','有效'],['温度','32.4','°C','有效'],['运行状态','运行','','有效']]
const PARAMS = isPump ? PUMP_PARAMS : [['来源模板','储能变流器 v2.1'],['型号','PCS-100'],['单位','V / A / kW / °C'],['最近验证','2026-11-19 · 张工']]
const RECENT_CHANGES = isPump ? PUMP_CHANGES : []
const TODAY_RUNS = isPump ? PUMP_RUNS : project.id === 'site-farm' ? [['08:00 批量并网','完成'],['12:00 SOC 校准','跳过'],['23:00 谷电充电','完成']] : project.requiresEms ? [['08:00 早高峰放电','完成'],['12:00 光伏限功率','跳过'],['23:00 谷电充电','完成']] : []
const retained = overviewPreviewState(project.id)
const current = toRef(retained, 'current')
const currentData = computed(() => OVERVIEW_DATA[current.value] || OVERVIEW_DATA[Object.keys(OVERVIEW_DATA)[0]])
const deviceTab = toRef(retained, 'deviceTab')
const flowMetrics = isPump ? [{ label: "水箱", value: "68", unit: "%" }, { label: "1号泵", value: "运行", detail: "0.42 MPa · 12.4 A" }, { label: "供水管网", value: "0.42", unit: "MPa" }] : projectData.flow
const alarms = toRef(retained, 'alarms')
const alarmHistory = toRef(retained, 'alarmHistory')
const acked = toRef(retained, 'acked')
const pendingAlarms = computed(() => alarms.value.filter(item => !acked.value[item.id]))
const alarmOpen = ref(false)
const operationOpen = ref(false)
const opScenario = toRef(retained, 'opScenario')
const operationResult = toRef(retained, 'operationResult')
const scenarios = [{ key: "success", label: "回读成功" }, { key: "timeout", label: "超时" }, { key: "denied", label: "拒绝" }, { key: "offline", label: "离线" }]
const historyOpen = ref(false)
const historyTitle = ref("1号泵 · 出口压力")
const historyRange = toRef(retained, 'historyRange')
const historyGran = toRef(retained, 'historyGran')
const historyView = toRef(retained, 'historyView')
const toastText = ref("")
let toastTimer: ReturnType<typeof setTimeout> | undefined
const appearance = ref<Appearance>(storedAppearance())
function storedAppearance(): Appearance { const stored = uni.getStorageSync("fastiot-overview-appearance"); if (stored) { try { return normalizeAppearance(typeof stored === "string" ? JSON.parse(stored) : stored) } catch { return { ...DEFAULT_APPEARANCE } } } return { ...DEFAULT_APPEARANCE } }
function persistAppearance() { uni.setStorageSync("fastiot-overview-appearance", JSON.stringify(appearance.value)); applyAppearanceDocument(appearance.value) }
function setAlpha(alpha: number) { appearance.value = normalizeAppearance({ ...appearance.value, alpha }); persistAppearance() }
function setTheme(theme: AppearanceTheme) { appearance.value = normalizeAppearance({ ...appearance.value, theme }); persistAppearance() }
function resetAppearance() { appearance.value = { ...DEFAULT_APPEARANCE }; persistAppearance() }
function navigate(key: string) { if (key !== "overview") uni.navigateTo({ url: `/pages/${key}/index` }) }
function selectLayer(key: string) { current.value = key; operationResult.value = "" }
function toast(text: string) { toastText.value = text; clearTimeout(toastTimer); toastTimer = setTimeout(() => { toastText.value = "" }, 1800) }
function ack(id: string) { acked.value = { ...acked.value, [id]: true }; toast("已接手；不等于设备恢复") }
function recoverAlarm(id: string) { const alarm = alarms.value.find(item => item.id === id); if (!alarm) return; alarms.value = alarms.value.filter(item => item.id !== id); acked.value = { ...acked.value, [id]: true }; alarmHistory.value.unshift({ ...alarm, recoveredAt: "刚刚" }); toast("已标记恢复并移入历史") }
function retrigger(id: string) { const index = alarmHistory.value.findIndex(item => item.id === id); if (index < 0) return; const [alarm] = alarmHistory.value.splice(index, 1); alarms.value.push({ ...alarm, occurrences: alarm.occurrences + 1 }); acked.value = { ...acked.value, [id]: false }; toast("再次触发：沿用同一告警并累计次数") }
function locate(device: string) { current.value = ({ "1号泵": "d1", "2号泵": "d2", "水箱": "t1" } as Record<string, string>)[device] || "area"; alarmOpen.value = false }
function openOperation() { operationResult.value = ""; operationOpen.value = true }
function runOperation(action: string) { operationResult.value = opScenario.value === "success" ? `${action}：已回读，操作完成` : opScenario.value === "timeout" ? `${action}：已下发，等待回读超时，结果未知` : opScenario.value === "denied" ? `${action}：已拒绝，未下发` : `${action}：设备离线，未下发` }
function openHistory(title?: string) { historyTitle.value = title || `${currentData.value.title} · ${isPump ? '出口压力' : '功率'}`; alarmOpen.value = false; historyOpen.value = true }
const historyColumns = computed(() => isPump ? ['时间','出口压力 MPa','目标 MPa','电流 A','运行状态','质量'] : currentData.value.device ? ['时间','功率 kW','直流电压 V','温度 °C','质量'] : ['时间','光伏 kW','储能 kW','SOC %','并网 kW','质量'])
const historyRows = computed(() => Array.from({ length: 30 }, (_, index) => {
  const time = new Date(new Date('2026-11-22T21:34:00').getTime() - index * 5000).toLocaleTimeString('zh-CN', { hour12: false })
  if (isPump) {
    if (index === 7 || index === 8) return [time,'—','—','—','—',index === 7 ? '失效' : '断线']
    return [time,(0.42 + Math.sin(index / 3) * 0.02).toFixed(2),'0.45',(12.4 + Math.cos(index / 4) * 0.8).toFixed(1),'运行','有效']
  }
  const cells = currentData.value.device ? [(8 + Math.cos(index / 4) * 0.4).toFixed(1),(750 + Math.sin(index / 5) * 6).toFixed(0),(32 + Math.sin(index / 3) * 1.2).toFixed(1)] : [(12.4 + Math.sin(index / 3) * 0.6).toFixed(1),(8 + Math.cos(index / 4) * 0.4).toFixed(1),(76 - index * 0.1).toFixed(1),(20.4 + Math.sin(index / 5) * 0.5).toFixed(1)]
  return [time,...cells,'有效']
}))
const historySegments = computed(() => { const segments: string[][] = []; let segment: string[] = []; historyRows.value.forEach((row, index) => { const value = Number.parseFloat(row[1]); if (row[1] === "—" || Number.isNaN(value)) { if (segment.length) segments.push(segment); segment = [] } else segment.push(`${20 + index * 30},${300 - (value - (isPump ? 0.35 : currentData.value.device ? 7.5 : 11.7)) * (isPump ? 1800 : 160)}`) }); if (segment.length) segments.push(segment); return segments })
function exportHistory() { downloadText("fastiot-prototype-history.csv", `${historyColumns.value.join(',')}\n${historyRows.value.map(row => row.join(",")).join("\n")}`, "text/csv;charset=utf-8"); toast("已导出 30 行预览数据") }
watch(() => props.initialHistory, value => { if (value) openHistory() }, { immediate: true })
onMounted(() => applyAppearanceDocument(appearance.value))
</script>
