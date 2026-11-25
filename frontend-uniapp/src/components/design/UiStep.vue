<template>
  <view class="ui-step">
    <view class="step-head"><b>第 5 步 · 配界面</b><span>给这一层选块、绑定对象；保存后客户看到的就是它。</span></view>
    <view class="cfg-bar">
      <span class="chip">{{ selectedName }} · {{ selectedKind }}</span>
      <span class="chip" :class="savedScreen ? 'ok' : 'warn'">{{ sourceText }}</span>
      <span class="src">{{ dirty ? '有未保存改动' : '改动先预览，保存才生效' }}</span>
      <span class="spacer"></span>
      <button class="ghost" :disabled="!supported || !!busy" @click="restoreDefault">恢复默认</button>
      <button class="ghost" :disabled="!supported || !!busy" @click="save(true)">保存并应用到同类层</button>
      <button class="primary" :disabled="!supported || !!busy" @click="save(false)">{{ busy ? '保存中…' : '保存本层' }}</button>
    </view>
    <p v-if="saveStatus" class="save-status">{{ saveStatus }}</p>
    <p v-if="saveError" class="save-error">{{ saveError }}</p>
    <view v-if="!supported" class="empty">通道与点位不是客户画面层，请选择现场、区域、单元或设备。</view>

    <view v-else class="cfg-grid">
      <view class="cfg-list glass">
        <template v-if="!editingBlock">
          <view v-for="slot in slots" :key="slot.key" class="cfg-slot" :data-slot="slot.key">
            <h5>{{ slot.label }}</h5>
            <view v-for="type in draft.slots[slot.key]" :key="type" class="cfg-row" :data-block="type">
              <b>{{ definition(type)?.label }}</b><span class="src">{{ bindingSummary(type) }}</span>
              <button class="ghost" @click="editingBlock = type">配置</button>
              <button class="ghost" aria-label="上移" @click="moveBlock(type, -1)">↑</button>
              <button class="ghost" aria-label="下移" @click="moveBlock(type, 1)">↓</button>
              <button class="ghost" @click="removeBlock(type)">移除</button>
            </view>
            <p v-if="!draft.slots[slot.key].length" class="note">空</p>
          </view>
          <h5>块目录</h5>
          <view class="catalog"><button v-for="block in catalogBlocks" :key="block.type" @click="addBlock(block.type)">＋ {{ block.label }}</button></view>
          <p v-if="!catalogBlocks.length" class="note">没有可添加的块。</p>
          <p class="note">块目录封闭；排序只发生在槽位内，不做自由画布。</p>
        </template>
        <view v-else class="bind-panel">
          <view class="bind-head"><b>配置 · {{ definition(editingBlock)?.label }}</b><span class="src">{{ selectedName }}</span><button class="ghost" @click="editingBlock = ''">×</button></view>
          <template v-if="editingBlock === 'kpi'">
            <h5>指标（4–6 个）</h5>
            <view v-for="(metric, index) in kpiBinding.metrics" :key="index" class="metric-editor">
              <select :aria-label="`指标 ${index + 1} 绑定`" :value="metricKey(metric)" @change="changeMetric(index, inputValue($event))">
                <optgroup label="本层及下级点位"><option v-for="option in pointOptions" :key="option.id" :value="`point:${option.id}`">{{ option.label }}</option></optgroup>
                <optgroup label="自动指标"><option v-for="option in automaticMetrics" :key="option.key" :value="`auto:${option.key}`">{{ option.label }}</option></optgroup>
              </select>
              <input v-model="metric.label" aria-label="指标名称" placeholder="显示名称" />
              <input v-model="metric.target" aria-label="目标值或阈值" placeholder="目标值 / 阈值（可选）" />
              <button class="ghost" :disabled="kpiBinding.metrics.length <= 4" @click="removeMetric(index)">删除</button>
            </view>
            <button class="ghost" :disabled="kpiBinding.metrics.length >= 6" @click="addMetric">＋ 添加指标</button>
          </template>
          <template v-else-if="editingBlock === 'recent'">
            <h5>点位</h5><view class="catalog"><button v-for="option in pointOptions" :key="option.id" class="chip" :class="{ ok: recentBinding.point_ids.includes(option.id) }" @click="toggleListValue(recentBinding.point_ids, option.id)">{{ option.label }}</button></view>
            <h5>列</h5><view class="catalog"><button v-for="column in recentColumns" :key="column.key" class="chip" :class="{ ok: recentBinding.columns.includes(column.key) }" @click="toggleRequiredValue(recentBinding.columns, column.key)">{{ column.label }}</button></view>
            <h5>行数</h5><select v-model.number="recentBinding.rows" aria-label="最近数据行数"><option :value="3">3 行</option><option :value="5">5 行</option><option :value="10">10 行</option></select>
          </template>
          <template v-else-if="editingBlock === 'trend'">
            <h5>序列（1 条）</h5><select v-model="trendBinding.point_id" aria-label="趋势序列"><option value="">未绑定</option><option v-for="option in pointOptions" :key="option.id" :value="option.id">{{ option.label }}</option></select>
            <p class="note">{{ PREVIEW_MODE ? '原型趋势示意；可从客户预览进入历史。' : '预览只显示当前真实值；没有历史序列时显示 —，不生成演示曲线。' }}</p>
          </template>
          <template v-else-if="editingBlock === 'devices'">
            <h5>列（设备列固定）</h5><view class="catalog"><button v-for="column in deviceColumns" :key="column.key" class="chip" :class="{ ok: devicesBinding.columns.includes(column.key) }" @click="toggleRequiredValue(devicesBinding.columns, column.key)">{{ column.label }}</button></view>
          </template>
          <template v-else-if="editingBlock === 'deviceData'">
            <h5>页签</h5><view class="catalog"><button v-for="tab in deviceTabs" :key="tab.key" class="chip" :class="{ ok: deviceDataBinding.tabs.includes(tab.key) }" @click="toggleRequiredValue(deviceDataBinding.tabs, tab.key)">{{ tab.label }}</button></view>
          </template>
          <template v-else-if="editingBlock === 'flow'">
            <h5>次要信息</h5><button class="chip" :class="{ ok: flowBinding.detail }" @click="flowBinding.detail = !flowBinding.detail">{{ flowBinding.detail ? '显示质量与观测时间' : '仅主要值' }}</button>
          </template>
          <p v-else class="note">{{ autoBindingNote }}</p>
          <p class="note">绑定只作用于本层；保存本层后才对客户生效。</p>
          <div class="actions"><button class="ghost" @click="editingBlock = ''">← 返回块列表</button></div>
        </view>
      </view>

      <view class="cfg-preview">
        <view class="preview-caption"><span>客户在这一层会看到：</span><span>{{ dirty ? '未保存草稿 · 实时联动' : '已保存配置 · 实时数据' }}</span></view>
        <view class="cfg-frame customer-preview">
          <view class="cust-head">
            <view><span class="kicker">{{ projectName }} / {{ selectedKind }}</span><h1>{{ selectedName }}</h1></view>
            <view class="cust-head-right"><span class="chip ok">{{ onlineChip }}</span><view class="cust-tools"><span>历史数据</span><span>刷新</span><span>全屏</span></view></view>
          </view>
          <template v-for="type in draft.slots.top" :key="type">
            <view v-if="type === 'status'" class="cust-meta">
              <span>更新于 <b>{{ shortTime(lastSync) }}</b></span><span>通信 <b>{{ communicationText }}</b></span><span>数据质量 <b>{{ qualityPercent }}</b></span><span>采样 <b>—</b></span><span>设备 <b>{{ onlineText }}</b> 在线</span><span class="spacer"></span><span>{{ dirty ? '草稿预览' : '已保存布局' }}</span>
            </view>
            <view v-else-if="type === 'kpi'" class="kpi-row" :style="{ gridTemplateColumns: `repeat(${Math.min(previewMetrics.length, 6)}, minmax(0, 1fr))` }">
              <view v-for="metric in previewMetrics" :key="metric.key" class="kpi"><span>{{ metric.label }}</span><b>{{ metric.value }}<em v-if="metric.unit">{{ metric.unit }}</em></b><small v-if="metric.target">目标 {{ metric.target }}</small></view>
            </view>
          </template>
          <view v-if="hasBlock('alarm') && layerAlarms.length" class="alarm-strip"><b>{{ layerAlarms.length }} 条待处理</b><span>{{ layerAlarms[0].title }}</span><span class="src">{{ shortTime(layerAlarms[0].updated_at) }}</span></view>

          <view class="canvas-grid">
            <view class="canvas-main">
              <template v-for="type in draft.slots.main" :key="type">
                <view v-if="type === 'flow'" class="screen-card">
                  <h5>工艺总览</h5><view class="flow"><template v-for="(slot, index) in flowSlots" :key="slot.row?.node.id || `empty-${index}`"><view class="flow-node" :class="{ pcc: index === 2 }"><span>{{ slot.row?.name || '未映射对象' }}</span><b>{{ slot.metric?.value || '—' }}<em v-if="slot.metric?.unit">{{ slot.metric.unit }}</em></b><small v-if="flowBinding.detail">{{ flowDetail(slot.metric) }}</small></view><i v-if="index < flowSlots.length - 1">→</i></template></view>
                </view>
                <view v-else-if="type === 'children'" class="screen-card"><h5>下级对象 <span>{{ directChildren.length }} 个真实对象</span></h5><view v-if="directChildren.length" class="children-grid"><view v-for="row in directChildren" :key="row.node.id" class="stat"><span>{{ row.kind }}</span><b>{{ row.name }}</b></view></view><view v-else class="empty">这一层没有下级对象。</view></view>
                <view v-else-if="type === 'members'" class="screen-card"><h5>成员 <span>{{ memberRows.length }} 台成员设备</span></h5><div v-if="memberRows.length" class="table-wrap"><table class="mini-table"><thead><tr><th>成员</th><th>类型</th><th>主要值</th><th>通信</th></tr></thead><tbody><tr v-for="row in memberRows" :key="row.node.id"><td>{{ row.name }}</td><td>{{ row.node.type }}</td><td>{{ deviceValue(row.node) }}</td><td>{{ deviceHealth(row.node) }}</td></tr></tbody></table></div><view v-else class="empty">这一单元没有真实成员设备。</view></view>
                <view v-else-if="type === 'deviceData'" class="screen-card"><h5>设备数据</h5><view class="catalog device-tabs"><button v-for="tab in enabledDeviceTabs" :key="tab.key" class="chip" :class="{ ok: deviceTab === tab.key }" @click="deviceTab = tab.key">{{ tab.label }}</button></view><div v-if="deviceTab === 'realtime' && recentValues.length" class="table-wrap"><table class="mini-table"><thead><tr><th>点位</th><th>当前值</th><th>质量</th><th>观测时间</th></tr></thead><tbody><tr v-for="sample in recentValues" :key="sample.point_id"><td>{{ pointName(sample.point_id) }}</td><td>{{ sampleText(sample) }}</td><td>{{ qualityText(sample.quality) }}</td><td>{{ sampleTime(sample) }}</td></tr></tbody></table></div><div v-else-if="deviceTab === 'parameters' && selected?.points.length" class="table-wrap"><table class="mini-table"><thead><tr><th>参数类型</th><th>值</th></tr></thead><tbody><tr v-for="item in selected.points" :key="`${item.type}-${item.key}`"><td>{{ item.type }}</td><td>{{ item.text || item.value || '—' }}</td></tr></tbody></table></div><view v-else class="empty">当前页签没有真实数据。</view></view>
                <view v-else-if="type === 'devices'" class="screen-card">
                  <h5>设备清单 <span>{{ devices.length }} 个真实对象</span></h5><div v-if="devices.length" class="table-wrap"><table class="mini-table"><thead><tr><th>设备</th><th v-for="column in enabledDeviceColumns" :key="column.key">{{ column.label }}</th></tr></thead><tbody><tr v-for="row in devices" :key="row.node.id"><td>{{ row.name }}</td><td v-for="column in enabledDeviceColumns" :key="column.key">{{ deviceColumnValue(row.node, column.key) }}</td></tr></tbody></table></div><view v-else class="empty">这一层还没有设备对象。</view>
                </view>
                <view v-else-if="type === 'recent'" class="screen-card">
                  <h5>最近数据 <span>{{ recentValues.length }} 条当前值</span></h5><div v-if="recentValues.length" class="table-wrap"><table class="mini-table"><thead><tr><th>点位</th><th v-for="column in enabledRecentColumns" :key="column.key">{{ column.label }}</th></tr></thead><tbody><tr v-for="sample in recentValues" :key="sample.point_id"><td>{{ pointName(sample.point_id) }}</td><td v-for="column in enabledRecentColumns" :key="column.key">{{ recentColumnValue(sample, column.key) }}</td></tr></tbody></table></div><view v-else class="empty">当前绑定没有可显示的点值，保留结构并显示 —。</view>
                </view>
              </template>
            </view>

            <view class="canvas-side">
              <template v-for="type in draft.slots.side" :key="type">
                <view v-if="type === 'alarm'" class="screen-card"><h5>告警</h5><view v-for="alarm in layerAlarms.slice(0, 3)" :key="alarm.id" class="alarm-mini"><span><b>{{ alarm.title }}</b><small>{{ alarm.severity }} · {{ shortTime(alarm.updated_at) }}</small></span><span class="chip warn">待处理</span></view><view v-if="!layerAlarms.length" class="empty">这一层没有待处理告警。</view></view>
                <view v-else-if="type === 'today'" class="screen-card"><h5>今日执行</h5><template v-if="PREVIEW_MODE" data-preview="today"><view v-for="run in previewRuns" :key="run[0]" class="alarm-mini"><span><b>{{ run[0] }} · {{ run[1] }}</b><small>{{ run[3] }}</small></span><span class="chip" :class="{ ok: run[2] === '完成' }">{{ run[2] }}</span></view><p class="note">本轮自动化的结果；实际效果以设备回读为准。</p></template><template v-else><view class="alarm-mini"><span><b>执行记录</b><small>实际效果以设备回读为准</small></span><span class="chip">—</span></view><p class="note">后端未提供“今日执行”查询 API，不推算触发、完成或跳过记录。</p></template></view>
                <view v-else-if="type === 'oplog'" class="screen-card"><h5>操作记录</h5><template v-if="PREVIEW_MODE" data-preview="oplog"><view v-for="entry in previewOperations" :key="entry[0]" class="alarm-mini"><span><b>{{ entry[0] }}</b><small>{{ entry[1] }}</small></span><span class="chip ok">{{ entry[2] }}</span></view></template><view v-else class="empty">没有在本预览中读取操作记录；不伪造回读结果。</view></view>
                <view v-else-if="type === 'trend'" class="screen-card"><h5>趋势 · {{ trendMetric?.label || '—' }}</h5><template v-if="PREVIEW_MODE" data-preview="trend"><view class="prototype-chart" role="img" aria-label="原型趋势示意，非实测历史"><i v-for="(height, index) in previewTrend" :key="index" :style="{ height: `${height}%` }" :title="`示意 ${index + 1}：${height}%`" /></view><div class="actions"><button class="ghost" @click="emit('history', selected?.id || '')">看历史</button></div></template><template v-else><view class="trend-empty">{{ trendMetric?.value || '—' }}</view><p class="note">尚未读取历史序列，不生成演示曲线。</p></template></view>
                <view v-else-if="type === 'summary'" class="screen-card"><h5>运行摘要</h5><view class="stat-grid"><view class="stat"><span>今日电量</span><b>—</b></view><view class="stat"><span>运行时长</span><b>—</b></view><view class="stat"><span>通信成功率</span><b>{{ successRate }}</b></view><view class="stat"><span>数据新鲜度</span><b>{{ latestTime }}</b></view></view></view>
              </template>
            </view>
          </view>
        </view>
      </view>
    </view>
    <section class="card glass layer-status-card">
      <view class="panel-head"><h3>哪些层已配</h3><span class="tag">{{ configuredLayerCount }} / {{ configurableLayers.length }}</span></view>
      <button v-for="row in configurableLayers" :key="row.node.id" class="row layer-row" @click="emit('select', row.node.id)"><span>{{ row.name }} <span class="src">{{ row.kind }}</span></span><span class="src">{{ screenStateText(row.node.id) }}</span></button>
    </section>
  </view>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue"
