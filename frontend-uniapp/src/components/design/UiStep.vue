<template>
  <view class="ui-step">
    <view class="step-head"><b>第 5 步 · 配界面</b><span>给这一层选块、绑定对象；保存后客户看到的就是它。</span></view>
    <view class="cfg-bar">
      <span class="chip">{{ selectedName }} · {{ selectedKind }}</span>
      <span class="chip" :class="savedScreen ? 'ok' : 'warn'">{{ sourceText }}</span>
      <span class="src">{{ dirty ? '有未保存改动' : '改动先预览，保存才生效' }}</span>
      <span class="spacer"></span>
      <select v-model.number="draft.columns" aria-label="主区列数"><option :value="1">1 列布局</option><option :value="2">2 列布局</option></select>
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
                <option v-if="metric.point_id && !pointOptions.some(point => point.id === metric.point_id)" :value="`point:${metric.point_id}`">未绑定或不在本层范围</option>
                <optgroup label="本层及下级点位"><option v-for="option in pointOptions" :key="option.id" :value="`point:${option.id}`">{{ option.label }}{{ option.detail ? ` · ${option.detail}` : '' }}</option></optgroup>
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
          <h5>列宽</h5><select :value="block(editingBlock)?.span" aria-label="块列宽" @change="setSpan(inputValue($event))"><option :value="1">1 列</option><option :value="2">2 列</option></select>
          <p class="note">绑定只作用于本层；保存本层后才对客户生效。</p>
          <div class="actions"><button class="ghost" @click="editingBlock = ''">← 返回块列表</button></div>
        </view>
      </view>

      <view class="cfg-preview">
        <view class="preview-caption"><span>客户在这一层会看到：</span><span>{{ dirty ? '未保存草稿 · 实时联动' : '已保存配置 · 实时数据' }}</span></view>
        <view class="cfg-frame customer-preview">
          <view class="cust-head">
            <view><span class="kicker">{{ projectName }} / {{ selectedKind }}</span><h1>{{ selectedName }}</h1></view>
            <view class="cust-head-right"><span class="chip ok">客户画面预览</span><view class="cust-tools"><span>历史数据</span><span>刷新</span><span>全屏</span></view></view>
          </view>
          <ScreenRenderer :document="previewDocument" :snapshot="snapshot" :values="values" :quality="quality" :alarms="alarms" :ledger="ledger" :layer-id="selected?.id || ''" :last-sync="lastSync" @history="emit('history', $event)" />

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
import { projectContext } from "../../stores/projects"
import { PREVIEW_MODE } from "../../api/source"
import { request, ApiError } from "../../api/client"
import { nameOf } from "../../types/api"
import type { EmsAlarm, ScreenConfig, Snapshot, StateValue } from "../../types/api"
import { isDerivedInput } from "./strategy"
import { boundDevicesForLayer, devicePointIds, isBusinessLayer, metricForPoint } from "../../pages/overview/overview"
import type { LedgerDevice } from "../../pages/overview/overview"
import { memberRolesOf } from "./members"
import { flattenLedger, kindLabel } from "./design"
import { addScreenBlock, automaticMetricLabel, automaticMetrics, defaultScreen, makeScreenDraft, moveScreenBlock, removeScreenBlock, screenBlockDefinition, screenCatalog, screenMetricKey, setScreenMetric, syncScreenPointIDs, toScreenPayload } from "./screen"
import type { ScreenDocument, ScreenLike, ScreenMetric } from "./screen"

