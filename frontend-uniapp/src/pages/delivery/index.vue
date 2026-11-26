<template>
  <view class="page">
    <TopBar active="delivery" :project="projectName" meta="发布 / 导入 / 回退，不等于部署" :pending="pendingAlarms.length" :alpha="42"
      show-ems :ems-installed="emsInstalled" :ems-busy="busy === 'industry'" @toggle-ems="toggleEms" @project-change="changePreviewProject" @new-project="changePreviewProject" @import-project="current = 'package'; choosePackage()" @navigate="navigate" @bell="alarmOpen = true" @alpha="() => {}" />
    <view v-if="!PREVIEW_MODE && session.error" class="error">{{ session.error }}</view>
    <view class="statebar">
      <span>项目 <b>{{ projectName }}</b></span><span>工作台 <b>交付</b></span><span>页面 <b>{{ currentLabel }}</b></span><span>EMS <b>{{ emsInstalled === null ? "—" : emsInstalled ? "已启用" : "未启用" }}</b></span>
    </view>
    <view class="layout">
      <aside class="side glass">
        <view class="navgroup"><h4>交付资产</h4>
          <button v-for="item in assetTabs" :key="item.key" :class="{ active: current === item.key }" @click="current = item.key">{{ item.label }}</button>
        </view>
        <view class="navgroup"><h4>系统</h4>
          <button v-for="item in systemTabs" :key="item.key" :class="{ active: current === item.key }" @click="current = item.key">{{ item.label }}</button>
        </view>
      </aside>
      <main class="stage">
        <IndustryGate v-if="industryBlocked && assetTabs.some(tab => tab.key === current)" />
        <template v-else-if="current === 'package'">
          <view class="variant-head"><b>项目包与版本</b><span>这份工程是哪个版本、依赖什么？</span></view>
          <view class="package-grid">
            <section class="card glass package-card delivery-package">
              <view class="row"><span>当前版本</span><view class="version-site"><span>{{ hasPublished ? currentVersion?.id || "未发布" : "未发布" }}</span><label class="site-field">Site<select v-model="selectedSite" class="site-select" :disabled="!sites.length" @change="!PREVIEW_MODE && selectLayer(selectedSite)"><option v-if="!sites.length" value="">没有可用 Site</option><option v-for="item in sites" :key="item.id" :value="item.id">{{ nameOf(item) }}</option></select></label></view></view>
              <view class="row"><span>依赖</span><span class="src">{{ packageDependencies }}</span></view>
              <view class="row"><span>现场绑定</span><span class="src">地址、凭据、控制启动与运行数据不进包</span></view>
              <view class="row"><span>预检</span><span v-if="gates" class="chip" :class="gates.status">{{ statusText(gates.status) }} · 通过 {{ gates.summary?.pass || 0 }} · 注意 {{ gates.summary?.warn || 0 }} · 阻断 {{ gates.summary?.fail || 0 }}<template v-if="PREVIEW_MODE && previewPreflightFail"> · PCS7 必需点未映射</template></span><span v-else class="src">{{ gateError ? "读取失败" : "—" }}</span></view>
              <view class="actions">
                <button class="primary" :disabled="!selectedSite || !canConfigure || !!busy" @click="publish()">{{ busy === "publish" ? "发布中…" : hasPublished ? "重新发布" : "发布版本" }}</button>
                <button class="ghost" :disabled="!selectedSite || !hasPublished || !canExport || !!busy" @click="exportPackage">{{ busy === "export" ? "导出中…" : "导出项目包" }}</button>
                <button class="ghost" :disabled="!canExport || !!busy" @click="exportEvidence">{{ busy === "evidence" ? "导出中…" : "导出验收证据" }}</button>
                <label class="publish-note"><input v-model="publishNote" aria-label="发布说明（可选）" placeholder="发布说明（可选）" /></label>
              </view>
              <view v-if="PREVIEW_MODE" class="actions" aria-label="原型模拟场景">
                <button class="ghost" :aria-pressed="previewPreflightFail" @click="togglePreviewPreflight">模拟预检失败{{ previewPreflightFail ? '：开' : '' }}</button>
                <button class="ghost" :aria-pressed="previewDependencyMismatch" @click="togglePreviewDependency">模拟依赖不匹配{{ previewDependencyMismatch ? '：开' : '' }}</button>
              </view>
              <view v-if="publishBlocked" class="confirm-box">
                <span>预检存在阻断项，不能发布可交付版本；请修复后重新预检。</span>
                <button class="ghost" :disabled="!!busy" @click="runPreflight">重新预检</button>
              </view>
              <p class="note">部署到另一实例时必须重新绑定现场；稳定引用靠节点 ref 与画面 token。发布不改变运行配置，也不等于部署。</p>
            </section>
            <section class="card glass package-card package-contents">
              <h5>包内容 <span class="h5-note">{{ packageSummary?.source_version || (PREVIEW_MODE ? packageSummary?.id : hasPublished ? "正在读取冻结版本…" : "当前草稿 · 未发布") }}</span></h5>
              <table class="mini-table"><tbody>
                <tr><td>对象</td><td class="src">{{ contentSummary ? `${contentSummary.nodes} 个节点` : "—" }}</td></tr>
                <tr><td>点位</td><td class="src">{{ contentSummary ? `${contentSummary.points} 个工程点位` : "—" }}</td></tr>
                <tr><td>画面</td><td class="src">{{ contentSummary ? `${contentSummary.screens} 份画面定义 · 其余继承或默认` : "—" }}</td></tr>
                <tr><td>策略</td><td class="src">{{ contentSummary ? `${contentSummary.strategies} 条规则或状态机` : "—" }}</td></tr>
                <tr><td>依赖</td><td class="src">{{ packageDependencies }}</td></tr>
              </tbody></table>
              <view v-if="packageSummary?.warnings?.length" class="package-warnings">{{ packageSummary.warnings.join("；") }}</view>
              <view v-if="packageError" class="error">{{ packageError }}</view>
            </section>
          </view>
          <view v-if="message" class="notice" :class="messageTone">{{ message }}</view>
          <section class="card glass versions-card">
            <h5>版本</h5>
            <view v-if="versionError" class="error">{{ versionError }}</view>
            <view v-else-if="versions.length" class="version-lines">
              <view v-for="version in versions" :key="version.id" class="row version-line"><span class="version-id">{{ version.id }}</span><span class="src">{{ version.id === currentVersion?.id ? "当前可交付 · " : !PREVIEW_MODE && !version.deliverable ? "非可交付快照 · " : "" }}{{ clock(version.created_at) }} · {{ version.operator || "—" }} · {{ statusText(version.preflight_status) }} · {{ version.nodes }} 个节点{{ version.note ? ` · ${version.note}` : "" }}</span></view>
            </view>
            <view v-else class="empty compact-empty">{{ selectedSite ? "当前 Site 还没有发布版本。" : "选择真实 Site 后才能读取版本；项目根不能代替 Site。" }}</view>
          </section>
          <section class="card glass import-card delivery-package">
            <h5>{{ importResult ? "跨实例导入" : "跨实例交付" }}</h5>
            <view v-if="!importResult" class="row"><span>下一步</span><span class="src">在干净实例导入，再重新绑定现场</span></view>
            <view v-if="!importResult" class="actions"><button class="ghost" :disabled="!projectRootId || !canConfigure || !!busy" @click="choosePackage">{{ busy === "import" ? "导入中…" : "导入到新实例" }}</button></view>
            <template v-if="importResult">
              <view class="import-result">
                <view class="row"><span>新 Site</span><span class="src">{{ importResult.name }} · {{ importResult.site_id }}</span></view>
                <view class="row"><span>导入结果</span><span class="src">{{ importResult.created }} 个节点 · {{ importResult.channels }} 条通道 · {{ importResult.rules }} 条规则</span></view>
                <view class="row"><span>基线版本</span><span class="src">{{ importResult.version_id || "未保存，查看导入警告" }}</span></view>
                <view v-if="importResult.warnings?.length" class="package-warnings">{{ importResult.warnings.join("；") }}</view>
              </view>
              <ImportGuide :result="importResult" :snapshot="session.snapshot" :values="session.values" :quality="session.quality" @refresh="refreshImportState" />
            </template>
            <p class="note">依赖不匹配会拒绝导入；地址与凭据须重新绑定，重绑完成前不能运行，控制不自动启动。</p>
          </section>
          <PreflightPanel :report="gates" :latest="latestPreflight" :error="gateError || latestError" :busy="busy === 'preflight'" :can-run="canConfigure" @run="runPreflight" @repair="repairGate" />
        </template>

        <template v-else-if="current === 'deploy'">
          <view class="variant-head"><b>部署记录</b><span>它现在跑在哪个现场？</span></view>
          <section class="card glass">
            <view class="table-wrap"><table class="deploy-table"><thead><tr><th>目标</th><th>版本</th><th>时间</th><th>人</th><th>状态</th></tr></thead>
              <tbody>
                <tr v-if="PREVIEW_MODE" v-for="record in DELIVERY_PREVIEW_DEPLOYMENTS" :key="record.target"><td>{{ record.target }}</td><td>{{ record.version }}</td><td>{{ record.time }}</td><td>{{ record.operator }}</td><td><span class="chip" :class="record.status === '运行中' ? 'ok' : 'warn'">{{ record.status }}</span></td></tr>
                <tr v-else><td colspan="5" class="empty">暂无部署记录：后端没有独立的“包版本 → 目标实例”部署记录 API。发布、导入与回退都不能记成部署成功。</td></tr>
              </tbody>
            </table></view>
            <view class="session-import">
              <view class="row"><span>本次页面导入状态</span><span class="src">{{ importStatus }}</span></view>
              <template v-if="importResult">
                <view class="row"><span>新 Site</span><span class="src">{{ importResult.name }} · {{ importResult.site_id }}</span></view>
                <view class="row"><span>导入结果</span><span class="src">{{ importResult.created }} 个节点 · {{ importResult.channels }} 条通道 · {{ importResult.rules }} 条规则</span></view>
                <view class="row"><span>基线版本</span><span class="src">{{ importResult.version_id || "未保存，查看导入警告" }}</span></view>
                <view v-if="importResult.warnings?.length" class="notice warn">{{ importResult.warnings.join("；") }}</view>
              </template>
            </view>
            <p class="note">部署只应用工程内容，不自动授予控制权限；地址与凭据需在现场重新绑定。此处只显示本次真实导入响应，不显示“运行中”或“已部署”等后端没有记录的状态。</p>
          </section>
        </template>

        <template v-else-if="current === 'rollback'">
          <view class="variant-head"><b>版本回退</b><span>怎么回到上一版工程？</span></view>
          <section class="card glass">
            <view v-if="rollbackMessage" class="notice" :class="rollbackTone">{{ rollbackMessage }}</view>
            <view v-if="versionError" class="error">{{ versionError }}</view>
            <view v-else-if="versions.length" class="version-list">
              <view v-for="version in versions" :key="version.id" class="row version-row">
                <span class="version-id">{{ version.id }}</span><span class="src">{{ clock(version.created_at) }} · {{ version.operator || "—" }} · {{ version.nodes }} 个节点</span>
                <button class="ghost" :class="{ danger: rollbackConfirm === version.id }" :disabled="!selectedSite || !canConfigure || busy === 'rollback'" @click="rollback(version)">{{ rollbackConfirm === version.id ? "确认回退（历史保留）" : "回退" }}</button>
              </view>
            </view>
            <view v-else class="empty compact-empty">{{ selectedSite ? "还没有可回退的版本。" : "请先选择真实 Site。" }}</view>
            <h5 class="rollback-scope">回退范围</h5>
            <table class="mini-table"><tbody>
              <tr><td>恢复</td><td class="src">节点、点位、画面、策略与依赖声明</td></tr>
              <tr><td>保留</td><td class="src">运行历史、告警、审计与命令；回退前保存非交付基线，失败先恢复基线</td></tr>
              <tr><td>不做</td><td class="src">不回放旧命令，不自动恢复控制输出，不等于部署</td></tr>
              <tr><td>后端限制</td><td class="src">版本或当前工程包含计算点、虚拟输入点时拒绝回退；预览不模拟资源级阻断</td></tr>
            </tbody></table>
            <p class="note">回退只回工程，不回历史与命令。</p>
          </section>
        </template>

        <AccountsPanel v-else-if="current === 'users'" :root-id="projectRootId" :nodes="session.snapshot?.nodes || []" :can-manage="canManageAccounts" />
        <AuditPanel v-else-if="current === 'audit'" :can-view="canViewAudit" />
        <IndustryPanel v-else :can-change="canSiteSettings" @changed="loadIndustryStatus" />
      </main>
    </view>
    <AlarmCenter v-if="alarmOpen" :alarms="session.alarms" @close="alarmOpen = false" @ack="acknowledge" @close-alarm="close" />
  </view>