import { PREVIEW_MODE } from "../../api/source"
import { request, ApiError } from "../../api/client"
import { nameOf, point, valueText } from "../../types/api"
import type { EmsAlarm, Node, ScreenConfig, Snapshot, StateValue } from "../../types/api"
import { healthLabel, metricForPoint, shortTime, summarizeSuccessRate } from "../../pages/overview/overview"
import type { QualityStats } from "../../pages/overview/overview"
import { flattenLedger, kindLabel } from "./design"
import { addScreenBlock, automaticMetricLabel, automaticMetrics, defaultScreen, makeScreenDraft, moveScreenBlock, removeScreenBlock, screenBlockDefinition, screenCatalog, screenMetricKey, setScreenMetric, syncScreenPointIDs, toScreenPayload } from "./screen"
import type { ScreenDocument, ScreenLike, ScreenMetric } from "./screen"

const props = withDefaults(defineProps<{ snapshot: Snapshot | null; values: StateValue[]; alarms: EmsAlarm[]; quality: { channels: unknown[]; devices: unknown[] }; screen: ScreenConfig | null; selectedNodeId?: string; lastSync?: string; layerScreens?: Record<string, { screen: ScreenLike | null; error: boolean }> }>(), { selectedNodeId: "", lastSync: "", layerScreens: () => ({}) })
const emit = defineEmits<{ (event: "select", value: string): void; (event: "saved", value: string): void; (event: "history", value: string): void }>()
// Final prototype fixtures; never substituted for live execution or history data.
const runsByProject: Record<string, string[][]> = {
  "教学泵房": [["14:00", "饮水线冲洗", "完成", "回读末端阀 · 30 秒"], ["09:00", "备用泵试转", "完成", "回读 5 秒"], ["07:00", "采暖预循环", "跳过", "回水 36℃，无需预循环"]],
  "储能站 A": [["08:00", "早高峰放电", "完成", "回读 8.0 kW"], ["12:00", "光伏限功率", "跳过", "无弃光"], ["23:00", "谷电充电", "完成", "SOC 76% → 100%"]],
  "批量接入示例": [["08:00", "批量并网", "完成", "24 台 · 22 台回读"], ["12:00", "SOC 校准", "跳过", "未到校准窗口"], ["23:00", "谷电充电", "完成", "SOC 62% → 90%"]],
}
const previewRuns = computed(() => runsByProject[projectName.value] || runsByProject["教学泵房"])
const previewOperations = [["10:42 · 启动", "张工", "已回读"], ["10:40 · 确认报警", "李工", "已接手"], ["09:58 · 下发目标值", "张工", "已回读"]]
const previewTrend = [42, 55, 48, 63, 70, 66, 74, 61, 54, 59, 65, 69]
const slots = [{ key: "top", label: "顶部" }, { key: "main", label: "主区" }, { key: "side", label: "右栏" }] as const
const recentColumns = [{ key: "value", label: "当前值" }, { key: "quality", label: "质量" }, { key: "time", label: "观测时间" }]
const deviceColumns = [{ key: "type", label: "类型" }, { key: "state", label: "状态" }, { key: "value", label: "主要值" }, { key: "communication", label: "通信" }, { key: "updated", label: "更新" }]
const deviceTabs = [{ key: "realtime", label: "实时数据" }, { key: "parameters", label: "设备参数" }]
const nodes = computed(() => props.snapshot?.nodes || [])
const selected = computed(() => nodes.value.find(node => node.id === props.selectedNodeId) || nodes.value.find(node => node.id === props.snapshot?.roots[0]) || null)
const selectedName = computed(() => selected.value ? nameOf(selected.value) : "未选择对象")
const selectedKind = computed(() => selected.value ? kindLabel(selected.value) : "—")
const supported = computed(() => !!selected.value && ["现场", "区域", "单元", "设备"].includes(selectedKind.value))
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
const pointOptions = computed(() => nodes.value.filter(node => layerIds.value.has(node.id) && ["variable", "modbusIo"].includes(node.type)).map(node => ({ id: node.id, label: nameOf(node) })))
const savedScreen = ref<ScreenLike | null>(null)
const draft = ref<ScreenDocument>(defaultScreen("", "", "现场", []))
const baseline = ref("")
const editingBlock = ref("")
const busy = ref("")
const saveStatus = ref("")
const saveError = ref("")
const deviceTab = ref("realtime")
const dirty = computed(() => JSON.stringify(draft.value) !== baseline.value)
watch(draft, () => { if (dirty.value) saveStatus.value = "" }, { deep: true, flush: "sync" })
const sourceText = computed(() => {
  const screen = savedScreen.value as ScreenDocument | null
  if (screen?.inherited_from) return `继承自 ${pointName(screen.inherited_from)}`
  if (screen?.blocks) return `本层已配 · v${screen.version}`
  if (savedScreen.value) return "简单画面已读取 · 可保存为块配置"
  return "默认块 · 本层尚未保存"
})
watch(() => [props.selectedNodeId, props.screen] as const, ([, screen]) => {
  savedScreen.value = screen
  resetDraft()
  editingBlock.value = ""
  saveStatus.value = ""
  saveError.value = ""
}, { immediate: true })
function resetDraft() {
  draft.value = makeScreenDraft(savedScreen.value, selected.value?.id || "", selectedName.value, selectedKind.value, pointOptions.value)
  baseline.value = JSON.stringify(draft.value)
}
function restoreDefault() {
  const version = draft.value.version
  draft.value = defaultScreen(selected.value?.id || "", selectedName.value, selectedKind.value, pointOptions.value)
  draft.value.version = version
  editingBlock.value = ""
  saveStatus.value = "已恢复默认块（尚未保存）"
  saveError.value = ""
}
function definition(type: string) { return screenBlockDefinition(type) }
function block(type: string) { return draft.value.blocks.find(item => item.type === type) }
function hasBlock(type: string) { return !!block(type) }
const catalogBlocks = computed(() => screenCatalog.filter(item => !hasBlock(item.type) && (!("kinds" in item) || item.kinds.includes(selectedKind.value as never))))
function moveBlock(type: string, direction: -1 | 1) { moveScreenBlock(draft.value, type, direction); saveStatus.value = "" }
function addBlock(type: string) { addScreenBlock(draft.value, type, selectedKind.value, pointOptions.value); saveStatus.value = "" }
function removeBlock(type: string) { removeScreenBlock(draft.value, type); editingBlock.value = ""; saveStatus.value = "" }
function bindingSummary(type: string) {
  const binding = block(type)?.binding || {}
  if (type === "kpi") return `${binding.metrics?.length || 0} 个指标`
  if (type === "recent") return `${binding.point_ids?.length || 0} 点 · ${binding.rows || 5} 行`
  if (type === "trend") return pointName(binding.point_id) || "—"
  if (type === "devices") return `${binding.columns?.length || 0} 列`
  if (type === "deviceData") return `${binding.tabs?.length || 0} 个页签`
  if (type === "flow") return binding.detail ? "含次要信息" : "仅主要值"
  return "自动"
}
const kpiBinding = computed(() => block("kpi")!.binding)
const recentBinding = computed(() => block("recent")!.binding)
const trendBinding = computed(() => block("trend")!.binding)
const devicesBinding = computed(() => block("devices")!.binding)
const deviceDataBinding = computed(() => block("deviceData")!.binding)
const flowBinding = computed(() => block("flow")?.binding || { detail: false })
const autoBindingNote = computed(() => ({ status: "自动绑定本层通信、质量与设备状态。", alarm: "自动绑定本层子树的待处理告警。", today: "自动绑定今日执行；无 API 时明确显示缺口。", oplog: "自动绑定设备操作与回读记录；无数据时不伪造。", children: "自动绑定真实下级对象。", members: "自动绑定本单元的真实成员设备。", summary: "自动绑定通信成功率与数据新鲜度。" } as Record<string, string>)[editingBlock.value] || "这一块自动绑定，无需配置。")
function metricKey(metric: ScreenMetric) { return screenMetricKey(metric) }
function changeMetric(index: number, key: string) { setScreenMetric(draft.value, index, key, pointOptions.value); saveStatus.value = "" }
function addMetric() {
  const metrics = kpiBinding.value.metrics as ScreenMetric[]
  if (metrics.length >= 6) return
  const automatic = automaticMetrics.find(item => !metrics.some(metric => metric.metric === item.key)) || automaticMetrics[0]
  metrics.push({ metric: automatic.key, label: automatic.label, target: "" })
}
function removeMetric(index: number) { if (kpiBinding.value.metrics.length > 4) kpiBinding.value.metrics.splice(index, 1); syncScreenPointIDs(draft.value) }
function toggleListValue(list: string[], value: string) { const index = list.indexOf(value); if (index >= 0) list.splice(index, 1); else list.push(value) }
function toggleRequiredValue(list: string[], value: string) { if (list.length === 1 && list[0] === value) return; toggleListValue(list, value) }
function inputValue(event: Event) { return (event.target as HTMLSelectElement).value }

