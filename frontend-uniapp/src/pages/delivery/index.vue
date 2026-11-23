<template>
  <view class="page">
    <TopBar active="delivery" :project="projectName" meta="冻结 / 导入 / 回退，不等于部署" :pending="pendingAlarms.length" :alpha="42"
      @navigate="navigate" @bell="alarmOpen = true" @alpha="() => {}" />
    <view class="statebar">
      <span>项目 <b>{{ projectName }}</b></span><span>工作台 <b>交付</b></span><span>页面 <b>{{ currentLabel }}</b></span><span>EMS <b>{{ emsInstalled === null ? "—" : emsInstalled ? "已启用" : "未启用" }}</b></span>
    </view>
    <view class="layout">
      <aside class="side glass">
        <view class="nav-title">交付资产</view>
        <button v-for="item in assetTabs" :key="item.key" :class="{ active: current === item.key }" @click="current = item.key">{{ item.label }}</button>
        <view class="nav-title">系统</view>
        <button v-for="item in systemTabs" :key="item.key" :class="{ active: current === item.key }" @click="current = item.key">{{ item.label }}</button>
      </aside>
      <main class="stage">
        <template v-if="current === 'package'">
          <view class="step-head page-heading"><b>项目包与版本</b><span>这份工程是哪个版本、依赖什么？</span></view>
          <view class="package-grid">
            <section class="card glass package-card delivery-package">
              <view class="row"><span>当前版本</span><view class="version-site"><span>{{ versions[0]?.id || "未发布" }}</span><label class="site-field">Site<select v-model="selectedSite" class="site-select" :disabled="!sites.length"><option v-if="!sites.length" value="">没有可用 Site</option><option v-for="item in sites" :key="item.id" :value="item.id">{{ nameOf(item) }}</option></select></label></view></view>
              <view class="row"><span>依赖</span><span class="src">{{ packageDependencies }}</span></view>
              <view class="row"><span>现场绑定</span><span class="src">地址、凭据、控制启动与运行数据不进包</span></view>
              <view class="row"><span>预检</span><span v-if="gates" class="chip" :class="gates.status">{{ statusText(gates.status) }} · 通过 {{ gates.summary?.pass || 0 }} · 注意 {{ gates.summary?.warn || 0 }} · 阻断 {{ gates.summary?.fail || 0 }}</span><span v-else class="src">{{ gateError ? "读取失败" : "—" }}</span></view>
              <view class="actions">
                <button class="primary" :disabled="!selectedSite || !canConfigure || !!busy" @click="publish(false)">{{ busy === "publish" ? "冻结中…" : "冻结版本" }}</button>
                <button class="ghost" :disabled="!selectedSite || !canExport || !!busy" @click="exportPackage">{{ busy === "export" ? "导出中…" : "导出项目包" }}</button>
                <button class="ghost" :disabled="!canExport || !!busy" @click="exportEvidence">{{ busy === "evidence" ? "导出中…" : "导出证据 ZIP" }}</button>
                <label class="publish-note"><input v-model="publishNote" aria-label="发布说明（可选）" placeholder="发布说明（可选）" /></label>
              </view>
              <view v-if="publishBlocked" class="confirm-box">
                <span>预检存在阻断项。冻结只保存工程版本，不改变运行配置，也不等于部署。</span>
                <button class="danger" :disabled="busy === 'publish'" @click="publish(true)">预检未通过，仍要冻结</button>
              </view>
              <p class="note">部署到另一实例时必须重新绑定现场；冻结不改变运行配置，也不等于部署。</p>
            </section>
            <section class="card glass package-card package-contents">
              <view class="card-title">包内容 <span>{{ packageSummary?.id || "—" }}</span></view>
              <view class="row"><span>对象</span><span class="src">{{ packageSummary ? `${packageSummary.nodes?.length || 0} 个节点` : "—" }}</span></view>
              <view class="row"><span>画面</span><span class="src">{{ packageSummary ? `${packageSummary.screens?.length || 0} 份配置 · 绑定已 token 化` : "—" }}</span></view>
              <view class="row"><span>现场数据剥离</span><span class="src">{{ packageSummary ? `${packageSummary.stripped?.length || 0} 项绑定、凭据或运行数据` : "—" }}</span></view>
              <view class="row"><span>依赖</span><span class="src">{{ packageDependencies }}</span></view>
              <view v-if="packageSummary?.warnings?.length" class="package-warnings">{{ packageSummary.warnings.join("；") }}</view>
              <view v-if="packageError" class="error">{{ packageError }}</view>
            </section>
          </view>
          <view v-if="message" class="notice" :class="messageTone">{{ message }}</view>
          <section class="card glass versions-card">
            <view class="card-title">版本</view>
            <view v-if="versionError" class="error">{{ versionError }}</view>
            <view v-else-if="versions.length" class="version-lines">
              <view v-for="(version, index) in versions" :key="version.id" class="row version-line"><span class="version-id">{{ version.id }}</span><span class="src">{{ index === 0 ? "当前 · " : "" }}{{ clock(version.created_at) }} · {{ version.operator || "—" }} · {{ statusText(version.preflight_status) }} · {{ version.nodes }} 个节点{{ version.note ? ` · ${version.note}` : "" }}</span></view>
            </view>
            <view v-else class="empty compact-empty">{{ selectedSite ? "当前 Site 还没有冻结版本。" : "选择真实 Site 后才能读取版本；项目根不能代替 Site。" }}</view>
          </section>
          <section class="card glass import-card delivery-package">
            <view class="card-title">跨实例交付</view>
            <view class="row"><span>下一步</span><span class="src">在项目根下导入新 Site，再重新绑定现场</span></view>
            <view class="actions"><button class="ghost" :disabled="!projectRootId || !canConfigure || !!busy" @click="choosePackage">{{ busy === "import" ? "导入中…" : "导入到新实例" }}</button></view>
            <template v-if="importResult">
              <view class="import-result">
                <view class="row"><span>新 Site</span><span class="src">{{ importResult.name }} · {{ importResult.site_id }}</span></view>
                <view class="row"><span>导入结果</span><span class="src">{{ importResult.created }} 个节点 · {{ importResult.channels }} 条通道 · {{ importResult.rules }} 条规则</span></view>
                <view class="row"><span>基线版本</span><span class="src">{{ importResult.version_id || "未保存，查看导入警告" }}</span></view>
                <view v-if="importResult.warnings?.length" class="package-warnings">{{ importResult.warnings.join("；") }}</view>
              </view>
            </template>
            <p class="note">依赖不匹配会拒绝导入；地址与凭据须重新绑定，重绑完成前不能运行，控制不自动启动。</p>
          </section>
          <PreflightPanel :report="gates" :latest="latestPreflight" :error="gateError || latestError" :busy="busy === 'preflight'" :can-run="canConfigure" @run="runPreflight" />
        </template>

        <section v-else-if="current === 'deploy'" class="card glass">
          <view class="step-head"><b>部署记录</b><span>它现在跑在哪个现场？</span></view>
          <view class="gap-note">后端缺口：当前没有独立的“包版本 → 目标实例”部署记录 API。冻结、导入与回退都不能记成部署成功。</view>
          <view class="row"><span>本次页面导入状态</span><span class="src">{{ importStatus }}</span></view>
          <template v-if="importResult">
            <view class="row"><span>新 Site</span><span class="src">{{ importResult.name }} · {{ importResult.site_id }}</span></view>
            <view class="row"><span>导入结果</span><span class="src">{{ importResult.created }} 个节点 · {{ importResult.channels }} 条通道 · {{ importResult.rules }} 条规则</span></view>
            <view class="row"><span>基线版本</span><span class="src">{{ importResult.version_id || "未保存，查看导入警告" }}</span></view>
            <view v-if="importResult.warnings?.length" class="notice warn">{{ importResult.warnings.join("；") }}</view>
          </template>
          <p class="note">此处只显示本次真实导入响应，不显示“运行中”或“已部署”等后端没有记录的状态。</p>
        </section>

        <section v-else-if="current === 'rollback'" class="card glass">
          <view class="step-head"><b>版本回退</b><span>只回工程配置，不回历史与命令。</span></view>
          <view class="scope-grid">
            <view class="row"><span>恢复</span><span class="src">节点、点位、画面、策略与依赖声明</span></view>
            <view class="row"><span>保留</span><span class="src">运行历史、告警与审计；回退前自动冻结当前配置</span></view>
            <view class="row"><span>不做</span><span class="src">不回放旧命令，不自动恢复控制输出，不等于部署</span></view>
            <view class="row"><span>后端限制</span><span class="src">版本或当前工程包含计算点、虚拟输入点时拒绝回退</span></view>
          </view>
          <view v-if="rollbackMessage" class="notice" :class="rollbackTone">{{ rollbackMessage }}</view>
          <view v-if="versionError" class="error">{{ versionError }}</view>
          <view v-else-if="versions.length" class="version-list">
            <view v-for="version in versions" :key="version.id" class="version-row">
              <view><b>{{ version.id }}</b><span>{{ clock(version.created_at) }} · {{ version.operator || "—" }} · {{ version.nodes }} 个节点</span></view>
              <button class="ghost" :class="{ danger: rollbackConfirm === version.id }" :disabled="!selectedSite || !canConfigure || busy === 'rollback'" @click="rollback(version)">{{ rollbackConfirm === version.id ? "确认回退（历史保留）" : "回退到此版本" }}</button>
            </view>
          </view>
          <view v-else class="empty">{{ selectedSite ? "还没有可回退的版本。" : "请先选择真实 Site。" }}</view>
        </section>

        <AccountsPanel v-else-if="current === 'users'" :root-id="projectRootId" :nodes="session.snapshot?.nodes || []" :can-manage="canManageAccounts" />
        <AuditPanel v-else-if="current === 'audit'" :can-view="canViewAudit" />
        <IndustryPanel v-else :can-change="canSiteSettings" />
      </main>
    </view>
    <AlarmCenter v-if="alarmOpen" :alarms="session.alarms" @close="alarmOpen = false" @ack="acknowledge" @close-alarm="close" />
  </view>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue"