</template>

<script setup lang="ts">
import IndustryGate from '../../components/IndustryGate.vue'
import { industryBlocked } from '../../stores/session'
import { currentProject } from '../../stores/projects'
import { computed, nextTick, onMounted, onUnmounted, ref, toRef, watch } from "vue"
import TopBar from "../../components/TopBar.vue"
import AlarmCenter from "../../components/AlarmCenter.vue"
import PreflightPanel from "../../components/delivery/PreflightPanel.vue"
import AccountsPanel from "../../components/delivery/AccountsPanel.vue"
import AuditPanel from "../../components/delivery/AuditPanel.vue"
import IndustryPanel from "../../components/delivery/IndustryPanel.vue"
import ImportGuide from "../../components/delivery/ImportGuide.vue"
import { ApiError, downloadText, request } from "../../api/client"
import { PREVIEW_MODE } from "../../api/source"
import { DELIVERY_PREVIEW_DEPLOYMENTS, deliveryPreviewState, setDeliveryPreviewScenarios } from "../../api/preview/delivery"
import { previewProjects, previewWorkspace } from "../../api/preview/workspace"
import { acknowledgeAlarm, closeAlarm, layerPath, selectLayer, nameOf, pendingAlarms, refreshAll, requireAuth, session, startPolling, stopPolling } from "../../stores/session"
import { canAction, clock, deliveryError, statusText } from "../../components/delivery/contracts"

