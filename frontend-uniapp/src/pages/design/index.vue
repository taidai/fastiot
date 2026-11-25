<template>
  <view class="page">
    <TopBar active="design" :project="projectName" :meta="PREVIEW_MODE ? '原型预览数据 · 操作仅保存在当前会话' : '按步骤牵引，不使用演示配置'" :pending="pendingAlarms.length" :alpha="42"
      show-ems :ems-installed="PREVIEW_MODE ? previewWorkspace.emsEnabled : emsInstalled" :ems-busy="industryBusy" @project-change="changeProject" @new-project="newPreviewProject" @import-project="navigate('delivery')" @toggle-ems="toggleEms" @navigate="navigate" @bell="alarmOpen = true" @alpha="() => {}" />
    <view v-if="!PREVIEW_MODE && session.error" class="error">{{ session.error }}</view>
    <IndustryGate v-if="industryBlocked" />
    <template v-else>
    <view class="statebar">
      <span>项目 <b>{{ projectName }}</b></span><span>工作台 <b>设计</b></span><span>页面 <b>{{ activeTool || '台账树' }}</b></span><span v-if="selectedRow">选中 <b>{{ selectedRow.name }}</b> · {{ selectedRow.kind }}</span><span>EMS <b>{{ emsInstalled === null ? "—" : emsInstalled ? "已启用" : "未启用" }}</b></span>
    </view>
    <view class="layout">
      <aside class="side design-ledger glass">
        <p v-if="dependencyBlocked" class="note">此项目依赖 EMS，未启用时台账不可打开。</p>
        <view v-else class="tree">
          <view class="tree-title"><span>{{ projectName }}</span><button class="tag" title="在站点下新建" aria-label="在站点下新建" :disabled="!canConfigure || !rootNode" @click="rootNode && openNewNode(rootNode.id, 'area')">＋</button></view>
          <button v-for="row in businessRows" :key="row.node.id" class="node" :class="{ active: session.layer === row.node.id }"
            :style="{ paddingLeft: `${8 + row.depth * 12}px` }" @click="selectNode(row.node.id)">
            <text class="node-kind" :class="kindClass(row.kind)">{{ row.kind === '单元' ? '业务单元' : row.kind }}</text><text class="name">{{ row.name }}</text><text class="count" :class="{ unbound: row.kind === '设备' && !boundIDs.has(row.node.id) }">{{ treeSummary(row) }}</text>
          </button>
          <view v-if="!businessRows.length" class="empty">没有真实业务对象。</view>
        </view>
      </aside>

      <main class="stage">
        <section v-if="dependencyBlocked" class="card glass step-card dependency-gate"><h3>项目依赖未满足</h3><p>此项目依赖 EMS，未启用时台账不可打开。</p><button class="primary" @click="toggleEms">启用 EMS</button></section>
        <PrototypeTools v-else-if="activeTool" :tool="activeTool" :snapshot="session.snapshot" :selected-node-id="session.layer" :read-only="!canConfigure" @back="activeTool = ''" />
        <template v-else>
        <view class="stepper-wrap">
          <view class="stepper">
            <template v-for="(step, index) in steps" :key="step.key">
              <button class="step" :class="{ active: current === step.key, done: step.done }" @click="current = step.key">
                <i>{{ step.done ? '✓' : index + 1 }}</i>{{ step.label }}
              </button><em v-if="index < steps.length - 1">›</em>
            </template>
          </view>
        </view>

        <template v-if="current === 'site'">
          <view class="step-head"><b>第 1 步 · 建现场</b><span>先确定这是哪个现场，后面所有对象都挂在它下面。</span></view>
          <section class="card glass step-card">
            <view class="row"><span>项目</span><span>{{ projectName }}</span></view>
            <view class="row"><span>现场</span><span>{{ rootNode ? nameOf(rootNode) : '—' }}</span></view>
            <h5 class="section-title">从哪种骨架开始</h5>
            <view class="tpl-grid">
              <button v-for="template in siteTemplates" :key="template.id" class="tpl-card" :class="{ active: selectedTemplate === template.id }" @click="selectedTemplate = template.id">
                <b>{{ template.name }}</b><span>{{ template.description }}</span><small>{{ templateMeta(template) }}</small>
              </button>
            </view>
            <template v-if="siteTemplateOutline.length">
              <h5 class="section-title">骨架预览</h5>
              <view class="outline template-outline">
                <view v-for="(row, index) in siteTemplateOutline" :key="index" class="row" :style="{ paddingLeft: `${row.depth * 16}px` }"><span><span class="node-kind" :class="kindClass(typeLabel(row.type))">{{ typeLabel(row.type) }}</span>{{ row.name }}</span></view>
              </view>
              <p class="note">模板只带结构与默认策略，不带地址、凭据与运行数据。</p>
            </template>
            <p v-else class="note">空白项目从第 2 步开始，一个对象一个对象地搭。</p>
            <div class="actions"><input v-model="siteName" class="site-name" placeholder="新现场名称" :disabled="!canConfigure || actionBusy !== ''"><button class="primary" :disabled="!canConfigure || !siteName.trim() || !selectedTemplate || actionBusy !== ''" @click="createSite">{{ actionBusy === 'site' ? '创建中…' : createSiteButtonText }}</button><button class="ghost" @click="navigate('delivery')">导入项目包</button></div>
            <p v-if="modelError" class="error" role="alert">{{ modelError }}</p><p v-if="modelNotice" class="notice" role="status">{{ modelNotice }}</p>
            <p v-if="!canConfigure" class="note">当前账号没有配置权限，模板、新建与复制已停用。</p>
          </section>
        </template>

        <template v-else-if="current === 'tree'">
          <p v-if="modelError" class="error" role="alert">{{ modelError }}</p><p v-if="modelNotice" class="notice" role="status">{{ modelNotice }}</p>
          <section v-if="newNodeParent" class="card glass step-card">
            <view class="panel-head"><h3>在「{{ nodeName(newNodeParent) }}」下新建</h3><span class="tag">类型受后端层级约束</span></view>
            <view class="create-line"><select v-model="newNodeType" :disabled="actionBusy !== ''"><option v-for="type in allowedTypes" :key="type" :value="type">{{ typeLabel(type) }}</option></select><input v-model="newNodeName" placeholder="对象名称" :disabled="actionBusy !== ''"><button class="primary" :disabled="!newNodeName.trim() || actionBusy !== ''" @click="createNode">{{ actionBusy === 'node' ? '创建中…' : '创建' }}</button><button class="ghost" @click="newNodeParent = ''">取消</button></view>
          </section>
          <template v-else>
            <view class="step-head"><b>第 2 步 · 搭台账</b><span>按区域、单元、设备把现场搭出来；设备可以先占位。</span></view>
            <section class="card glass step-card">
              <view class="panel-head"><h3>{{ projectName }}</h3><span class="tag">区域 {{ summary.areas }} · 单元 {{ summary.units }} · 设备 {{ summary.devices }}</span></view>
              <view class="check-strip">
                <button class="chip" :class="accessSummary.devices && accessSummary.bound === accessSummary.devices ? 'ok' : 'warn'" @click="current = 'access'">{{ accessSummary.devices - accessSummary.bound > 0 ? `${accessSummary.devices - accessSummary.bound} 台设备未绑定` : accessSummary.devices ? '设备全部已绑定' : '还没有设备' }}</button>
                <button class="chip" :class="everyLayerHasScreen ? 'ok' : ''" @click="current = 'ui'">{{ layerScreenSummary }}</button>
              </view>
              <div class="actions compact-actions"><button class="ghost" :disabled="!canConfigure || !allowedTypes.includes('area')" @click="openNewNode(session.layer, 'area')">＋ 区域</button><button class="ghost" :disabled="!canConfigure || !allowedTypes.some(type => type === 'unit' || type === 'energyUnit')" @click="openNewNode(session.layer, allowedTypes.includes('energyUnit') ? 'energyUnit' : 'unit')">＋ 业务单元</button><button class="ghost" :disabled="!canConfigure || !allowedTypes.includes('device')" @click="openNewNode(session.layer, 'device')">＋ 设备</button></div>
              <view class="search-row tree-search"><input v-model="treeQuery" placeholder="搜索对象 / 设备 / 点位" /><span class="src">{{ treeQuery.trim() ? `找到 ${filteredTree.length} 个对象` : `共 ${businessRows.length} 个对象` }}</span></view>
              <view v-if="activeBatchSelection.length" class="batch-bar"><span>已勾选 <b>{{ activeBatchSelection.length }}</b> 台设备</span><span class="spacer"></span><button class="primary" @click="openBatchAccess">去批量接入 →</button></view>
              <h5 class="section-title">台账</h5>
              <div class="table-wrap"><table class="mini-table ledger-table ledger-worktable"><thead><tr><th></th><th>对象</th><th>子对象</th><th>设备</th><th>未绑定</th><th>策略</th><th>总览</th><th v-if="treeQuery.trim()">命中</th><th></th></tr></thead><tbody>
                <tr v-for="row in filteredTree" :key="row.node.id">
                  <td><checkbox v-if="row.node.type === 'device' && row.node.parent !== 'root' && !boundIDs.has(row.node.id)" :checked="activeBatchSelection.includes(row.node.id)" :disabled="!canConfigure" aria-label="选择批量接入设备" @click="toggleBatch(row.node.id)" /></td>
                  <td><span :style="{ paddingLeft: `${row.depth * 14}px` }"><span class="node-kind" :class="kindClass(row.kind)">{{ row.kind === '单元' ? '业务单元' : row.kind }}</span>{{ row.name }}</span></td>
                  <td>{{ childCount(row.node.id) || '—' }}</td><td>{{ row.kind === '设备' ? (boundIDs.has(row.node.id) ? 1 : '—') : descendantDeviceCount(row.node.id) }}</td>
                  <td><span v-if="descendantUnboundCount(row.node.id)" class="chip warn">{{ descendantUnboundCount(row.node.id) }}</span><template v-else>0</template></td>
                  <td class="src">{{ strategyLabel(row) }}</td><td class="src">{{ screenText(row.node.id) }}</td><td v-if="treeQuery.trim()" class="src">{{ matchReason(row, treeQuery) }}</td>
                  <td class="row-actions"><button class="ghost" @click="selectNode(row.node.id)">选中</button><button v-if="allowedTypesForRow(row).length" class="ghost" :disabled="!canConfigure" @click="openNewNode(row.node.id, allowedTypesForRow(row)[0])">＋</button><button v-if="row.node.parent !== 'root' && row.node.id !== rootNode?.id" class="ghost" :disabled="!canConfigure || actionBusy !== ''" @click="copySubtree(row.node.id, row.node.parent, row.name)">复制</button></td>
                </tr>
                <tr v-if="!filteredTree.length"><td colspan="9" class="empty">没有匹配的对象；换个词，或清空搜索。</td></tr>
              </tbody></table></div>
              <p class="note">设备先建成占位；绑定通道与点位之前，它不产生数据，也不出现在客户画面里。勾选多台未绑定设备可去第 3 步批量接入。</p>
            </section>
            <section class="card glass step-card template-library">
              <view class="panel-head"><h3>模板库</h3><span class="tag">点表 · 策略 · 画面</span></view>
              <view class="tpl-grid"><button v-for="template in reusableSiteTemplates" :key="template.id" class="tpl-card" @click="chooseTreeTemplate(template.id)"><b>{{ template.name }}</b><span>{{ template.description }}</span><small>{{ templateMeta(template) }}</small></button></view>
              <p class="note">模板只带结构与默认策略，不带地址与凭据；建出来后仍要在本现场重新绑定。</p>
              <div class="actions"><button class="primary" @click="current = 'access'">下一步：接设备 →</button></div>
            </section>
          </template>
        </template>

        <AccessStep v-else-if="current === 'access'" :snapshot="session.snapshot" :values="session.values" :selected-node-id="session.layer" :batch-node-ids="activeBatchSelection"
          @refresh="refreshDesign" @status="accessSummary = $event" @navigate="current = $event" @tool="activeTool = $event" />

        <StrategyStep v-else-if="current === 'strategy'" :snapshot="session.snapshot" :values="session.values" :rules="rules" :selected-node-id="session.layer" :read-only="!canConfigure" @refresh="refreshDesign" @navigate="current = $event" @tool="activeTool = $event" />

        <UiStep v-else-if="current === 'ui'" :snapshot="session.snapshot" :values="session.values" :alarms="session.alarms"
          :quality="session.quality" :screen="screen" :selected-node-id="session.layer" :last-sync="session.lastSync" :layer-screens="layerScreens"
          @select="session.layer = $event" @saved="handleScreenSaved" @history="openPreviewHistory" />

        <DeliveryStep v-else :snapshot="session.snapshot" @navigate="navigate" @status="deliveryDone = $event" @tool="activeTool = $event" />
        <view v-if="PREVIEW_MODE && (current === 'site' || current === 'tree')" class="tool-links">已有资产：<button class="link" @click="activeTool = 'templates'">模板库</button></view>
        </template>
      </main>
    </view>
    <AlarmCenter v-if="alarmOpen" :alarms="session.alarms" @close="alarmOpen = false" @ack="acknowledge" @close-alarm="close" />
    </template>
  </view>