import TopBar from "../../components/TopBar.vue"
import AlarmCenter from "../../components/AlarmCenter.vue"
import PreflightPanel from "../../components/delivery/PreflightPanel.vue"
import AccountsPanel from "../../components/delivery/AccountsPanel.vue"
import AuditPanel from "../../components/delivery/AuditPanel.vue"
import IndustryPanel from "../../components/delivery/IndustryPanel.vue"
import { ApiError, downloadText, request } from "../../api/client"
import { acknowledgeAlarm, closeAlarm, nameOf, pendingAlarms, refreshAll, requireAuth, session, startPolling, stopPolling } from "../../stores/session"
import { canAction, clock, deliveryError, statusText } from "../../components/delivery/contracts"

type Gate = { key: string; title: string; status: string; detail: string; object?: string; next_step?: string; informational?: boolean }
type PreflightReport = { id?: string; generated_at?: string; operator?: string; status: string; summary: Record<string, number>; gates: Gate[] }
type VersionSummary = { id: string; site_id: string; site_name: string; created_at: string; operator: string; note?: string; preflight_status?: string; nodes: number }
type ProjectPackage = { id: string; name: string; dependencies?: { kind: string; id: string; version: string }[]; nodes?: unknown[]; screens?: unknown[]; stripped?: unknown[]; warnings?: string[] }
type ImportResult = { site_id: string; name: string; created: number; channels: number; rules: number; version_id?: string; warnings?: string[] }
type RollbackResult = { version: string; updated: number; created: number; extra: number; preflight_status?: string }