type Gate = { key: string; title: string; status: string; detail: string; object?: string; next_step?: string; informational?: boolean }
type ReleaseContent = { nodes: number; points: number; strategies: number; screens: number; dependencies: { kind: string; id: string; version: string }[] }
type PreflightReport = { id?: string; site?: string; generated_at?: string; operator?: string; status: string; summary: Record<string, number>; gates: Gate[]; fingerprint?: string; content?: ReleaseContent }
type VersionSummary = { id: string; site_id: string; site_name: string; created_at: string; operator: string; note?: string; preflight_status?: string; nodes: number; deliverable?: boolean; fingerprint?: string }
type ProjectPackage = { id: string; name: string; source_version?: string; source_fingerprint?: string; point_count?: number; strategy_count?: number; screen_count?: number; dependencies?: { kind: string; id: string; version: string }[]; nodes?: unknown[]; screens?: unknown[]; stripped?: unknown[]; warnings?: string[] }
type ImportResult = { site_id: string; name: string; created: number; channels: number; rules: number; node_ids?: string[]; version_id?: string; warnings?: string[] }
type RollbackResult = { version: string; baseline: string; updated: number; created: number; extra: number; preflight_status?: string }

const current = PREVIEW_MODE ? toRef(deliveryPreviewState, "current") : ref("package")
const emsInstalled = ref<boolean | null>(null)
const emsVersion = ref("")
const selectedSite = PREVIEW_MODE ? toRef(deliveryPreviewState, "selectedSite") : ref("")
const gates = ref<PreflightReport | null>(null)
const latestPreflight = ref<PreflightReport | null>(null)
const gateError = ref("")
const latestError = ref("")
const versions = ref<VersionSummary[]>([])
const versionError = ref("")
const publishNote = PREVIEW_MODE ? toRef(deliveryPreviewState, "publishNote") : ref("")
const currentVersion = computed(() => versions.value.find(version => PREVIEW_MODE || version.deliverable))
const hasPublished = computed(() => PREVIEW_MODE ? !!deliveryPreviewState.publishedSites[selectedSite.value] : !!currentVersion.value)
const publishBlocked = ref(false)
const message = ref("")
const messageTone = ref<"success" | "warn" | "error">("success")
const rollbackMessage = ref("")
const rollbackTone = ref<"success" | "warn" | "error">("success")
const rollbackConfirm = ref("")
const packageSummary = ref<ProjectPackage | null>(null)
const packageError = ref("")
const packageLoading = ref(false)
const importResult = PREVIEW_MODE ? toRef(deliveryPreviewState, "importResult") : ref<ImportResult | null>(null)
const importStatus = PREVIEW_MODE ? toRef(deliveryPreviewState, "importStatus") : ref("未导入")
const busy = ref("")
const previewPreflightFail = PREVIEW_MODE ? toRef(deliveryPreviewState, "preflightFail") : ref(false)
const previewDependencyMismatch = PREVIEW_MODE ? toRef(deliveryPreviewState, "dependencyMismatch") : ref(false)
async function togglePreviewPreflight() {
  previewPreflightFail.value = !previewPreflightFail.value
  setDeliveryPreviewScenarios({ preflightFail: previewPreflightFail.value })
  publishBlocked.value = false
  message.value = ""
  await loadGates(selectedSite.value)
}
function togglePreviewDependency() {
  previewDependencyMismatch.value = !previewDependencyMismatch.value
  setDeliveryPreviewScenarios({ dependencyMismatch: previewDependencyMismatch.value })
  message.value = ""
}
const alarmOpen = ref(false)
let gateTimer: ReturnType<typeof setInterval> | undefined
const assetTabs = [{ key: "package", label: "项目包与版本" }, { key: "deploy", label: "部署记录" }, { key: "rollback", label: "版本回退" }]
const systemTabs = [{ key: "users", label: "账号与权限" }, { key: "audit", label: "审计" }, { key: "industry", label: "行业包" }]
const currentLabel = computed(() => [...assetTabs, ...systemTabs].find(item => item.key === current.value)?.label || "")
const sites = computed(() => session.snapshot?.nodes.filter(node => node.type === "site") || [])
const projectRootId = computed(() => PREVIEW_MODE ? session.snapshot?.roots[0] || "" : currentProject.value?.root_id || "")
const projectName = computed(() => {
  if (PREVIEW_MODE) return previewProjects.find(project => project.id === previewWorkspace.projectId)?.name || "未选择项目"
  return currentProject.value?.name || "未选择项目"
})
const siteName = computed(() => {
  const site = sites.value.find(node => node.id === selectedSite.value)
  return site ? nameOf(site) : ""
})
const contentSummary = computed(() => packageSummary.value ? {
  nodes: packageSummary.value.nodes?.length || 0, points: packageSummary.value.point_count || 0,
  strategies: packageSummary.value.strategy_count || 0, screens: packageSummary.value.screen_count ?? packageSummary.value.screens?.length ?? 0,
  dependencies: packageSummary.value.dependencies || [],
} : !hasPublished.value && !gateError.value ? gates.value?.content : undefined)
const packageDependencies = computed(() => {
  if (!selectedSite.value) return "—"
  if (!contentSummary.value) return packageError.value || gateError.value ? "读取失败" : packageLoading.value ? "正在读取…" : "—"
  return contentSummary.value.dependencies.length ? contentSummary.value.dependencies.map(dependencyText).join("；") : "无行业包或 Profile 依赖"
})
const canConfigure = computed(() => canAction(session.snapshot, "configure"))
const canExport = computed(() => canAction(session.snapshot, "export"))
const canManageAccounts = computed(() => canAction(session.snapshot, "account_manage"))
const canViewAudit = computed(() => canAction(session.snapshot, "audit_view"))
const canSiteSettings = computed(() => canAction(session.snapshot, "site_settings") && (PREVIEW_MODE || !!session.snapshot?.roots.includes(projectRootId.value)))

