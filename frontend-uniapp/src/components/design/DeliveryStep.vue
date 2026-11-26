<template>
  <view class="delivery-step">
    <view class="step-head"><b>第 6 步 · 交付</b><span>把这份工程变成可复现的包，交给下一位同事或客户。</span></view>
    <section class="card glass step-card">
      <view class="row"><span>当前版本</span><view class="version-site"><span>{{ currentVersion?.id || "未发布" }}</span><label class="site-field">Site<select v-model="selectedSite" :disabled="!sites.length || !!busy"><option v-if="!sites.length" value="">没有可用 Site</option><option v-for="site in sites" :key="site.id" :value="site.id">{{ nameOf(site) }}</option></select></label></view></view>
      <view class="row"><span>依赖</span><span class="src">{{ packageDependencies }}</span></view>
      <view class="row"><span>部署目标</span><span>{{ selectedSiteName || "—" }}</span></view>

      <h5 class="section-title">项目包里有什么</h5>
      <table class="mini-table"><tbody>
        <tr><td>对象</td><td class="src">{{ packageSummary ? `${showCount(packageSummary.nodes?.length)} 个节点 · ${packageSummary.devices ?? "—"} 台设备 · ${packageSummary.bound_devices ?? "—"} 台已绑定` : "—" }}</td></tr>
        <tr><td>点位</td><td class="src">{{ packageSummary ? `${showCount(packageSummary.point_count)} 个采集点 · 画面绑定已 token 化` : "—" }}</td></tr>
        <tr><td>画面</td><td class="src">{{ packageSummary ? `${showCount(packageSummary.screen_count ?? packageSummary.screens?.length)} 份本层配置 · 其余继承或默认` : "—" }}</td></tr>
        <tr><td>策略</td><td class="src">{{ packageSummary ? `${packageSummary.rules ?? "—"} 条规则 · ${packageSummary.state_machines ?? "—"} 个状态机` : "—" }}</td></tr>
        <tr><td>现场绑定</td><td class="src">已剥离：地址、凭据、控制启动与运行数据不进包</td></tr>
      </tbody></table>
      <p v-if="packageError" class="error" role="alert">{{ packageError }}</p>
      <p v-if="versionError" class="error" role="alert">{{ versionError }}</p>
      <p v-if="packageSummary?.warnings?.length" class="notice warn">{{ packageSummary.warnings.join("；") }}</p>

      <view class="section-heading"><h5 class="section-title">交付前检查</h5><button class="ghost" :disabled="!selectedSite || !canConfigure || !!busy" @click="runPreflight">{{ busy === "preflight" ? "预检中…" : "运行预检" }}</button></view>
      <template v-if="gates?.gates?.length">
        <view v-for="gate in gates.gates" :key="gate.key" class="row"><span>{{ gate.title }}<small v-if="gate.detail">{{ gate.detail }}</small></span><span class="chip" :class="gate.status">{{ statusText(gate.status) }}</span></view>
      </template>
      <view v-else class="row"><span>预检</span><span class="src">{{ gateError || "后端尚未返回检查项" }}</span></view>

      <h5 class="section-title">部署记录</h5>
      <div class="table-wrap"><table class="mini-table deploy-table"><thead><tr><th>目标</th><th>版本</th><th>时间</th><th>人</th><th>状态</th></tr></thead><tbody><tr><td colspan="5" class="empty">暂无部署记录：后端没有独立的“包版本 → 目标实例”部署记录 API。发布、导入与回退都不能记成部署成功。</td></tr></tbody></table></div>
      <view class="tool-line"><span>历史留多久</span><button class="ghost" @click="emit('tool', 'retention')">数据保留 →</button></view>
      <p class="note">发布只冻结工程配置，不改变运行配置，也不等于部署；跨实例导入后必须重新绑定地址与凭据。</p>
      <p v-if="message" class="notice" :class="messageTone" role="status">{{ message }}</p>
      <view v-if="publishBlocked" class="confirm-box" role="alert"><span>预检存在阻断项，不能发布可交付版本；请修复后重新预检。</span><button class="ghost" :disabled="!!busy" @click="runPreflight">重新预检</button></view>
      <div class="actions"><button class="primary" :disabled="!selectedSite || !canConfigure || !!busy" @click="publish()">{{ busy === "publish" ? "发布中…" : versions.length ? "重新发布" : "发布版本" }}</button><button class="ghost" :disabled="!selectedSite || !currentVersion || !canExport || !!busy" @click="exportPackage">{{ busy === "export" ? "导出中…" : "导出项目包" }}</button><button class="ghost" @click="emit('navigate', 'delivery')">去交付页</button></div>
    </section>
  </view>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue"
