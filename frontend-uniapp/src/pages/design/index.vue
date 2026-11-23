<template>
  <view class="page">
    <TopBar active="design" :project="projectName" meta="按步骤牵引，不使用演示配置" :pending="pendingAlarms.length" :alpha="42"
      @navigate="navigate" @bell="alarmOpen = true" @alpha="() => {}" />
    <view class="layout">
      <aside class="design-ledger glass">
        <view class="ledger-project"><span>当前项目</span><b>{{ projectName }}</b></view>
        <view class="search"><input v-model="ledgerQuery" placeholder="搜索对象 / 设备" aria-label="搜索设计台账" /></view>
        <view class="ledger-title">台账 <text>{{ filteredLedger.length }} / {{ businessRows.length }}</text></view>
        <button v-for="row in filteredLedger" :key="row.node.id" class="node" :class="{ active: session.layer === row.node.id }"
          :style="{ paddingLeft: `${9 + row.depth * 12}px` }" @click="session.layer = row.node.id">
          <text class="kind">{{ row.kind }}</text><text class="name">{{ row.name }}</text><text class="count">{{ childCount(row.node.id) || '' }}</text>
        </button>
        <view v-if="!filteredLedger.length" class="empty">没有匹配的真实业务对象。</view>
        <view class="ledger-note">服务端实际对象；点位与规则不作为客户层。</view>
      </aside>

      <main class="stage">
        <view class="stepper">
          <template v-for="(step, index) in steps" :key="step.key">
            <button :class="{ active: current === step.key, done: step.done }" @click="current = step.key">
              <i>{{ step.done && current !== step.key ? '✓' : index + 1 }}</i>{{ step.label }}
            </button><em v-if="index < steps.length - 1">›</em>
          </template>
        </view>

        <section v-if="current === 'site'" class="card glass step-card">
          <view class="step-head"><b>第 1 步 · 建现场</b><span>先确定真实现场，后续对象都挂在它下面。</span></view>
          <view class="row"><span>项目</span><span>{{ projectName }}</span></view>
          <view class="row"><span>现场根对象</span><span>{{ rootNode ? nameOf(rootNode) : '—' }}</span></view>
          <view class="metric-grid">
            <view class="metric"><span>业务对象</span><b>{{ businessRows.length }}</b></view>
            <view class="metric"><span>区域 / 单元</span><b>{{ summary.areas }} / {{ summary.units }}</b></view>
            <view class="metric"><span>设备对象</span><b>{{ summary.devices }}</b></view>
            <view class="metric"><span>决策表</span><b>{{ summary.rules }}</b></view>
          </view>
          <h5>现场骨架 · 服务端实际对象</h5>
          <view class="outline site-outline">
            <view v-for="row in businessRows" :key="row.node.id" class="outline-row" :style="{ paddingLeft: `${row.depth * 16}px` }">
              <span><span class="node-kind">{{ row.kind }}</span>{{ row.name }}</span><span class="src">{{ childCount(row.node.id) }} 个直接子对象</span>
            </view>
          </view>
          <p class="note">当前实例没有通用现场新建接口；可继续检查这份真实骨架，或从交付页导入项目包。</p>
          <div class="actions"><button class="primary" @click="current = 'tree'">下一步：搭台账 →</button><button class="ghost" @click="navigate('delivery')">去导入项目包</button></div>
        </section>

        <section v-else-if="current === 'tree'" class="card glass step-card">
          <view class="step-head"><b>第 2 步 · 搭台账</b><span>按区域、单元、设备检查真实对象与下一步。</span></view>
          <view class="panel-head"><h3>{{ projectName }}</h3><span class="tag">区域 {{ summary.areas }} · 单元 {{ summary.units }} · 设备 {{ summary.devices }}</span></view>
          <view class="check-strip">
            <button class="chip" :class="accessSummary.devices && accessSummary.bound === accessSummary.devices ? 'ok' : 'warn'" @click="current = 'access'">接入台账 {{ accessSummary.bound }} / {{ accessSummary.devices }} 已绑定</button>
            <button class="chip" :class="screen ? 'ok' : ''" @click="current = 'ui'">当前层画面：{{ screen ? '已配置' : '默认结构' }}</button>
          </view>
          <view class="search-row tree-search"><input v-model="treeQuery" placeholder="搜索对象 / 设备" /><span class="src">{{ filteredTree.length }} / {{ businessRows.length }} 个对象</span></view>
          <div class="table-wrap"><table class="mini-table ledger-worktable"><thead><tr><th>对象</th><th>直接子对象</th><th>设备</th><th>属性</th><th>下一步</th></tr></thead><tbody>
            <tr v-for="row in filteredTree" :key="row.node.id">
              <td><span :style="{ paddingLeft: `${row.depth * 14}px` }"><span class="node-kind">{{ row.kind }}</span>{{ row.name }}</span></td>
              <td>{{ childCount(row.node.id) || '—' }}</td><td>{{ descendantDeviceCount(row.node.id) }}</td><td>{{ row.node.points.length }}</td>
              <td class="row-actions"><button class="ghost" @click="session.layer = row.node.id">选中</button><button class="ghost" @click="openAccess(row.node.id)">接设备</button><button class="ghost" @click="openUi(row.node.id)">配界面</button></td>
            </tr>
            <tr v-if="!filteredTree.length"><td colspan="5" class="empty">没有匹配的对象；换个词，或清空搜索。</td></tr>
          </tbody></table></div>
          <p class="note">通用区域、单元、设备新建需要后端模型；本页不伪造新增。可从任一真实对象进入接入或界面预览。</p>
          <div class="actions"><button class="primary" @click="current = 'access'">下一步：接设备 →</button></div>
        </section>

        <section v-else-if="current === 'access'" class="card glass step-card">
          <AccessStep :snapshot="session.snapshot" :values="session.values" :selected-node-id="session.layer"
            @refresh="refreshDesign" @status="accessSummary = $event" />
        </section>

        <section v-else-if="current === 'strategy'" class="card glass step-card">
          <StrategyStep :snapshot="session.snapshot" :rules="rules" :read-only="!canConfigure" @refresh="refreshDesign" />
        </section>

        <UiStep v-else-if="current === 'ui'" :snapshot="session.snapshot" :values="session.values" :alarms="session.alarms"
          :quality="session.quality" :screen="screen" :selected-node-id="session.layer" :last-sync="session.lastSync" />

        <section v-else class="card glass step-card">
          <view class="step-head"><b>第 6 步 · 交付</b><span>把真实工程变成可复现的包。</span></view>
          <view class="row"><span>当前项目</span><span>{{ projectName }}</span></view>
          <view class="row"><span>对象 / 设备</span><span>{{ businessRows.length }} / {{ summary.devices }}</span></view>
          <view class="row"><span>接入 / 策略</span><span>{{ accessSummary.devices }} 台设备 · {{ rules.length }} 张决策表</span></view>
          <p class="note">交付页只调用已有 API；导入不等于部署成功，回退不回放历史命令。</p>
          <div class="actions"><button class="primary" @click="navigate('delivery')">去交付工作台</button></div>
        </section>
      </main>
    </view>
    <AlarmCenter v-if="alarmOpen" :alarms="session.alarms" @close="alarmOpen = false" @ack="acknowledge" @close-alarm="close" />
  </view>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue"