</template>

<script setup lang="ts">
import IndustryGate from '../../components/IndustryGate.vue'
import { industryBlocked } from '../../stores/session'
import { currentProject, projectContext } from '../../stores/projects'
import { onLoad } from "@dcloudio/uni-app"
import { computed, onMounted, onUnmounted, ref, watch } from "vue"
import TopBar from "../../components/TopBar.vue"
import AlarmCenter from "../../components/AlarmCenter.vue"
import AccessStep from "../../components/design/AccessStep.vue"
import StrategyStep from "../../components/design/StrategyStep.vue"
import UiStep from "../../components/design/UiStep.vue"
import DeliveryStep from "../../components/design/DeliveryStep.vue"
import { request, ApiError } from "../../api/client"
import { PREVIEW_MODE } from "../../api/source"
import { previewWorkspace, previewProjects } from '../../api/preview/workspace'
import PrototypeTools from '../../components/design/PrototypeTools.vue'
import { canConfigureAccess } from "../../components/design/access"
import { allowedChildTypes, boundBusinessIDs, flattenLedger, summarizeLedger } from "../../components/design/design"
import { acknowledgeAlarm, closeAlarm, descendantsOf, loadScreen, nameOf, pendingAlarms, refreshAll, requireAuth, session, startPolling, stopPolling } from "../../stores/session"
import { kindOf } from "../../types/api"
import type { ScreenConfig } from "../../types/api"
import { memberRolesOf } from "../../components/design/members"