import { ApiError, downloadText, request } from "../../api/client"
import { nameOf } from "../../types/api"
import type { Snapshot } from "../../types/api"
import { canAction, countText, deliveryError, statusText } from "../delivery/contracts"

type Gate = { key: string; title: string; status: string; detail: string; object?: string; next_step?: string; informational?: boolean }
type PreflightReport = { id?: string; site?: string; generated_at?: string; operator?: string; status: string; summary: Record<string, number>; gates: Gate[]; fingerprint?: string }
type VersionSummary = { id: string; site_id: string; site_name: string; created_at: string; operator: string; note?: string; preflight_status?: string; nodes: number; deliverable?: boolean }
type ProjectPackage = { id: string; name: string; point_count?: number; strategy_count?: number; devices?: number; bound_devices?: number; rules?: number; state_machines?: number; screen_count?: number; dependencies?: { kind: string; id: string; version: string }[]; nodes?: unknown[]; screens?: unknown[]; warnings?: string[] }

const props = defineProps<{ snapshot: Snapshot | null }>()
const emit = defineEmits<{ (event: "navigate", value: "delivery"): void; (event: "status", value: boolean): void; (event: "tool", value: string): void }>()
const selectedSite = ref("")
const versions = ref<VersionSummary[]>([])
const currentVersion = computed(() => versions.value.find(version => version.deliverable))
const packageSummary = ref<ProjectPackage | null>(null)
const showCount = countText
const gates = ref<PreflightReport | null>(null)
const versionError = ref("")
const packageError = ref("")
const gateError = ref("")
const message = ref("")
const messageTone = ref<"success" | "warn" | "error">("success")
const publishBlocked = ref(false)
const busy = ref("")
const sites = computed(() => props.snapshot?.nodes.filter(node => node.type === "site") || [])
const selectedSiteName = computed(() => {
  const site = sites.value.find(node => node.id === selectedSite.value)
  return site ? nameOf(site) : ""
})
const canConfigure = computed(() => canAction(props.snapshot, "configure"))
const canExport = computed(() => canAction(props.snapshot, "export"))
const packageDependencies = computed(() => {
  if (!selectedSite.value) return "—"
  if (!canExport.value) return "无导出权限"
  if (!packageSummary.value) return packageError.value ? "读取失败" : "—"
  return packageSummary.value.dependencies?.length ? packageSummary.value.dependencies.map(item => `${item.kind === "industry" ? "行业包" : "Profile"} ${item.id}@${item.version}`).join("；") : "无行业包或 Profile 依赖"
})