const current = ref("package")
const emsInstalled = ref<boolean | null>(null)
const selectedSite = ref("")
const gates = ref<PreflightReport | null>(null)
const latestPreflight = ref<PreflightReport | null>(null)
const gateError = ref("")
const latestError = ref("")
const versions = ref<VersionSummary[]>([])
const versionError = ref("")
const publishNote = ref("")
const publishBlocked = ref(false)
const message = ref("")
const messageTone = ref<"success" | "warn" | "error">("success")
const rollbackMessage = ref("")
const rollbackTone = ref<"success" | "warn" | "error">("success")
const rollbackConfirm = ref("")
const packageSummary = ref<ProjectPackage | null>(null)
const packageError = ref("")
const packageLoading = ref(false)
const importResult = ref<ImportResult | null>(null)
const importStatus = ref("未导入")
const busy = ref("")
const alarmOpen = ref(false)
let gateTimer: ReturnType<typeof setInterval> | undefined
const assetTabs = [{ key: "package", label: "项目包与版本" }, { key: "deploy", label: "部署记录" }, { key: "rollback", label: "版本回退" }]
const systemTabs = [{ key: "users", label: "账号与权限" }, { key: "audit", label: "审计" }, { key: "industry", label: "行业包" }]
const currentLabel = computed(() => [...assetTabs, ...systemTabs].find(item => item.key === current.value)?.label || "")
const sites = computed(() => session.snapshot?.nodes.filter(node => node.type === "site") || [])
const projectRootId = computed(() => session.snapshot?.roots[0] || "")
const projectName = computed(() => {
  const root = session.snapshot?.nodes.find(node => node.id === projectRootId.value)
  return root ? nameOf(root) : "未选择项目"
})
const siteName = computed(() => {
  const site = sites.value.find(node => node.id === selectedSite.value)
  return site ? nameOf(site) : ""
})
const packageDependencies = computed(() => {
  if (!selectedSite.value) return "—"
  if (!canExport.value) return "无导出权限"
  if (!packageSummary.value) return packageError.value ? "读取失败" : packageLoading.value ? "正在读取…" : "—"
  return packageSummary.value.dependencies?.length ? packageSummary.value.dependencies.map(dependencyText).join("；") : "无行业包或 Profile 依赖"
})
const canConfigure = computed(() => canAction(session.snapshot, "configure"))
const canExport = computed(() => canAction(session.snapshot, "export"))
const canManageAccounts = computed(() => canAction(session.snapshot, "account_manage"))
const canViewAudit = computed(() => canAction(session.snapshot, "audit_view"))
const canSiteSettings = computed(() => canAction(session.snapshot, "site_settings"))