type TemplateNode = { name: string; type: string; children?: TemplateNode[] }
type SiteTemplate = { id: string; name: string; description: string; nodes: TemplateNode[] }
type LedgerRow = ReturnType<typeof flattenLedger>[number]
type LayerScreen = ScreenConfig & { inherited_from?: string; blocks?: unknown[]; version?: number }
type LayerScreenState = { screen: LayerScreen | null; error: boolean }

const current = ref("site")
onLoad(query => {
  const step = String(query?.step || "")
  if (["site", "tree", "access", "strategy", "ui", "delivery"].includes(step)) current.value = step
})
const activeTool = ref('')
const dependencyBlocked = computed(() => PREVIEW_MODE && !previewWorkspace.emsEnabled && !!previewProjects.find(p => p.id === previewWorkspace.projectId)?.requiresEms)
async function changeProject() {
  activeTool.value = ''; current.value = 'tree'; newNodeParent.value = ''; batchSelection.value = []
  session.layer = previewWorkspace.projectId
  layerScreens.value = {}; screen.value = null
  await refreshDesign()
}
async function newPreviewProject() { await changeProject(); selectedTemplate.value = 'blank'; siteName.value = projectName.value; current.value = 'site' }
function openPreviewHistory() { uni.navigateTo({ url: '/pages/overview/index?history=1' }) }
const siteTemplates = ref<SiteTemplate[]>([])
const selectedTemplate = ref(PREVIEW_MODE ? 'pump-house' : 'blank')
const siteName = ref(PREVIEW_MODE ? previewProjects.find(p => p.id === previewWorkspace.projectId)?.name || '未命名项目' : '')
const actionBusy = ref("")
const modelError = ref("")
const modelNotice = ref("")
const newNodeParent = ref("")
const newNodeType = ref("area")
const newNodeName = ref("")
const batchSelection = ref<string[]>([])
const accessSummary = ref({ channels: 0, devices: 0, bound: 0, verified: 0 })
const deliveryDone = ref(false)
const alarmOpen = ref(false)
const emsInstalled = ref<boolean | null>(null)
const emsVersion = ref("")
const industryBusy = ref(false)
const screen = ref<ScreenConfig | null>(null)
const layerScreens = ref<Record<string, LayerScreenState>>({})
const treeQuery = ref("")
const businessRows = computed(() => flattenLedger(session.snapshot).filter(row => !PREVIEW_MODE || row.kind !== '通道'))
const summary = computed(() => summarizeLedger(session.snapshot?.nodes || []))
const boundIDs = computed(() => boundBusinessIDs(session.snapshot?.nodes || []))
const activeBatchSelection = computed(() => batchSelection.value.filter(id => !boundIDs.value.has(id)))
const rootNode = computed(() => session.snapshot?.nodes.find(node => node.id === session.snapshot?.roots[0]) || null)
const projectName = computed(() => PREVIEW_MODE ? previewProjects.find(p => p.id === previewWorkspace.projectId)?.name || '未命名项目' : currentProject.value?.name || "未选择项目")
const canConfigure = computed(() => canConfigureAccess(session.snapshot))
const selectedNode = computed(() => session.snapshot?.nodes.find(node => node.id === session.layer) || rootNode.value)
const selectedRow = computed(() => businessRows.value.find(row => row.node.id === selectedNode.value?.id) || null)
const allowedTypes = computed(() => selectedNode.value ? allowedChildTypes(selectedNode.value.type, selectedNode.value.parent === "root" || selectedNode.value.type === "siteRoot") : [])
const siteTemplateOutline = computed(() => {
  const template = siteTemplates.value.find(item => item.id === selectedTemplate.value)
  const rows: { name: string; type: string; depth: number }[] = []
  const walk = (nodes: TemplateNode[], depth: number) => nodes.forEach(node => { rows.push({ name: node.name, type: node.type, depth }); walk(node.children || [], depth + 1) })
  walk(template?.nodes || [], 0)
  return rows
})
const rules = computed(() => (session.snapshot?.nodes || []).filter(node => node.type === "decisionTable"))
const filteredTree = computed(() => filterRows(treeQuery.value))
const reusableSiteTemplates = computed(() => siteTemplates.value.filter(template => template.id !== "blank"))
const createSiteButtonText = computed(() => {
  const template = siteTemplates.value.find(item => item.id === selectedTemplate.value)
  return template?.id === "blank" ? "建空白项目 →" : `按「${template?.name || "模板"}」建出来 →`
})
const everyLayerHasScreen = computed(() => screenLayers.value.length > 0 && screenLayers.value.every(row => !!layerScreens.value[row.node.id]?.screen))
const screenLayers = computed(() => businessRows.value.filter(row => row.kind !== "通道"))
const layerScreenSummary = computed(() => {
  const states = screenLayers.value.map(row => layerScreens.value[row.node.id])
  const failed = states.filter(state => state?.error).length
  if (failed) return `${failed} 层画面读取失败`
  if (states.some(state => !state)) return "画面状态读取中…"
  const defaults = states.filter(state => !state?.screen).length
  return defaults ? `${defaults} 层用默认块` : "每一层都已配界面"
})
const steps = computed(() => [
  { key: "site", label: "建现场", done: !!session.snapshot },
  { key: "tree", label: "搭台账", done: businessRows.value.length > 1 },
  { key: "access", label: "接设备", done: accessSummary.value.devices > 0 && accessSummary.value.bound === accessSummary.value.devices },
  { key: "strategy", label: "配策略", done: rules.value.length > 0 },
  { key: "ui", label: "配界面", done: everyLayerHasScreen.value },
  { key: "delivery", label: "交付", done: deliveryDone.value },
])
function filterRows(query: string) {
  const q = query.trim().toLowerCase()
  return q ? businessRows.value.filter(row => !!matchReason(row, q)) : businessRows.value
}
function matchReason(row: LedgerRow, query: string) {
  const q = query.trim().toLowerCase()
  if (!q) return ""
  if (`${row.name} ${row.kind} ${row.node.type}`.toLowerCase().includes(q)) return "名称"
  if (row.kind === "设备" && modelSearchText(row.node.id).includes(q)) return "站号 / 型号"
  const pointMatch = descendantsOf(row.node.id).some(node => ["variable", "modbusIo"].includes(node.type) && node.points.some(item => item.type === "description" && (item.tombstone || 0) % 2 === 0 && item.text?.toLowerCase().includes(q)))
  return pointMatch ? "点位" : ""
}
function modelSearchText(id: string) {
  const parts: string[] = []
  for (const node of session.snapshot?.nodes || []) {
    if (node.type !== "modbus") continue
    for (const item of node.points) {
      if (item.type !== "easyemsUnitModel" || (item.tombstone || 0) % 2 !== 0 || !item.text) continue
      try {
        if ((JSON.parse(item.text) as { business_node_id?: string }).business_node_id === id) parts.push(item.key, item.text)
      } catch { /* Malformed models cannot supply search metadata. */ }
    }
  }
  return parts.join(" ").toLowerCase()
}
function childCount(id: string) { return businessRows.value.filter(row => row.node.parent === id).length }
function descendantDeviceCount(id: string) { return descendantsOf(id).filter(node => kindOf(node.type) === "device").length }
function descendantUnboundCount(id: string) { return descendantsOf(id).filter(node => kindOf(node.type) === "device" && !boundIDs.value.has(node.id)).length }
function kindClass(kind: string) { return kind === "单元" ? "unit" : kind === "设备" ? "device" : "" }
function treeSummary(row: (typeof businessRows.value)[number]) {
  if (row.kind === "设备") return boundIDs.value.has(row.node.id) ? "已绑定" : "未绑定"
  if (row.kind === "单元") return `${memberRolesOf(row.node).length} 个角色`
  const total = descendantDeviceCount(row.node.id)
  const unbound = descendantUnboundCount(row.node.id)
  return `设备 ${total}${unbound ? ` · 未绑定 ${unbound}` : ""}`
}
function selectNode(id: string) {
  session.layer = id
  const row = businessRows.value.find(item => item.node.id === id)
  if ((current.value === "site" || current.value === "tree") && (row?.kind === "设备" || row?.kind === "单元")) current.value = "access"
}
function allowedTypesForRow(row: LedgerRow) { return allowedChildTypes(row.node.type, row.node.parent === "root" || row.node.type === "siteRoot") }
function strategyLabel(row: LedgerRow) {
  const nodes = session.snapshot?.nodes || []
  if (row.kind === "设备") {
    const parents = new Set(nodes.map(node => node.id))
    let parent = row.node.parent
    while (parents.has(parent)) {
      if (rules.value.some(rule => rule.parent === parent)) return "继承所属层"
      parent = nodes.find(node => node.id === parent)?.parent || ""
    }
    return "—"
  }
  const own = rules.value.filter(rule => rule.parent === row.node.id).length
  if (own) return `决策表 ${own} · 本层`
  const descendants = new Set(descendantsOf(row.node.id).map(node => node.id))
  const inherited = rules.value.filter(rule => descendants.has(rule.parent)).length
  return inherited ? `决策表 ${inherited} · 含下级` : "—"
}
function screenText(id: string) {
  if (businessRows.value.find(row => row.node.id === id)?.kind === "通道") return "—"
  const state = layerScreens.value[id]
  if (state?.error) return "读取失败"
  if (!state) return "—"
  if (state.screen?.inherited_from) return `继承 ${nodeName(state.screen.inherited_from)}`
  return state.screen ? "本层已配" : "默认块"
}
function templateMeta(template: SiteTemplate) {
  const counts = { areas: 0, units: 0, devices: 0 }
  const walk = (nodes: TemplateNode[]) => nodes.forEach(node => {
    if (["area", "group", "plant"].includes(node.type)) counts.areas++
    if (["unit", "energyUnit"].includes(node.type)) counts.units++
    if (node.type === "device") counts.devices++
    walk(node.children || [])
  })
  walk(template.nodes)
  return template.nodes.length ? `${counts.areas} 区域 · ${counts.units} 单元 · ${counts.devices} 设备` : "0 对象"
}
function chooseTreeTemplate(id: string) { selectedTemplate.value = id; current.value = "site" }
function toggleBatch(id: string) { batchSelection.value = batchSelection.value.includes(id) ? batchSelection.value.filter(item => item !== id) : [...batchSelection.value, id] }
function openBatchAccess() { current.value = "access" }
function nodeName(id: string) { const node = session.snapshot?.nodes.find(item => item.id === id); return node ? nameOf(node) : id }
function typeLabel(type: string) { return ({ site: "现场", siteRoot: "现场", plant: "电站", area: "区域", group: "区域", unit: "业务单元", energyUnit: "能源单元", device: "设备" } as Record<string, string>)[type] || type }
async function loadSiteTemplates() {
  try {
    const result = await request<{ site_templates: SiteTemplate[] }>("/templates")
    siteTemplates.value = result.site_templates || []
    if (PREVIEW_MODE) siteTemplates.value.sort((a, b) => ['pump-house', 'storage', 'blank'].indexOf(a.id) - ['pump-house', 'storage', 'blank'].indexOf(b.id))
  } catch (cause) { modelError.value = cause instanceof Error ? `现场模板读取失败：${cause.message}` : "现场模板读取失败" }
}
async function loadIndustryStatus() {
  try {
    const result = await request<{ packages: { id: string; version: string; installed: boolean }[] }>("/industry/packages")
    const ems = result.packages?.find(item => item.id === "ems")
    emsInstalled.value = ems?.installed || false
    emsVersion.value = ems?.version || ""
  } catch { emsInstalled.value = null }
}
async function toggleEms() {
  const installed = PREVIEW_MODE ? previewWorkspace.emsEnabled : emsInstalled.value
  if (installed === null || industryBusy.value) return
  if (!installed && !emsVersion.value) { uni.showToast({ title: "未读取到 EMS 行业包版本", icon: "none" }); return }
  industryBusy.value = true
  try {
    if (installed) await request("/industry/packages/ems", "DELETE")
    else await request("/industry/packages", "POST", { id: "ems", version: emsVersion.value })
    await Promise.all([loadIndustryStatus(), refreshAll()])
    uni.showToast({ title: installed ? "EMS 已停用；配置数据保留" : "EMS 已启用", icon: "none" })
  } catch (cause) {
    uni.showToast({ title: cause instanceof Error ? cause.message : String(cause), icon: "none" })
  } finally { industryBusy.value = false }
}
function openNewNode(parent: string, preferred: string) {
  session.layer = parent
  newNodeParent.value = parent
  newNodeType.value = preferred
  newNodeName.value = ""
  modelError.value = ""
  modelNotice.value = ""
}
async function createSite() {
  if (!rootNode.value || !siteName.value.trim()) return
  actionBusy.value = "site"
  modelError.value = ""
  modelNotice.value = ""
  try {
    const result = await request<{ id: string; created: number }>("/nodes", "POST", { parent: rootNode.value.id, description: siteName.value.trim(), site_template: selectedTemplate.value })
    await refreshAll()
    session.layer = result.id
    modelNotice.value = `现场已创建，共 ${result.created} 个真实对象；设备仍未绑定`
    siteName.value = ""
    current.value = "tree"
  } catch (cause) { modelError.value = cause instanceof Error ? cause.message : "现场创建失败" } finally { actionBusy.value = "" }
}
async function createNode() {
  if (!newNodeName.value.trim()) return
  actionBusy.value = "node"
  modelError.value = ""
  modelNotice.value = ""
  try {
    const result = await request<{ id: string }>("/nodes", "POST", { parent: newNodeParent.value, type: newNodeType.value, description: newNodeName.value.trim() })
    await refreshAll()
    session.layer = result.id
    modelNotice.value = `${typeLabel(newNodeType.value)}已创建${newNodeType.value === "device" ? "，当前为未绑定占位" : ""}`
    newNodeParent.value = ""
    newNodeName.value = ""
  } catch (cause) { modelError.value = cause instanceof Error ? cause.message : "对象创建失败" } finally { actionBusy.value = "" }
}
async function copySubtree(id: string, parent: string, name: string) {
  actionBusy.value = "copy"
  modelError.value = ""
  modelNotice.value = ""
  try {
    const result = await request<{ id: string; created: number; skipped: number; confirm: string[] }>(`/nodes/${encodeURIComponent(id)}/copy`, "POST", { parent, description: `${name} 副本` })
    await refreshAll()
    session.layer = result.id
    modelNotice.value = `已复制 ${result.created} 个对象${result.skipped ? `，跳过 ${result.skipped} 个连接/规则对象` : ""}${result.confirm?.length ? `；${result.confirm.length} 个外部成员引用需重新绑定` : ""}`
  } catch (cause) { modelError.value = cause instanceof Error ? cause.message : "子树复制失败" } finally { actionBusy.value = "" }
}
let layerScreenRequest = 0
async function loadLayerScreens() {
  const requestID = ++layerScreenRequest
  const ids = screenLayers.value.map(row => row.node.id)
  const entries = await Promise.all(ids.map(async id => {
    try { return [id, { screen: await request<LayerScreen>(`/ems/screen/${encodeURIComponent(id)}`), error: false }] as const }
    catch (error) {
      if (error instanceof ApiError && error.status === 404) return [id, { screen: null, error: false }] as const
      return [id, { screen: null, error: true }] as const
    }
  }))
  if (requestID !== layerScreenRequest) return
  layerScreens.value = Object.fromEntries(entries)
}
async function handleScreenSaved() {
  await loadLayerScreens()
}
async function refreshDesign() {
  await refreshAll()
  await loadSelectedScreen()
  await loadLayerScreens()
}
let screenRequest = 0
async function loadSelectedScreen() {
  const requestID = ++screenRequest
  const id = session.layer
  const generation = projectContext.generation
  screen.value = null
  const loaded = id ? await loadScreen(id) : null
  if (requestID === screenRequest && id === session.layer && generation === projectContext.generation) screen.value = loaded
}
watch(() => session.layer, () => { void loadSelectedScreen() }, { immediate: true })
watch(() => businessRows.value.map(row => row.node.id).join("|"), () => { void loadLayerScreens() }, { immediate: true })
function navigate(key: string) { if (key !== "design") uni.navigateTo({ url: `/pages/${key}/index` }) }
async function acknowledge(id: string) { await acknowledgeAlarm(id) }
async function close(id: string) { await closeAlarm(id) }
onMounted(() => { if (requireAuth()) { startPolling(); void loadSiteTemplates(); void loadIndustryStatus() } })
onUnmounted(stopPolling)
</script>