const allRows = computed(() => flattenLedger(props.snapshot))
const configurableLayers = computed(() => allRows.value.filter(row => row.kind !== "通道"))
const configuredLayerCount = computed(() => configurableLayers.value.filter(row => {
  const screen = props.layerScreens[row.node.id]?.screen
  return !!screen && !(screen as ScreenDocument).inherited_from
}).length)
function screenStateText(id: string) {
  const state = props.layerScreens[id]
  if (state?.error) return "读取失败"
  if (!state) return "—"
  const inherited = (state.screen as ScreenDocument | null)?.inherited_from
  if (inherited) return `继承 ${pointName(inherited)}`
  return state.screen ? "本层已配" : "默认块"
}
const layerRows = computed(() => allRows.value.filter(row => layerIds.value.has(row.node.id)))
const directChildren = computed(() => layerRows.value.filter(row => row.node.parent === selected.value?.id))
const memberRows = computed(() => directChildren.value.filter(row => row.kind === "设备"))
const semanticNodes = computed(() => layerRows.value.filter(row => row.node.id !== selected.value?.id).slice(0, 3))
const devices = computed(() => layerRows.value.filter(row => row.kind === "设备"))
const layerValues = computed(() => props.values.filter(sample => layerIds.value.has(sample.point_id) || layerIds.value.has(sample.device_id)))
const recentValues = computed(() => {
  const binding = block("recent")?.binding
  const ids = (binding?.point_ids || []) as string[]
  const rows = (binding?.rows || 5) as number
  return ids.map(id => props.values.find(sample => sample.point_id === id)).filter((sample): sample is StateValue => !!sample).slice(0, rows)
})
const layerAlarms = computed(() => props.alarms.filter(alarm => alarm.active && (selected.value?.id === props.snapshot?.roots[0] || layerIds.value.has(alarm.device_id || "") || layerIds.value.has(alarm.point_id || ""))))
const previewMetrics = computed(() => {
  const configured = (block("kpi")?.binding.metrics || []) as ScreenMetric[]
  return configured.map((metric, index) => {
    if (metric.point_id) {
      const actual = metricForPoint(metric.point_id, props.snapshot, layerValues.value)
      return { key: `point-${metric.point_id}-${index}`, label: metric.label || actual.label, value: actual.value, unit: actual.unit, target: metric.target || "" }
    }
    const automatic = automaticMetricValue(metric.metric || "")
    return { key: `auto-${metric.metric}-${index}`, label: metric.label || automaticMetricLabel(metric.metric), value: automatic.value, unit: automatic.unit, target: metric.target || "" }
  })
})
const pointMetrics = computed(() => {
  const configured = (block("kpi")?.binding.metrics || []) as ScreenMetric[]
  return configured.filter(metric => !!metric.point_id).map(metric => metricForPoint(metric.point_id!, props.snapshot, layerValues.value))
})
const trendMetric = computed(() => {
  const id = block("trend")?.binding.point_id
  return id ? metricForPoint(id, props.snapshot, layerValues.value) : null
})
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
const latestTime = computed(() => {
  const times = layerValues.value.map(sample => sample.source_time || sample.received_time).filter((time): time is string => !!time).sort()
  return times.length ? shortTime(times[times.length - 1]) : "—"
})
const flowSlots = computed(() => Array.from({ length: 3 }, (_, index) => ({ row: semanticNodes.value[index], metric: pointMetrics.value[index] })))
const enabledRecentColumns = computed(() => recentColumns.filter(column => (block("recent")?.binding.columns || []).includes(column.key)))
const enabledDeviceColumns = computed(() => deviceColumns.filter(column => (block("devices")?.binding.columns || []).includes(column.key)))
const enabledDeviceTabs = computed(() => deviceTabs.filter(tab => (block("deviceData")?.binding.tabs || []).includes(tab.key)))
watch(enabledDeviceTabs, tabs => { if (!tabs.some(tab => tab.key === deviceTab.value)) deviceTab.value = tabs[0]?.key || "realtime" }, { immediate: true })
function automaticMetricValue(key: string): { value: string; unit: string } {
  if (key === "online_devices") return { value: onlineText.value, unit: "" }
  if (key === "active_alarms") return { value: String(layerAlarms.value.length), unit: "条" }
  if (key === "communication_success_rate") return { value: successRate.value, unit: "" }
  if (key === "data_freshness") return { value: latestTime.value, unit: "" }
  return { value: "—", unit: key === "today_energy" ? "kWh" : key === "today_supply" ? "m³" : "" }
}
function flowDetail(metric?: ReturnType<typeof metricForPoint>) { return metric ? `${metric.quality} · ${metric.time}` : "未映射实时点位" }
function pointName(id?: string) { if (!id) return ""; const node = nodes.value.find(item => item.id === id); return node ? nameOf(node) : id }
function sampleText(sample: StateValue) { const node = nodes.value.find(item => item.id === sample.point_id); const unit = node ? point(node, "units")?.text : ""; return `${valueText(sample)}${unit ? ` ${unit}` : ""}` }
function qualityText(quality: string) { return ({ good: "有效", stale: "过期", offline: "离线", invalid: "无效", uninitialized: "无值" } as Record<string, string>)[quality] || quality }
function sampleTime(sample: StateValue) { const time = sample.source_time || sample.received_time; return time ? shortTime(time) : "—" }
function deviceSample(node: Node) { const childIds = new Set(nodes.value.filter(item => item.parent === node.id).map(item => item.id)); return props.values.find(sample => sample.device_id === node.id || childIds.has(sample.point_id)) }
function deviceValue(node: Node) { const sample = deviceSample(node); return sample ? sampleText(sample) : "—" }
function deviceHealth(node: Node) { return healthLabel(qualityDevices.value.find(item => item.device_id === node.id)?.health) }
function deviceTime(node: Node) { const sample = deviceSample(node); return sample ? sampleTime(sample) : "—" }
function deviceColumnValue(node: Node, column: string) { if (column === "type") return node.type; if (column === "state") return "—"; if (column === "value") return deviceValue(node); if (column === "communication") return deviceHealth(node); return deviceTime(node) }
function recentColumnValue(sample: StateValue, column: string) { if (column === "value") return sampleText(sample); if (column === "quality") return qualityText(sample.quality); return sampleTime(sample) }
async function save(applyToSame: boolean) {
  if (!selected.value || busy.value) return
  busy.value = applyToSame ? "same" : "save"
  saveStatus.value = ""
  saveError.value = ""
  const layerID = selected.value.id
  const submittedDraft = JSON.stringify(draft.value)
  try {
    const result = await request<{ saved: boolean; applied: number }>(`/ems/screen/${encodeURIComponent(layerID)}`, "PUT", toScreenPayload(draft.value, applyToSame))
    emit("saved", layerID)
    const loaded = await request<ScreenLike>(`/ems/screen/${encodeURIComponent(layerID)}`)
    if (selected.value?.id !== layerID) return
    savedScreen.value = loaded
    const savedDraft = makeScreenDraft(loaded, layerID, selectedName.value, selectedKind.value, pointOptions.value)
    if (JSON.stringify(draft.value) === submittedDraft) {
      draft.value = savedDraft
      baseline.value = JSON.stringify(savedDraft)
      saveStatus.value = applyToSame ? `已保存，并应用到 ${result.applied} 个同类层（含本层）` : "已保存本层；客户总览可读取这份配置"
    } else {
      draft.value.version = savedDraft.version
      baseline.value = JSON.stringify(savedDraft)
      saveStatus.value = applyToSame ? `已保存提交时的配置，并应用到 ${result.applied} 个同类层；后续修改仍未保存` : "已保存提交时的配置；后续修改仍未保存"
    }
  } catch (error) {
    if (selected.value?.id === layerID) saveError.value = screenErrorText(error)
  } finally { busy.value = "" }
}
function screenErrorText(error: unknown) {
  const code = error instanceof ApiError ? error.code : ""
  if (code === "version_conflict") return "配置版本已变化，请刷新本层后重新编辑；本次未保存。"
  if (code === "invalid_binding") return "绑定不在本层范围或配置不完整，请检查点位与指标。"
  if (["invalid_screen", "invalid_slots", "invalid_block", "invalid_version"].includes(code)) return `后端拒绝了这份画面配置：${code}`
  return error instanceof Error ? error.message : String(error)
}
</script>