const props = withDefaults(defineProps<{ snapshot: Snapshot | null; values: StateValue[]; alarms: EmsAlarm[]; quality: { channels: unknown[]; devices: unknown[] }; screen: ScreenConfig | null; selectedNodeId?: string; lastSync?: string; layerScreens?: Record<string, { screen: ScreenLike | null; error: boolean }> }>(), { selectedNodeId: "", lastSync: "", layerScreens: () => ({}) })
const emit = defineEmits<{ (event: "select", value: string): void; (event: "saved", value: string): void; (event: "history", value: string): void }>()
import ScreenRenderer from "./ScreenRenderer.vue"
const slots = [{ key: "top", label: "顶部" }, { key: "main", label: "主区" }, { key: "side", label: "右栏" }] as const
const recentColumns = [{ key: "value", label: "当前值" }, { key: "quality", label: "质量" }, { key: "time", label: "观测时间" }]
const deviceColumns = [{ key: "type", label: "类型" }, { key: "state", label: "状态" }, { key: "value", label: "主要值" }, { key: "communication", label: "通信" }, { key: "updated", label: "更新" }]
const deviceTabs = [{ key: "realtime", label: "实时数据" }, { key: "parameters", label: "设备参数" }]
const nodes = computed(() => props.snapshot?.nodes || [])
const selected = computed(() => nodes.value.find(node => node.id === props.selectedNodeId) || nodes.value.find(node => node.id === props.snapshot?.roots[0]) || null)
const selectedName = computed(() => selected.value ? nameOf(selected.value) : "未选择对象")
const selectedKind = computed(() => selected.value ? kindLabel(selected.value) : "—")
const supported = computed(() => !!selected.value && isBusinessLayer(selected.value))
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
  for (const member of memberRolesOf(selected.value)) if (member.node_id) walk(member.node_id)
  return ids
})
const ledger = ref<LedgerDevice[]>([])
let ledgerSerial = 0
watch(() => props.snapshot?.roots.join('|'), async () => {
  const serial = ++ledgerSerial; ledger.value = []
  try { const result = await request<{ devices: LedgerDevice[] }>("/device-ledger"); if (serial === ledgerSerial) ledger.value = result.devices || [] } catch { /* No fallback device data. */ }
}, { immediate: true })
const pointOptions = computed(() => {
  const physical = new Set(devicePointIds(nodes.value, boundDevicesForLayer(selected.value?.id || '', nodes.value, ledger.value, memberRolesOf(selected.value).flatMap(member => member.node_id ? [member.node_id] : []))))
  return nodes.value.filter(node => physical.has(node.id) || layerIds.value.has(node.id) && (node.type === 'variable' || isDerivedInput(node))).map(node => ({ id: node.id, label: nameOf(node), detail: isDerivedInput(node) ? metricForPoint(node.id, props.snapshot, props.values).quality : '' }))
})
const savedScreen = ref<ScreenLike | null>(null)
const draft = ref<ScreenDocument>(defaultScreen("", "", "现场", []))
const baseline = ref("")
const editingBlock = ref("")
const busy = ref("")
const saveStatus = ref("")
const saveError = ref("")
const dirty = computed(() => JSON.stringify(draft.value) !== baseline.value)
const previewDocument = computed(() => {
  const saved = savedScreen.value as ScreenDocument | null
  return !dirty.value && saved?.inherited_from ? { ...draft.value, inherited_from: saved.inherited_from, version: saved.version || 0 } : draft.value
})
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
function setSpan(value: string) { const item = block(editingBlock.value); if (item) item.span = value === '1' ? 1 : 2 }
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
function pointName(id?: string) { if (!id) return ""; const node = nodes.value.find(item => item.id === id); return node ? nameOf(node) : "未绑定或不在本层范围" }
async function save(applyToSame: boolean) {
  if (!selected.value || busy.value) return
  busy.value = applyToSame ? "same" : "save"
  saveStatus.value = ""
  saveError.value = ""
  const layerID = selected.value.id
  const generation = projectContext.generation
  const active = () => selected.value?.id === layerID && generation === projectContext.generation
  syncScreenPointIDs(draft.value)
  const submittedDraft = JSON.stringify(draft.value)
  try {
    const result = await request<{ saved: boolean; applied: number }>(`/ems/screen/${encodeURIComponent(layerID)}`, "PUT", { ...toScreenPayload(draft.value, applyToSame), target_versions: Object.fromEntries(Object.entries(props.layerScreens).filter(([, state]) => !state.error).map(([id, state]) => [id, (state.screen as ScreenDocument | null)?.inherited_from ? 0 : (state.screen as ScreenDocument | null)?.version || 0])) })
    if (!active()) return
    emit("saved", layerID)
    const loaded = await request<ScreenLike>(`/ems/screen/${encodeURIComponent(layerID)}`)
    if (!active()) return
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
    if (active()) saveError.value = screenErrorText(error)
  } finally { busy.value = "" }
}
function screenErrorText(error: unknown) {
  const code = error instanceof ApiError ? error.code : ""
  if (code === "save_failed") return "保存未完整完成，部分目标可能已写入；请刷新各层确认后重试。"
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
.layer-status-card { margin-top: 12px; padding: 16px; }
.layer-status-card .panel-head { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; }
.layer-status-card h3 { margin: 0; font-size: 17px; }
.layer-row { width: 100%; border: 0; background: none; text-align: left; cursor: pointer; }
@media (max-width: 760px) {
  .cfg-grid { grid-template-columns: 1fr; }
  .preview-caption, .cust-head { align-items: flex-start; flex-direction: column; height: auto; }
  .cust-head-right { align-items: flex-start; text-align: left; }
  .cfg-bar .spacer { display: none; }
}
</style>