<style scoped>
.page { padding: 14px; }
.statebar { display: flex; align-items: center; gap: 10px; height: 24px; padding: 0 18px; color: var(--faint); background: rgba(255,255,255,.24); font-size: 11.5px; line-height: 1.2; }
.statebar b { color: var(--muted); font-weight: 600; }
.layout { display: grid; grid-template-columns: 272px minmax(0,1fr); gap: 14px; margin-top: 14px; }
.design-ledger { align-self: start; min-height: calc(100vh - 120px); padding: 14px 10px 30px; }
.tree { border: 1px solid rgba(255,255,255,.72); border-radius: 12px; padding: 8px; background: rgba(255,255,255,.32); backdrop-filter: blur(12px); }
.tree-title { display: flex; align-items: center; justify-content: space-between; gap: 6px; padding: 8px 8px 4px; color: var(--faint); font-size: 11px; letter-spacing: .08em; }
.tree-title > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tree-title .tag { flex: none; width: auto; min-height: 0; margin: 0; padding: 2px 8px; }
.node { display: block; width: 100%; min-height: 31px; margin: 0; border: 0; border-radius: 8px; padding: 7px 9px; background: transparent; color: var(--muted); text-align: left; font-size: 13px; line-height: 17px; }
.node::after { border: 0; }
.node:hover { background: rgba(255,255,255,.5); }
.node.active { background: rgba(255,255,255,.72); color: var(--accent); box-shadow: inset 2px 0 0 var(--accent); font-weight: 600; }
.node-kind { flex: none; margin-right: 4px; border: 1px solid var(--line); border-radius: 4px; padding: 0 4px; color: var(--faint); background: #eef4f7; font-size: 9.5px; }
.node-kind.unit { border-color: #cfe6dc; background: #e6f3ee; color: var(--mint); }
.node-kind.device { border-color: #d6e2f2; background: #eef3fb; color: #2f6f9f; }
.name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.count { margin-left: 6px; color: var(--faint); font-size: 10.5px; font-weight: 400; }
.unbound { color: var(--amber); }
.stage { min-width: 0; }
.stepper-wrap { position: relative; margin-bottom: 16px; }
.stepper { display: flex; align-items: center; justify-content: flex-start; gap: 4px; flex-wrap: wrap; }
.stepper uni-button { display: flex; flex: 0 0 auto; align-items: center; gap: 6px; width: auto; min-width: 0; min-height: 30px; height: 30px; margin: 0; border: 1px solid var(--glass-border); border-radius: 999px; background: rgba(255,255,255,.48); padding: 5px 11px; color: var(--muted); font-size: 12.5px; line-height: 18px; }
.stepper uni-button i { display: grid; place-items: center; width: 18px; height: 18px; border-radius: 50%; background: #eef4f7; color: var(--faint); font-size: 10px; font-style: normal; }
.stepper uni-button.active { border-color: var(--accent); background: rgba(255,255,255,.8); color: var(--accent); box-shadow: 0 4px 14px rgba(45,83,110,.1); }
.stepper uni-button.active i { background: var(--accent); color: #fff; }
.stepper uni-button.done:not(.active) i { background: #e6f3ee; color: var(--mint); }
.stepper em { margin: 0 2px; color: #c3d0da; font-style: normal; }
.step-card { padding: 16px; }
.step-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; margin-bottom: 12px; }
.step-head b { font-size: 16px; }
.step-head span, .note, .src { color: var(--faint); font-size: 12px; }
.note { line-height: 1.7; }
.section-title { margin: 14px 0 6px; }
.spacer { flex: 1; }
.outline { border: 1px solid var(--line); border-radius: 10px; padding: 4px 10px; background: rgba(255,255,255,.42); }
.outline .row { padding: 4px 0; font-size: 12.5px; }
.panel-head { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 12px; }
.panel-head h3 { margin: 0; font-size: 17px; }
.tag { border: 1px solid var(--line); border-radius: 999px; padding: 2px 8px; color: var(--muted); background: #eef4f7; font-size: 11px; }
.check-strip { display: flex; gap: 6px; flex-wrap: wrap; }
.batch-bar { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-top: 10px; padding: 8px 12px; border: 1px solid #cfe0ea; border-radius: 10px; background: #f1f8fb; font-size: 12.5px; }
.search-row { display: flex; align-items: center; gap: 10px; margin: 12px 0 0; }
.search-row input { flex: 1; width: 100%; max-width: 360px; border: 1px solid rgba(255,255,255,.8); border-radius: 9px; padding: 7px 10px; background: rgba(255,255,255,.58); font-size: 12.5px; }
.compact-actions { margin-top: 10px; }
.mini-table { min-width: 0; }
.mini-table td { vertical-align: middle; }
.row-actions { white-space: nowrap; text-align: right; }
.row-actions .ghost { margin-left: 4px; padding: 3px 7px; font-size: 11px; }
.tpl-grid { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 8px; margin: 8px 0; }
.tpl-card { display: flex; flex-direction: column; gap: 4px; margin: 0; padding: 11px 12px; border: 1px solid rgba(255,255,255,.72); border-radius: 10px; background: linear-gradient(135deg,rgba(255,255,255,.34),rgba(255,255,255,.06)),rgba(255,255,255,.42); box-shadow: 0 8px 22px rgba(45,83,110,.08),inset 0 1px 0 rgba(255,255,255,.75); text-align: left; }
.tpl-card.active { border-color: var(--accent); background: #f1faf9; }
.tpl-card b { font-size: 13px; }
.tpl-card span { color: var(--muted); font-size: 11.5px; line-height: 1.5; }
.tpl-card small { color: var(--faint); font-size: 10.5px; line-height: 1.5; }
.template-outline { margin-bottom: 0; }
.template-library { margin-top: 12px; }
.actions .site-name { flex: 0 1 220px; min-width: 150px; min-height: 35px; border: 1px solid rgba(255,255,255,.8); border-radius: 9px; padding: 7px 10px; background: rgba(255,255,255,.58); font-size: 12.5px; }
.create-line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin: 10px 0; }
.create-line input, .create-line select { flex: 1; min-width: 150px; border: 1px solid var(--glass-border); border-radius: 8px; padding: 8px 10px; background: rgba(255,255,255,.62); }
.editor-panel { margin: 10px 0; padding: 12px; border: 1px solid var(--glass-border); border-radius: 10px; background: rgba(255,255,255,.32); }
@media (max-width: 900px) {
  .layout { grid-template-columns: minmax(0,1fr); }
  .design-ledger { min-height: 0; }
}
@media (max-width: 760px) {
  .layout { display: block; }
  .statebar { height: auto; min-height: 20px; padding: 3px 10px; flex-wrap: wrap; font-size: 10px; }
  .design-ledger { max-height: 280px; overflow: auto; }
  .stage { margin-top: 10px; }
  .stepper { gap: 3px; }
  .stepper uni-button { padding: 4px 8px; font-size: 11px; }
  .stepper em { margin: 0; }
  .tpl-grid { grid-template-columns: 1fr; }
  table, .mini-table { min-width: 560px; }
}
</style>