watch(sites, list => { if (!list.some(node => node.id === selectedSite.value)) selectedSite.value = list[0]?.id || "" }, { immediate: true })
watch(selectedSite, site => {
  versions.value = []; packageSummary.value = null; gates.value = null
  versionError.value = ""; packageError.value = ""; gateError.value = ""; message.value = ""; publishBlocked.value = false
  void loadSiteData(site)
}, { immediate: true })
watch(versions, () => emit("status", !!currentVersion.value), { immediate: true })
function queryFor(site: string) { return site ? `?site=${encodeURIComponent(site)}` : "" }
async function loadSiteData(site: string) { await Promise.all([loadVersions(site), loadGates(site)]); if (site === selectedSite.value) await loadPackage(site) }
async function loadVersions(site: string) {
  if (!site) { versions.value = []; return }
  try {
    const result = await request<{ versions: VersionSummary[] }>(`/projects/versions${queryFor(site)}`)
    if (site === selectedSite.value) { versions.value = result.versions || []; versionError.value = "" }
  } catch (error) { if (site === selectedSite.value) { versions.value = []; versionError.value = deliveryError(error) } }
}
async function loadPackage(site: string) {
  if (!site || !canExport.value) { packageSummary.value = null; packageError.value = ""; return }
  const versionID = currentVersion.value?.id
  packageSummary.value = null
  try {
    const result = await request<ProjectPackage>(`/projects/export${queryFor(site)}${versionID ? `&version=${encodeURIComponent(versionID)}` : ""}`)
    if (site === selectedSite.value && currentVersion.value?.id === versionID) { packageSummary.value = result; packageError.value = "" }
  } catch (error) { if (site === selectedSite.value && currentVersion.value?.id === versionID) { packageSummary.value = null; packageError.value = deliveryError(error) } }
}
async function loadGates(site: string) {
  if (!site) { gates.value = null; return }
  try {
    const result = await request<PreflightReport>(`/delivery/gates${queryFor(site)}`)
    if (site === selectedSite.value) { gates.value = result; gateError.value = "" }
  } catch (error) { if (site === selectedSite.value) { gates.value = null; gateError.value = deliveryError(error) } }
}
async function runPreflight() {
  const site = selectedSite.value
  if (!site) return
  busy.value = "preflight"; gateError.value = ""
  try {
    const result = await request<PreflightReport>(`/delivery/preflight${queryFor(site)}`, "POST")
    if (site === selectedSite.value) {
      gates.value = result
      if (result.status !== "fail" && publishBlocked.value) { publishBlocked.value = false; message.value = "" }
    }
    await loadGates(site)
  } catch (error) { gateError.value = deliveryError(error) } finally { busy.value = "" }
}
async function publish() {
  const site = selectedSite.value
  if (!site) return
  busy.value = "publish"; message.value = ""
  try {
    const result = await request<VersionSummary>("/projects/publish", "POST", { site, note: "", confirm: false, expected_fingerprint: gates.value?.fingerprint })
    message.value = `已冻结版本 ${result.id}（${result.nodes} 个节点）；未改变运行配置，不等于部署`
    messageTone.value = "success"; publishBlocked.value = false
    await loadVersions(site)
    await loadPackage(site)
  } catch (error) {
    message.value = deliveryError(error); messageTone.value = "error"
    if (error instanceof ApiError && error.status === 409 && error.code === "preflight_failed") { publishBlocked.value = true; messageTone.value = "warn" }
  } finally { busy.value = "" }
}
async function exportPackage() {
  const site = selectedSite.value
  const versionID = currentVersion.value?.id
  if (!site || !versionID) return
  busy.value = "export"; message.value = ""; packageError.value = ""
  try {
    const result = await request<ProjectPackage>(`/projects/export${queryFor(site)}${versionID ? `&version=${encodeURIComponent(versionID)}` : ""}`)
    if (site !== selectedSite.value || currentVersion.value?.id !== versionID) return
    const filename = `${(result.name || selectedSiteName.value || "project").replace(/[\\/:*?"<>|]/g, "-")}.package.json`
    downloadText(filename, JSON.stringify(result, null, 2))
    packageSummary.value = result
    message.value = `已导出后端项目包 ${result.id}；不含地址、凭据、控制启动与运行状态`
    messageTone.value = "success"
  } catch (error) { message.value = deliveryError(error); messageTone.value = "error" } finally { busy.value = "" }
}
</script>

<style scoped>
.tool-line { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-top: 12px; color: var(--faint); font-size: 12px; }
.tool-line uni-button { margin: 0; min-height: 25px; padding: 2px 8px; font-size: 11px; }
.delivery-step { min-width: 0; }
.step-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; margin-bottom: 12px; }
.step-head b { font-size: 16px; }.step-head span, .note, .src { color: var(--faint); font-size: 12px; }
.step-card { padding: 16px; }
.section-heading { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-top: 14px; }
.section-heading .section-title { margin-top: 0; }
.section-title { margin: 14px 0 6px; color: var(--faint); font-size: 12px; letter-spacing: .06em; }
.version-site { display: flex; align-items: center; justify-content: flex-end; gap: 10px; min-width: 0; font-size: 12px; }
.site-field { display: flex; align-items: center; gap: 5px; color: var(--faint); font-size: 11px; }
.site-field select { width: 112px; height: 24px; min-height: 24px; padding: 0 5px; border: 1px solid var(--line); border-radius: 8px; background: rgba(255,255,255,.58); font-size: 11px; }
.row small { display: block; margin-top: 3px; color: var(--faint); font-size: 11px; }
.mini-table td { vertical-align: top; }
.note { margin-top: 10px; line-height: 1.7; }
.notice { margin: 10px 0 0; padding: 10px 12px; border-radius: 10px; font-size: 12.5px; line-height: 1.7; }
.notice.success { background: rgba(230,243,238,.8); color: var(--mint); }.notice.warn { background: rgba(253,247,234,.85); color: var(--amber); }.notice.error { background: rgba(253,241,238,.85); color: var(--danger); }
.confirm-box { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 12px; padding: 11px 12px; border: 1px solid rgba(164,99,27,.28); border-radius: 10px; background: rgba(253,247,234,.7); color: var(--amber); font-size: 12px; }
.danger { border: 1px solid rgba(178,58,50,.35); border-radius: 9px; padding: 8px 12px; background: rgba(253,241,238,.85); color: var(--danger); }
.empty { padding: 14px 10px; color: var(--faint); }
@media (max-width: 760px) { .mini-table { min-width: 560px; }.version-site { align-items: flex-end; flex-direction: column; }.confirm-box { align-items: flex-start; flex-direction: column; } }
</style>