import TopBar from "../../components/TopBar.vue"
import AlarmCenter from "../../components/AlarmCenter.vue"
import AccessStep from "../../components/design/AccessStep.vue"
import StrategyStep from "../../components/design/StrategyStep.vue"
import UiStep from "../../components/design/UiStep.vue"
import { canConfigureAccess } from "../../components/design/access"
import { flattenLedger, summarizeLedger } from "../../components/design/design"
import { acknowledgeAlarm, closeAlarm, descendantsOf, loadScreen, nameOf, pendingAlarms, refreshAll, requireAuth, session, startPolling, stopPolling } from "../../stores/session"
import { kindOf } from "../../types/api"
import type { ScreenConfig } from "../../types/api"

const current = ref("site")
const accessSummary = ref({ channels: 0, devices: 0, bound: 0, verified: 0 })
const alarmOpen = ref(false)
const screen = ref<ScreenConfig | null>(null)
const ledgerQuery = ref("")
const treeQuery = ref("")
const businessRows = computed(() => flattenLedger(session.snapshot))
const summary = computed(() => summarizeLedger(session.snapshot?.nodes || []))
const rootNode = computed(() => session.snapshot?.nodes.find(node => node.id === session.snapshot?.roots[0]) || null)
const projectName = computed(() => rootNode.value ? nameOf(rootNode.value) : "未选择项目")
const canConfigure = computed(() => canConfigureAccess(session.snapshot))
const rules = computed(() => (session.snapshot?.nodes || []).filter(node => node.type === "decisionTable"))
const filteredLedger = computed(() => filterRows(ledgerQuery.value))
const filteredTree = computed(() => filterRows(treeQuery.value))
const steps = computed(() => [
  { key: "site", label: "建现场", done: !!session.snapshot },
  { key: "tree", label: "搭台账", done: businessRows.value.length > 1 },
  { key: "access", label: "接设备", done: accessSummary.value.devices > 0 && accessSummary.value.bound === accessSummary.value.devices && accessSummary.value.verified === accessSummary.value.devices },
  { key: "strategy", label: "配策略", done: rules.value.length > 0 },
  { key: "ui", label: "配界面", done: !!screen.value },
  { key: "delivery", label: "交付", done: false },
])
function filterRows(query: string) {
  const q = query.trim().toLowerCase()
  return q ? businessRows.value.filter(row => `${row.name} ${row.kind} ${row.node.type}`.toLowerCase().includes(q)) : businessRows.value
}
function childCount(id: string) { return businessRows.value.filter(row => row.node.parent === id).length }
function descendantDeviceCount(id: string) { return descendantsOf(id).filter(node => kindOf(node.type) === "device").length }
function openAccess(id: string) { session.layer = id; current.value = "access" }
function openUi(id: string) { session.layer = id; current.value = "ui" }
async function refreshDesign() { await refreshAll() }
watch(() => session.layer, async id => { screen.value = id ? await loadScreen(id) : null }, { immediate: true })
function navigate(key: string) { if (key !== "design") uni.navigateTo({ url: `/pages/${key}/index` }) }
async function acknowledge(id: string) { await acknowledgeAlarm(id) }
async function close(id: string) { await closeAlarm(id) }
onMounted(() => { if (requireAuth()) startPolling() })
onUnmounted(stopPolling)
</script>