watch(() => previewWorkspace.projectId, () => { if (PREVIEW_MODE) void changePreviewProject() })
watch(() => previewWorkspace.emsEnabled, enabled => { if (PREVIEW_MODE) emsInstalled.value = enabled }, { immediate: true })
async function changePreviewProject() {
  if (!PREVIEW_MODE) return
  selectedSite.value = previewWorkspace.projectId
  await refreshAll()
  await loadSiteData(selectedSite.value)
}
watch([sites, () => session.layer], ([list]) => {
  const site = !PREVIEW_MODE ? layerPath(session.layer).find(node => node.type === 'site') : null
  if (site) selectedSite.value = site.id
  else if (!list.some(node => node.id === selectedSite.value)) selectedSite.value = PREVIEW_MODE ? previewWorkspace.projectId : list[0]?.id || ""
}, { immediate: true })
watch(selectedSite, () => {
  gates.value = null; latestPreflight.value = null; versions.value = []
  gateError.value = ""; latestError.value = ""; versionError.value = ""
  publishBlocked.value = false; rollbackConfirm.value = ""; packageSummary.value = null
  packageError.value = ""; packageLoading.value = false
  message.value = ""; rollbackMessage.value = ""
  void loadSiteData(selectedSite.value)
})
watch(canExport, () => { void loadPackage(selectedSite.value) })
watch(current, (next, previous) => { if (previous === "industry" && next !== "industry") void loadIndustryStatus() })
function queryFor(site: string) { return site ? `?site=${encodeURIComponent(site)}` : "" }
async function loadIndustryStatus() {
  try {
    const result = await request<{ packages: { id: string; version: string; installed: boolean }[] }>("/industry/packages")
    const ems = result.packages?.find(item => item.id === "ems")
    emsInstalled.value = ems?.installed || false
    emsVersion.value = ems?.version || ""
  } catch { emsInstalled.value = null }
}
async function toggleEms() {
  const installed = emsInstalled.value
  if (installed === null || busy.value) return
  if (!installed && !emsVersion.value) { uni.showToast({ title: "未读取到 EMS 行业包版本", icon: "none" }); return }
  busy.value = "industry"
  try {
    if (installed) await request("/industry/packages/ems", "DELETE")
    else await request("/industry/packages", "POST", { id: "ems", version: emsVersion.value })
    await Promise.all([loadIndustryStatus(), refreshAll()])
    uni.showToast({ title: installed ? "EMS 已停用；配置数据保留" : "EMS 已启用", icon: "none" })
  } catch (error) {
    uni.showToast({ title: deliveryError(error), icon: "none" })
  } finally { busy.value = "" }
}
async function loadSiteData(site: string) {
  await Promise.all([loadGates(site), loadLatest(site), loadVersions(site)])
  if (site === selectedSite.value) await loadPackage(site)
}
async function loadPackage(site: string) {
  if (!site || !canExport.value || !PREVIEW_MODE && !currentVersion.value) { packageSummary.value = null; packageError.value = ""; packageLoading.value = false; return }
  const versionID = currentVersion.value?.id
  packageLoading.value = true; packageError.value = ""; packageSummary.value = null
  try {
    const result = await request<ProjectPackage>(`/projects/export${queryFor(site)}${!PREVIEW_MODE && versionID ? `&version=${encodeURIComponent(versionID)}` : ""}`)
    if (site === selectedSite.value && (PREVIEW_MODE || currentVersion.value?.id === versionID)) packageSummary.value = result
  } catch (error) {
    if (site === selectedSite.value && (PREVIEW_MODE || currentVersion.value?.id === versionID)) { packageSummary.value = null; packageError.value = deliveryError(error) }
  } finally {
    if (site === selectedSite.value && (PREVIEW_MODE || currentVersion.value?.id === versionID)) packageLoading.value = false
  }
}
async function loadGates(site: string) {
  try {
    const result = await request<PreflightReport>(`/delivery/gates${queryFor(site)}`)
    if (site === selectedSite.value) { gates.value = result; gateError.value = "" }
  } catch (error) { if (site === selectedSite.value) { gates.value = null; gateError.value = deliveryError(error) } }
}
async function loadLatest(site: string) {
  try {
    const result = await request<{ latest: PreflightReport | null }>(`/delivery/preflight${queryFor(site)}`)
    if (site === selectedSite.value) { latestPreflight.value = result.latest || null; latestError.value = "" }
  } catch (error) { if (site === selectedSite.value) { latestPreflight.value = null; latestError.value = deliveryError(error) } }
}
async function loadVersions(site: string) {
  if (!site) { versions.value = []; versionError.value = ""; return }
  try {
    const result = await request<{ versions: VersionSummary[] }>(`/projects/versions${queryFor(site)}`)
    if (site === selectedSite.value) { versions.value = result.versions || []; versionError.value = "" }
  } catch (error) { if (site === selectedSite.value) { versions.value = []; versionError.value = deliveryError(error) } }
}
function repairGate(gate: { repair_node?: string; repair_step?: string }) {
  if (!gate.repair_node || !session.snapshot?.nodes.some(node => node.id === gate.repair_node)) return
  selectLayer(gate.repair_node)
  uni.navigateTo({ url: `/pages/design/index?step=${encodeURIComponent(gate.repair_step || "tree")}` })
}
async function runPreflight() {
  const site = selectedSite.value
  busy.value = "preflight"; gateError.value = ""; latestError.value = ""
  try {
    const result = await request<PreflightReport>(`/delivery/preflight${queryFor(site)}`, "POST")
    if (site === selectedSite.value) {
      latestPreflight.value = result
      if (result.status !== "fail" && publishBlocked.value) { publishBlocked.value = false; message.value = "" }
    }
    await loadGates(site)
  } catch (error) { gateError.value = deliveryError(error) } finally { busy.value = "" }
}
async function publish() {
  const site = selectedSite.value
  if (!site) { message.value = "请选择真实 Site"; messageTone.value = "error"; return }
  busy.value = "publish"; message.value = ""
  try {
    const result = await request<VersionSummary>("/projects/publish", "POST", { site, note: publishNote.value.trim(), confirm: false, ...(!PREVIEW_MODE ? { expected_fingerprint: gates.value?.fingerprint } : {}) })
    if (site !== selectedSite.value) return
    message.value = `已发布版本 ${result.id}（${result.nodes} 个节点）；未改变运行配置，不等于部署`
    messageTone.value = "success"; publishNote.value = ""; publishBlocked.value = false
    await loadVersions(site)
    await loadPackage(site)
  } catch (error) {
    messageTone.value = "error"; message.value = deliveryError(error)
    if (error instanceof ApiError && error.status === 409 && error.code === "preflight_failed") {
      messageTone.value = "warn"
      if (PREVIEW_MODE) { message.value = "预检失败，无法发布"; publishBlocked.value = false }
      else publishBlocked.value = true
    }
  } finally { busy.value = "" }
}
function dependencyText(item: { kind: string; id: string; version: string }) {
  return `${item.kind === "industry" ? "行业包" : "Profile"} ${item.id}@${item.version}`
}
async function exportPackage() {
  const site = selectedSite.value
  const versionID = currentVersion.value?.id
  if (!site || !PREVIEW_MODE && !versionID) return
  busy.value = "export"; message.value = ""; packageError.value = ""
  try {
    const result = await request<ProjectPackage>(`/projects/export${queryFor(site)}${!PREVIEW_MODE && versionID ? `&version=${encodeURIComponent(versionID)}` : ""}`)
    if (site !== selectedSite.value || !PREVIEW_MODE && currentVersion.value?.id !== versionID) return
    const filename = `${(result.name || siteName.value || "project").replace(/[\\/:*?"<>|]/g, "-")}.package.json`
    downloadText(filename, JSON.stringify(result, null, 2))
    packageSummary.value = result
    message.value = `已导出后端项目包 ${result.id}；不含地址、凭据、控制启动与运行状态`
    messageTone.value = "success"
  } catch (error) { message.value = deliveryError(error); messageTone.value = "error" } finally { busy.value = "" }
}
function choosePackage() {
  if (PREVIEW_MODE) {
    if (!hasPublished.value) { message.value = "先发布一个项目包版本"; messageTone.value = "warn"; return }
    const previewFile = new File([JSON.stringify(packageSummary.value || {})], "preview-package.json", { type: "application/json" })
    void importPackage(previewFile)
    return
  }
  uni.chooseFile({
    count: 1,
    type: "all",
    extension: [".json"],
    success: result => {
      const files = Array.isArray(result.tempFiles) ? result.tempFiles : [result.tempFiles]
      const file = files[0] as File | undefined
      if (file) void importPackage(file)
    },
    fail: error => {
      if (!String(error.errMsg || "").includes("cancel")) { message.value = "选择项目包失败"; messageTone.value = "error" }
    },
  })
}
async function importPackage(file: File) {
  busy.value = "import"; message.value = ""
  try {
    let parsed: unknown
    try { parsed = JSON.parse(await file.text()) } catch { throw new Error("文件不是有效的项目包 JSON") }
    if (!projectRootId.value) throw new Error("当前没有可导入的项目根")
    const result = await request<ImportResult>("/projects/import", "POST", { package: parsed, parent: projectRootId.value })
    importResult.value = result
    if (!PREVIEW_MODE) importStatus.value = `已导入 ${result.name}；不是部署`
    await refreshAll()
    if (!PREVIEW_MODE && sites.value.some(site => site.id === result.site_id)) selectedSite.value = result.site_id
    await nextTick()
    message.value = `已导入「${result.name}」：${result.created} 个节点、${result.channels} 条通道、${result.rules} 条规则${result.version_id ? `；基线 ${result.version_id}` : ""}。需重新绑定地址与凭据，控制不自动启动${result.warnings?.length ? `。警告：${result.warnings.join("；")}` : ""}`
    messageTone.value = result.warnings?.length ? "warn" : "success"
  } catch (error) { message.value = deliveryError(error, "import"); messageTone.value = "error" } finally { busy.value = "" }
}
async function refreshImportState() {
  await refreshAll()
}
async function exportEvidence() {
  busy.value = "evidence"; message.value = ""
  try {
    let data: ArrayBuffer | Uint8Array
    if (PREVIEW_MODE) {
      const evidence = await request<Record<string, unknown>>(`/delivery/evidence${queryFor(selectedSite.value)}`)
      downloadText("fastiot-acceptance-evidence.json", JSON.stringify({ ...evidence, project: projectName.value }, null, 2))
      message.value = "已导出验收证据 JSON（原型）：当前预检、映射差异、版本、导入状态与审计；不替代真机签收"
      messageTone.value = "success"
      return
    } else {
      data = await request<ArrayBuffer>(`/delivery/evidence${queryFor(selectedSite.value)}`, 'GET', undefined, 'arraybuffer')
    }
    const bytes = data instanceof Uint8Array ? data : new Uint8Array(data)
    if (bytes[0] !== 0x50 || bytes[1] !== 0x4b) throw new Error("后端未返回有效 ZIP 文件")
    const url = URL.createObjectURL(new Blob([bytes], { type: "application/zip" }))
    const link = document.createElement("a")
    link.href = url; link.download = `fastiot-delivery-evidence-${new Date().toISOString().slice(0, 10)}.zip`; link.click()
    URL.revokeObjectURL(url)
    message.value = "已导出后端证据 ZIP（当前账号可见范围）；仿真与当前实例检查不替代真机签收"
    messageTone.value = "success"
  } catch (error) { message.value = deliveryError(error); messageTone.value = "error" } finally { busy.value = "" }
}
async function rollback(version: VersionSummary) {
  if (rollbackConfirm.value !== version.id) { rollbackConfirm.value = version.id; rollbackMessage.value = `再次点击确认回退到 ${version.id}；只回工程配置，历史与命令保留`; rollbackTone.value = "warn"; return }
  const site = selectedSite.value
  if (!site) return
  busy.value = "rollback"; rollbackMessage.value = ""
  try {
    const result = await request<RollbackResult>("/projects/rollback", "POST", { site, version: version.id })
    rollbackMessage.value = `已回退到 ${result.version}：更新 ${result.updated}、创建 ${result.created}、保留多余 ${result.extra}；回退前基线 ${result.baseline}；历史未回退，旧命令未回放，不等于部署`
    rollbackTone.value = "success"; rollbackConfirm.value = ""
    try {
      await loadVersions(site)
      await refreshAll()
    } catch {
      rollbackMessage.value += "；回退结果已确认，但页面刷新失败，请重新读取"
      rollbackTone.value = "warn"
    }
  } catch (error) { rollbackMessage.value = deliveryError(error); rollbackTone.value = "error" } finally { busy.value = "" }
}
function navigate(key: string) { if (key !== "delivery") uni.navigateTo({ url: `/pages/${key}/index` }) }
async function acknowledge(id: string) { await acknowledgeAlarm(id) }
async function close(id: string) { await closeAlarm(id) }
onMounted(() => {
  if (!requireAuth()) return
  startPolling()
  void loadIndustryStatus()
  void loadSiteData(selectedSite.value)
  gateTimer = setInterval(() => { if (current.value === "package") void loadGates(selectedSite.value) }, 10000)
})
onUnmounted(() => { if (gateTimer) clearInterval(gateTimer); stopPolling() })
</script>