watch(sites, list => {
  if (!list.some(node => node.id === selectedSite.value)) selectedSite.value = list[0]?.id || ""
}, { immediate: true })
watch(selectedSite, () => {
  gates.value = null; latestPreflight.value = null; versions.value = []
  gateError.value = ""; latestError.value = ""; versionError.value = ""
  publishBlocked.value = false; rollbackConfirm.value = ""; packageSummary.value = null
  packageError.value = ""; packageLoading.value = false
  message.value = ""; rollbackMessage.value = ""
  void loadSiteData(selectedSite.value)
})
watch(current, (next, previous) => { if (previous === "industry" && next !== "industry") void loadIndustryStatus() })
function queryFor(site: string) { return site ? `?site=${encodeURIComponent(site)}` : "" }
async function loadIndustryStatus() {
  try {
    const result = await request<{ packages: { id: string; installed: boolean }[] }>("/industry/packages")
    emsInstalled.value = result.packages?.find(item => item.id === "ems")?.installed || false
  } catch { emsInstalled.value = null }
}
async function loadSiteData(site: string) {
  await Promise.all([loadGates(site), loadLatest(site), loadVersions(site), loadPackage(site)])
}
async function loadPackage(site: string) {
  if (!site || !canExport.value) { packageSummary.value = null; packageError.value = ""; packageLoading.value = false; return }
  packageLoading.value = true; packageError.value = ""
  try {
    const result = await request<ProjectPackage>(`/projects/export${queryFor(site)}`)
    if (site === selectedSite.value) packageSummary.value = result
  } catch (error) {
    if (site === selectedSite.value) { packageSummary.value = null; packageError.value = deliveryError(error) }
  } finally {
    if (site === selectedSite.value) packageLoading.value = false
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
async function runPreflight() {
  const site = selectedSite.value
  busy.value = "preflight"; gateError.value = ""; latestError.value = ""
  try {
    const result = await request<PreflightReport>(`/delivery/preflight${queryFor(site)}`, "POST")
    if (site === selectedSite.value) latestPreflight.value = result
    await loadGates(site)
  } catch (error) { gateError.value = deliveryError(error) } finally { busy.value = "" }
}
async function publish(confirm: boolean) {
  const site = selectedSite.value
  if (!site) { message.value = "请选择真实 Site"; messageTone.value = "error"; return }
  busy.value = "publish"; message.value = ""
  try {
    const result = await request<VersionSummary>("/projects/publish", "POST", { site, note: publishNote.value.trim(), confirm })
    message.value = `已冻结版本 ${result.id}（${result.nodes} 个节点）；未改变运行配置，不等于部署`
    messageTone.value = "success"; publishNote.value = ""; publishBlocked.value = false
    await loadVersions(site)
  } catch (error) {
    messageTone.value = "error"; message.value = deliveryError(error)
    if (error instanceof ApiError && error.status === 409 && error.code === "preflight_failed") { publishBlocked.value = true; messageTone.value = "warn" }
  } finally { busy.value = "" }
}
function dependencyText(item: { kind: string; id: string; version: string }) {
  return `${item.kind === "industry" ? "行业包" : "Profile"} ${item.id}@${item.version}`
}
async function exportPackage() {
  const site = selectedSite.value
  if (!site) return
  busy.value = "export"; message.value = ""; packageError.value = ""
  try {
    const result = await request<ProjectPackage>(`/projects/export${queryFor(site)}`)
    const filename = `${(result.name || siteName.value || "project").replace(/[\\/:*?"<>|]/g, "-")}.package.json`
    downloadText(filename, JSON.stringify(result, null, 2))
    packageSummary.value = result
    message.value = `已导出后端项目包 ${result.id}；不含地址、凭据、控制启动与运行状态`
    messageTone.value = "success"
  } catch (error) { message.value = deliveryError(error); messageTone.value = "error" } finally { busy.value = "" }
}
function choosePackage() {
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
    importStatus.value = `已导入 ${result.name}；不是部署`
    message.value = `已导入「${result.name}」：${result.created} 个节点、${result.channels} 条通道、${result.rules} 条规则${result.version_id ? `；基线 ${result.version_id}` : ""}。需重新绑定地址与凭据，控制不自动启动${result.warnings?.length ? `。警告：${result.warnings.join("；")}` : ""}`
    messageTone.value = result.warnings?.length ? "warn" : "success"
    await refreshAll()
  } catch (error) { message.value = deliveryError(error); messageTone.value = "error" } finally { busy.value = "" }
}
async function exportEvidence() {
  busy.value = "evidence"; message.value = ""
  try {
    const response = await new Promise<UniApp.RequestSuccessCallbackResult>((resolve, reject) => {
      uni.request({ url: "/api/delivery/evidence", method: "GET", responseType: "arraybuffer", header: { Authorization: `Bearer ${uni.getStorageSync("fastems-token")}` }, success: resolve, fail: reject })
    })
    const data = response.data as ArrayBuffer
    if (response.statusCode < 200 || response.statusCode >= 300) {
      let code = ""
      try { code = JSON.parse(new TextDecoder().decode(data)).error || "" } catch { /* non-JSON error */ }
      throw new ApiError(code || `证据导出失败（${response.statusCode}）`, response.statusCode, code)
    }
    const bytes = new Uint8Array(data)
    if (bytes[0] !== 0x50 || bytes[1] !== 0x4b) throw new Error("后端未返回有效 ZIP 文件")
    const url = URL.createObjectURL(new Blob([data], { type: "application/zip" }))
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
    rollbackMessage.value = `已回退到 ${result.version}：更新 ${result.updated}、创建 ${result.created}、保留多余 ${result.extra}；历史未回退，旧命令未回放，不等于部署`
    rollbackTone.value = "success"; rollbackConfirm.value = ""
    await loadVersions(site)
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
.statebar { display: flex; align-items: center; gap: 10px; height: 24px; padding: 0 18px; color: var(--faint); background: rgba(255,255,255,.24); font-size: 11.5px; line-height: 1.2; }
.statebar b { color: var(--muted); font-weight: 600; }
.layout { display: grid; grid-template-columns: 272px minmax(0, 1fr); gap: 14px; margin-top: 14px; }
.side { height: calc(100vh - 120px); padding: 14px 10px 30px; align-self: start; }
.nav-title { margin: 0 0 4px; padding: 0 8px; color: var(--faint); font-size: 11px; letter-spacing: .12em; line-height: 1.2; }
.side button { display: block; width: 100%; min-height: 31px; margin-bottom: 1px; border: 0; border-radius: 8px; background: transparent; color: var(--muted); padding: 7px 12px; text-align: left; font-size: 13px; }
.side button.active { background: rgba(14,124,123,.14); color: var(--accent); font-weight: 650; }
.stage { display: grid; gap: 12px; min-width: 0; align-content: start; }
.card { min-width: 0; padding: 16px; }
.side.glass, .package-grid > .card.glass, .versions-card.glass, .import-card.glass {
  background: linear-gradient(135deg,rgba(255,255,255,.32),rgba(255,255,255,.04)), rgba(255,255,255,.48);
  backdrop-filter: blur(28px) saturate(1.45);
  -webkit-backdrop-filter: blur(28px) saturate(1.45);
  border: 2px solid rgba(255,255,255,.78);
  border-radius: 18px;
  box-shadow: 0 20px 45px rgba(45,83,110,.13), inset 0 0 0 1px rgba(255,255,255,.22), inset 0 0 18px rgba(255,255,255,.22);
}
.side.glass { border-radius: 20px; }
.package-contents .card-title, .versions-card .card-title, .import-card .card-title { margin: 0 0 10px; font-size: 12px; font-weight: 600; letter-spacing: .06em; }
.step-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; margin-bottom: 12px; }
.step-head b { font-size: 16px; }
.step-head span, .note { color: var(--faint); font-size: 12px; }
.page-heading { margin: 0 2px; }
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
.version-id { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 11.5px; overflow-wrap: anywhere; }
.import-card { min-height: 170px; }
.import-card .ghost { min-height: 31px; }
.import-result { margin-top: 10px; padding-top: 4px; border-top: 1px solid var(--line); }
.import-card .note, .package-card .note { margin: 10px 0 0; line-height: 1.7; }
.gap-note { margin-bottom: 12px; padding: 12px; border: 1px solid rgba(164,99,27,.28); border-radius: 10px; background: rgba(253,247,234,.65); color: var(--amber); font-size: 12.5px; line-height: 1.7; }
.scope-grid { margin-bottom: 12px; }
.version-list { display: grid; gap: 8px; }
.version-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 11px 12px; border: 1px solid var(--line); border-radius: 10px; background: rgba(255,255,255,.3); }
.version-row > view { display: grid; gap: 4px; min-width: 0; }
.version-row b { font-size: 12.5px; overflow-wrap: anywhere; }
.version-row span { color: var(--faint); font-size: 11.5px; }
@media (max-width: 900px) { .layout { grid-template-columns: 178px minmax(0, 1fr); gap: 10px; } .page { padding: 10px; } }
@media (max-width: 760px) {
  .layout { display: block; }
  .side { display: flex; height: auto; min-height: 0; overflow-x: auto; gap: 4px; margin-bottom: 10px; }
  .side button { white-space: nowrap; }
  .nav-title { display: none; }
  .statebar { height: auto; min-height: 20px; padding: 3px 10px; flex-wrap: wrap; font-size: 10px; }
  .package-grid { grid-template-columns: 1fr; }
  .package-card, .package-contents, .versions-card, .import-card { min-height: 0; }
  .confirm-box, .version-row { align-items: flex-start; flex-direction: column; }
  .version-site { flex-wrap: wrap; }
  .version-line { align-items: flex-start; flex-direction: column; }
  .version-line .src { max-width: 100%; text-align: left; }
}
</style>