<style scoped>
.page { padding: 14px; }
.layout { display: grid; grid-template-columns: 272px minmax(0,1fr); gap: 14px; margin-top: 36px; }
.design-ledger { align-self: start; min-height: calc(100vh - 110px); max-height: calc(100vh - 110px); overflow: auto; padding: 14px 10px 24px; }
.ledger-project { display: flex; flex-direction: column; gap: 4px; padding: 2px 10px 12px; }
.ledger-project span, .ledger-title { color: var(--faint); font-size: 10px; letter-spacing: .08em; }
.ledger-project b { font-size: 13px; }
.search { padding: 0 6px 10px; }
.search input, .search-row input { width: 100%; border: 1px solid var(--glass-border); border-radius: 9px; padding: 8px 10px; background: rgba(255,255,255,.58); font-size: 12px; }
.ledger-title { display: flex; justify-content: space-between; padding: 0 10px 8px; }
.node { display: flex; align-items: center; gap: 5px; width: 100%; min-height: 38px; border: 0; border-left: 3px solid transparent; border-radius: 0; padding: 7px 9px; background: transparent; color: var(--muted); text-align: left; font-size: 12.5px; }
.node::after { border: 0; }
.node.active { border-left-color: var(--accent); background: rgba(255,255,255,.6); color: var(--accent); font-weight: 600; }
.kind, .node-kind { flex: none; margin-right: 4px; border: 1px solid var(--line); border-radius: 4px; padding: 1px 4px; color: var(--faint); background: rgba(255,255,255,.32); font-size: 9px; }
.name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.count { margin-left: auto; color: var(--faint); font-size: 10px; }
.ledger-note { margin: 12px 10px 2px; color: var(--faint); font-size: 10px; line-height: 1.7; }
.stage { min-width: 0; }
.stepper { display: flex; align-items: center; justify-content: flex-start; gap: 4px; flex-wrap: wrap; margin-bottom: 16px; }
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
.metric-grid { display: grid; grid-template-columns: repeat(4,minmax(0,1fr)); gap: 8px; margin: 14px 0; }
.metric { border: 1px solid var(--glass-border); border-radius: 10px; padding: 10px 12px; background: rgba(255,255,255,.42); }
.metric span { display: block; color: var(--faint); font-size: 11px; }
.metric b { display: block; margin-top: 4px; font-size: 18px; }
.step-card h5 { margin: 14px 0 7px; color: var(--faint); font-size: 11px; letter-spacing: .06em; }
.outline { border: 1px solid var(--glass-border); border-radius: 10px; padding: 4px 10px; background: rgba(255,255,255,.32); }
.outline-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; min-height: 32px; border-bottom: 1px dashed var(--line); font-size: 12px; }
.outline-row:last-child { border-bottom: 0; }
.panel-head { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 12px; }
.panel-head h3 { margin: 0; font-size: 17px; }
.tag { border: 1px solid var(--glass-border); border-radius: 999px; padding: 3px 8px; color: var(--muted); background: rgba(255,255,255,.42); font-size: 11px; }
.check-strip { display: flex; gap: 6px; flex-wrap: wrap; }
.search-row { display: flex; align-items: center; gap: 10px; margin: 12px 0; }
.search-row input { flex: 1; max-width: 360px; }
.mini-table { min-width: 620px; }
.mini-table td { vertical-align: middle; }
.row-actions { white-space: nowrap; text-align: right; }
.row-actions .ghost { margin-left: 4px; padding: 3px 7px; font-size: 11px; }
@media (max-width: 1100px) {
  .layout { grid-template-columns: 220px minmax(0,1fr); }
  .stepper { gap: 3px; }
  .stepper uni-button { padding: 5px 8px; font-size: 11px; }
  .stepper em { margin: 0; }
}
@media (max-width: 760px) {
  .layout { display: block; }
  .design-ledger { min-height: 0; max-height: 280px; }
  .stage { margin-top: 10px; }
  .metric-grid { grid-template-columns: repeat(2,minmax(0,1fr)); }
}
</style>