<style scoped>
.page { padding: 14px; }
.statebar { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; padding: 6px 18px; border: 0; color: var(--faint); background: rgba(255,255,255,.24); backdrop-filter: blur(16px); font-size: 11.5px; line-height: normal; }
.statebar b { color: var(--muted); font-weight: 600; }
.layout { display: grid; grid-template-columns: 272px minmax(0, 1fr); gap: 14px; margin-top: 14px; }
.layout, .statebar { --ink: #172c3b; --muted: #46677b; --faint: #718a9c; --line: rgba(72,111,139,.14); --accent: #0c7d84; --mint: #23705f; --amber: #a4631b; --danger: #b23a32; font-family: "HarmonyOS Sans SC", "PingFang SC", "Microsoft YaHei", system-ui, sans-serif; }
.side { height: calc(100vh - 120px); padding: 14px 10px 30px; align-self: start; }
.navgroup { margin-bottom: 16px; }
.navgroup > h4 { margin: 0 0 4px; padding: 0 8px; color: var(--faint); font-size: 11px; font-weight: 600; letter-spacing: .12em; line-height: 1.2; }
.side button { display: block; width: 100%; min-height: 31px; margin-bottom: 1px; border: 0; border-radius: 8px; background: transparent; color: var(--muted); padding: 7px 12px; text-align: left; font-size: 13px; }
.side button.active { background: rgba(14,124,123,.14); color: var(--accent); font-weight: 650; }
.stage { display: grid; gap: 12px; min-width: 0; align-content: start; }
.card { min-width: 0; padding: 16px; }
.side.glass {
  background: linear-gradient(135deg,rgba(255,255,255,.3),rgba(255,255,255,.04)), rgba(255,255,255,.48);
  backdrop-filter: blur(28px) saturate(1.4);
  -webkit-backdrop-filter: blur(28px) saturate(1.4);
  border: 2px solid rgba(255,255,255,.78);
  border-radius: 20px;
  box-shadow: 0 20px 45px rgba(45,83,110,.13), inset 0 0 18px rgba(255,255,255,.22);
}
.stage :deep(.card.glass) {
  background: linear-gradient(135deg,rgba(255,255,255,.32),rgba(255,255,255,.04)), rgba(255,255,255,.48);
  backdrop-filter: blur(28px) saturate(1.45);
  -webkit-backdrop-filter: blur(28px) saturate(1.45);
  border: 2px solid rgba(255,255,255,.78);
  border-radius: 18px;
  box-shadow: 0 20px 45px rgba(45,83,110,.13), inset 0 0 0 1px rgba(255,255,255,.22), inset 0 0 18px rgba(255,255,255,.22);
}
.stage :deep(h5) { margin: 0 0 10px; color: var(--faint); font-size: 12px; font-weight: 600; letter-spacing: .06em; }
.stage :deep(.h5-note) { float: right; color: var(--accent); font-size: 11px; font-weight: 400; letter-spacing: 0; }
.stage :deep(.row) { padding: 7px 0; border-bottom: 1px dashed #e8eef2; font-size: 13px; }
.stage :deep(.note) { color: var(--faint); font-size: 12px; line-height: 1.7; }
.stage :deep(table) { width: 100%; border-collapse: collapse; font-size: 13px; }
.stage :deep(th) { text-align: left; font-size: 11px; letter-spacing: .06em; color: var(--faint); font-weight: 600; padding: 8px 10px; border-bottom: 1px solid var(--line); white-space: normal; }
.stage :deep(td) { padding: 9px 10px; border-bottom: 1px solid rgba(72,111,139,.12); }
.stage :deep(.mini-table) { font-size: 12px; }
.stage :deep(.mini-table th), .stage :deep(.mini-table td) { padding: 7px 8px; }
.stage :deep(.mini-table th) { font-size: 10.5px; }
.stage :deep(.mini-table tr:last-child td) { border-bottom: 0; }
.stage :deep(.primary) { min-height: 0; border: 0; border-radius: 9px; padding: 8px 15px; background: var(--accent); color: #fff; font-size: 13px; line-height: normal; box-shadow: 0 6px 16px rgba(12,125,132,.22); }
.stage :deep(.ghost) { min-height: 0; border: 1px solid rgba(255,255,255,.8); border-radius: 9px; padding: 7px 13px; background: rgba(255,255,255,.55); color: var(--muted); font-size: 12.5px; line-height: normal; }
.stage :deep(.tag) { display: inline-block; min-height: 0; padding: 2px 8px; border: 1px solid var(--line); border-radius: 999px; background: #eef4f7; color: var(--muted); font-size: 11px; line-height: normal; }
.stage :deep(.tag.ok) { border-color: #cfe6dc; background: #e6f3ee; color: var(--mint); }
.stage :deep(.chip) { min-height: 0; padding: 2px 9px; border-radius: 999px; font-size: 11.5px; line-height: normal; }
.stage :deep(.chip.ok) { border-color: #cfe6dc; background: #eef8f3; color: var(--mint); }
.stage :deep(.chip.warn) { border-color: #efdcc8; background: #fdf5ec; color: var(--amber); }
.stage :deep(.banner) { display: flex; align-items: center; gap: 10px; margin-bottom: 0; padding: 10px 12px; border: 1px solid #eeddb9; border-radius: 10px; background: #fdf7ea; color: #6f4a12; font-size: 12.5px; }
.stage :deep(.banner button) { min-height: 0; padding: 4px 10px; border: 1px solid #d9bd86; border-radius: 8px; background: #fff; font-size: 12px; }
.variant-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; margin: 0; }
.variant-head b { font-size: 15px; }
.variant-head span { color: var(--faint); font-size: 12.5px; }
.package-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; align-items: start; }
.package-card { min-height: 286px; }
.package-contents { min-height: 217px; }
.package-card .row { min-height: 33px; padding: 7px 0; }
.package-card .row:first-child { min-height: 32px; padding: 4px 0; }
.package-card .src { max-width: 72%; overflow-wrap: anywhere; }
.version-site { display: flex; align-items: center; justify-content: flex-end; gap: 10px; min-width: 0; font-size: 12px; }
.site-field { display: flex; align-items: center; gap: 5px; color: var(--faint); font-size: 11px; }
.site-select { width: 112px; height: 24px; min-height: 24px; padding: 0 5px; border: 1px solid var(--line); border-radius: 8px; background: rgba(255,255,255,.58); font-size: 11px; }
.chip.fail { color: var(--danger); background: rgba(253,235,232,.8); }
.package-card .chip { min-height: 0; padding: 2px 9px; font-size: 11.5px; line-height: normal; }
.delivery-package .actions { gap: 8px; margin-top: 14px; }
.delivery-package .primary, .delivery-package .ghost { min-height: 34px; margin: 0; line-height: normal; }
.delivery-package .primary { padding: 8px 15px; font-size: 13px; }
.delivery-package .ghost { padding: 7px 13px; font-size: 12.5px; }
.publish-note { display: block; margin: 0; }
.publish-note input { width: 112px; height: 34px; min-height: 34px; padding: 5px 8px; border: 1px solid var(--line); border-radius: 9px; background: rgba(255,255,255,.58); font-size: 11.5px; }
.confirm-box { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 12px; padding: 11px 12px; border: 1px solid rgba(164,99,27,.28); border-radius: 10px; background: rgba(253,247,234,.7); color: var(--amber); font-size: 12px; }
.danger { border: 1px solid rgba(178,58,50,.35); border-radius: 9px; padding: 8px 12px; background: rgba(253,241,238,.85); color: var(--danger); }
.notice { padding: 10px 12px; border-radius: 10px; font-size: 12.5px; line-height: 1.7; }
.notice.success { background: rgba(230,243,238,.8); color: var(--mint); }
.notice.warn { background: rgba(253,247,234,.85); color: var(--amber); }
.notice.error { background: rgba(253,241,238,.85); color: var(--danger); }
.package-warnings { padding-top: 8px; color: var(--amber); font-size: 12px; line-height: 1.7; }
.compact-empty { padding: 12px 0 4px; }
.versions-card { min-height: 156px; }
.version-lines { display: grid; }
.versions-card .row, .import-card .row { min-height: 33px; padding: 7px 0; }
.version-line .src { max-width: 76%; overflow-wrap: anywhere; }
.version-id { font-family: inherit; font-size: 13px; overflow-wrap: anywhere; }
.import-card { min-height: 170px; }
.import-card .ghost { min-height: 31px; }
.import-result { margin-top: 10px; padding-top: 4px; border-top: 1px solid var(--line); }
.import-card .note, .package-card .note { margin: 10px 0 0; line-height: 1.7; }
.session-import { margin-top: 12px; padding-top: 4px; border-top: 1px solid var(--line); }
.stage .rollback-scope { margin: 14px 0 6px; }
.version-list { display: grid; }
.version-row .src { margin-left: auto; }
.version-row .ghost { flex: none; }
@media (max-width: 900px) {
  .layout { grid-template-columns: minmax(0, 1fr); gap: 14px; }
  .side { height: auto; }
}
@media (max-width: 760px) {
  .page { padding: 8px; }
  .layout { display: block; }
  .side.glass { display: block; height: auto; min-height: 0; max-height: 280px; overflow: auto; margin-bottom: 10px; padding: 10px 6px 14px; border-radius: 16px; }
  .statebar { min-height: 0; padding: 6px 10px; font-size: 10px; }
  .stage { width: 100%; overflow: hidden; }
  .stage :deep(.card.glass) { padding: 12px; overflow: auto; border-radius: 16px; }
  .package-grid { grid-template-columns: 1fr; }
  .package-card, .package-contents, .versions-card, .import-card { min-height: 0; }
  .confirm-box { align-items: flex-start; flex-direction: column; }
  .version-site { flex-wrap: wrap; }
  .version-line { align-items: flex-start; flex-direction: column; }
  .version-line .src { max-width: 100%; text-align: left; }
}
</style>