<style scoped>
.ui-step { min-width: 0; }
.step-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; margin-bottom: 12px; }
.step-head b { font-size: 16px; }
.step-head span, .src, .note { color: var(--faint); font-size: 12px; }
.cfg-bar { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 12px; }
.cfg-bar .chip { min-height: 33px; height: 33px; }
.spacer { flex: 1; }
.save-status, .save-error { margin: -4px 0 10px; font-size: 12px; }
.save-status { color: var(--mint); }
.save-error { color: var(--amber); }
.cfg-grid { display: grid; grid-template-columns: 290px minmax(0, 1fr); gap: 12px; align-items: start; }
.cfg-list { min-width: 0; max-height: 720px; overflow: auto; padding: 10px 12px; border-radius: 12px; }
.cfg-list h5 { margin: 10px 4px 6px; color: var(--faint); font-size: 11px; letter-spacing: .08em; }
.cfg-slot:first-child h5 { margin-top: 2px; }
.cfg-row { display: flex; align-items: center; gap: 5px; padding: 5px 2px; border-bottom: 1px dashed #eef2f5; font-size: 12.5px; }
.cfg-row b { flex: none; font-weight: 600; }
.cfg-row .src { flex: 1; min-width: 40px; font-size: 10.5px; text-align: right; }
.cfg-row uni-button, .catalog uni-button, .actions uni-button, .metric-editor uni-button { flex: none; min-height: 25px; margin: 0; padding: 2px 7px; font-size: 11px; }
.catalog { display: flex; flex-wrap: wrap; gap: 4px; }
.catalog span, .catalog button { border: 1px solid var(--glass-border); border-radius: 999px; padding: 3px 7px; background: rgba(255,255,255,.4); color: var(--muted); font-size: 10px; }
.note { line-height: 1.7; }
.bind-head { display: flex; align-items: center; gap: 8px; margin: 0 2px 8px; font-size: 13px; }
.bind-head .src { font-size: 11px; }
.bind-head uni-button { flex: none; min-height: 0; margin: 0 0 0 auto; border: 0; padding: 0; background: none; color: var(--faint); font-size: 17px; }
.metric-editor { display: grid; grid-template-columns: 1fr; gap: 4px; margin-bottom: 8px; border-bottom: 1px dashed var(--line); padding-bottom: 8px; }
.bind-panel select, .bind-panel input { width: 100%; min-height: 31px; border: 1px solid var(--glass-border); border-radius: 7px; padding: 5px 7px; background: rgba(255,255,255,.58); font-size: 11px; }
.actions { margin-top: 10px; }
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
.alarm-strip { display: flex; align-items: center; gap: 10px; margin-bottom: 10px; border: 1px solid rgba(238,221,185,.75); border-radius: 10px; padding: 7px 12px; background: rgba(255,244,228,.68); color: var(--amber); font-size: 12px; }
.kpi-row { display: grid; grid-template-columns: repeat(6,minmax(0,1fr)); gap: 6px; margin-bottom: 12px; }
.kpi { min-width: 0; min-height: 61px; border: 1px solid rgba(255,255,255,.74); border-radius: 12px; padding: 7px 8px; background: rgba(255,255,255,.44); box-shadow: 0 8px 20px rgba(45,83,110,.06); }
.kpi span { display: block; color: var(--faint); font-size: 9.5px; }
.kpi b { display: block; margin-top: 4px; font-size: 15px; font-variant-numeric: tabular-nums; }
.kpi small { display: block; color: var(--faint); font-size: 9px; }
.kpi em, .flow-node em { margin-left: 3px; color: var(--faint); font-size: 9px; font-style: normal; font-weight: 400; }
.canvas-grid { display: grid; grid-template-columns: minmax(0,1.5fr) minmax(180px,1fr); gap: 8px; align-items: start; }
.canvas-main, .canvas-side { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.screen-card { min-width: 0; border: 1px solid rgba(255,255,255,.74); border-radius: 14px; padding: 11px; background: rgba(255,255,255,.44); box-shadow: 0 10px 28px rgba(45,83,110,.09); }
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
.prototype-chart { display: flex; align-items: flex-end; gap: 5px; height: 96px; padding: 6px 0; }
.prototype-chart i { flex: 1; border-radius: 4px 4px 0 0; background: linear-gradient(0deg, #0c7d84, #79bdba); }
.trend-empty { display: grid; place-items: center; height: 54px; border-radius: 8px; background: rgba(255,255,255,.28); color: var(--faint); }
.stat-grid, .children-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
.stat { border: 1px solid rgba(255,255,255,.72); border-radius: 10px; padding: 8px 10px; background: rgba(255,255,255,.42); }
.stat span { display: block; color: var(--faint); font-size: 10.5px; }
.stat b { display: block; margin-top: 3px; font-size: 15px; }
.device-tabs { margin-bottom: 8px; }
.empty { padding: 10px 4px; font-size: 11px; }
.layer-status-card { margin-top: 12px; padding: 16px; }
.layer-status-card .panel-head { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; }
.layer-status-card h3 { margin: 0; font-size: 17px; }
.layer-row { width: 100%; border: 0; background: none; text-align: left; cursor: pointer; }
@media (max-width: 760px) {
  .cfg-grid { grid-template-columns: 1fr; }
  .preview-caption, .cust-head { align-items: flex-start; flex-direction: column; height: auto; }
  .cust-head-right { align-items: flex-start; text-align: left; }
  .kpi-row { grid-template-columns: repeat(2,minmax(0,1fr)) !important; }
  .canvas-grid { grid-template-columns: 1fr; }
  .canvas-side { grid-template-columns: 1fr; }
  .cfg-bar .spacer { display: none; }
}
</style>
