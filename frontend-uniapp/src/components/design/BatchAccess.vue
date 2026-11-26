<template>
  <view class="batch-access">
    <view class="panel-head"><strong>批量接入 · 设备占位</strong><span>已选 {{ checked.size }} 台未绑定设备；批量只保存映射，验证仍逐台进行。</span><span class="spacer"></span><button class="ghost" @click="emit('close')">退出批量</button></view>
    <div class="table-wrap"><table><thead><tr><th></th><th>设备</th><th>状态</th></tr></thead><tbody>
      <tr v-for="device in devices" :key="device.id"><td><checkbox :checked="checked.has(device.id)" :disabled="device.bound || readOnly || busy !== ''" @click="toggle(device.id)" /></td><td>{{ device.name }}<small>{{ device.id }}</small></td><td><span class="chip" :class="device.bound ? 'ok' : 'warn'">{{ device.bound ? "已绑定" : "未绑定" }}</span></td></tr>
      <tr v-if="!devices.length"><td colspan="3" class="empty">当前没有设备占位；请先在“搭台账”创建设备。</td></tr>
    </tbody></table></div>
    <view class="batch-form">
      <label>通道<select v-model="channelID" :disabled="readOnly || busy !== ''" @change="invalidate"><option v-for="channel in channels" :key="channel.id" :value="channel.id">{{ channelName(channel.id) }}</option></select></label>
      <label>Profile<select v-model="profileID" :disabled="readOnly || busy !== ''" @change="invalidate"><option v-for="profile in usableProfiles" :key="profile.id" :value="profile.id">{{ profile.vendor }} {{ profile.model }}</option></select></label>
      <label>起始站号<input v-model.number="startStation" type="number" min="1" max="247" :disabled="readOnly || busy !== ''" @input="invalidate"></label>
      <button class="ghost" :disabled="readOnly || !checked.size || !channelID || !profileID || busy !== ''" @click="makeDraft">生成批量草稿</button>
    </view>
    <label class="csv-label">CSV 草稿（设备,型号,通道,站号）<textarea v-model="csv" :maxlength="262145" rows="6" :disabled="readOnly || busy !== ''" @blur="syncCSV"></textarea></label>
    <p class="notice">每批最多 256 台、CSV 256 KiB、每字段 256 字节；超限会拒绝，不会截断后导入。</p>
    <p v-if="csvError" class="error" role="alert">{{ csvError }}</p>
    <view class="actions"><button class="ghost" :disabled="readOnly || !!csvError || !csv.trim() || busy !== ''" @click="preview">{{ busy === 'preview' ? '预检中…' : 'CSV 预检' }}</button><button class="primary" :disabled="readOnly || previewedCSV !== csv || !previewVersion || !rows.some(row => row.valid) || busy !== ''" @click="apply">{{ busy === 'apply' ? '套用中…' : '批量套用可导入项' }}</button></view>
    <p v-if="error" class="error" role="alert">{{ error }}</p><p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <div v-if="rows.length" class="table-wrap"><table><thead><tr><th>行</th><th>设备</th><th>通道 / 站号</th><th>点位</th><th>结果</th></tr></thead><tbody>
      <tr v-for="row in rows" :key="row.line"><td>{{ row.line }}</td><td>{{ deviceName(row.device_id) || row.device }}</td><td>{{ channelName(row.channel_id) || row.channel }} / {{ row.station || '—' }}</td><td>{{ row.status === 'mapped' ? `${row.mapped} 已映射${row.missing.length ? `，缺 ${row.missing.length}` : ''}` : `${row.created_points} 个待创建` }}<details v-if="plans[row.line]" class="batch-diff"><summary>Profile 差异 / 创建计划</summary><p>当前缺失必需点：{{ plans[row.line].missing_required.join('、') || '无' }}；应用后仍需实际读值验证。</p><p v-for="point in plans[row.line].match.points" :key="point.name">{{ point.name }}：{{ point.reason || point.verdict }}</p><p v-for="point in plans[row.line].create_points" :key="point.description">新建 {{ point.description }} · 地址 {{ point.address }} · {{ point.data_format }} · 倍率 {{ point.scale }} · {{ point.units || '无单位' }} · {{ point.read_only ? '只读' : '可写（本次不写设备）' }}</p></details></td><td><span class="chip" :class="row.status === 'mapped' ? 'ok' : row.valid ? '' : 'warn'">{{ statusText(row) }}</span><small v-if="row.errors.length">{{ row.errors.join('；') }}</small></td></tr>
    </tbody></table></div>
    <view class="panel-head"><strong>已保存映射 · 逐台验证</strong><span>读取同一设备模型及验证记录；应用映射不代表 L1/L2 通过。</span><button class="ghost" :disabled="busy !== ''" @click="refreshSaved">刷新已保存结果</button><button class="primary" :disabled="readOnly || busy !== '' || !saved.length" @click="verifyAll">{{ busy === 'verify-all' ? '全部验证中…' : `全部验证（${saved.length} 台已套用）` }}</button></view>
    <p v-if="batchProgress" class="notice" role="status">{{ batchProgress }}</p>
    <p v-if="batchSummary" class="notice" role="status">{{ batchSummary.text }}</p>
    <ul v-if="batchSummary?.failures.length" class="batch-failures" role="alert"><li v-for="failure in batchSummary.failures" :key="failure">{{ failure }}</li></ul>
    <p v-if="savedError" class="error" role="alert">{{ savedError }}</p>
    <div class="table-wrap saved-bindings"><table><thead><tr><th>设备 / 站号</th><th>保存结果与证据</th><th>只读验证</th></tr></thead><tbody>
      <tr v-for="item in saved" :key="bindingPath(item.device)"><td>{{ item.device.business_node_name || item.device.name }}<small>{{ channelName(item.device.channel_id) }} / {{ item.device.server_id }}</small></td><td><span>映射已保存 · {{ item.device.point_count }} 个物理点</span><small v-for="level in levels" :key="level">{{ batchVerificationText(item.records, level) }}</small><small v-if="item.error" class="error" role="alert">{{ item.error }}</small></td><td><button v-for="level in levels" :key="level" class="ghost" :disabled="readOnly || busy !== ''" @click="verify(item, level)">{{ level === 'L1' ? 'L1 只读验证' : 'L2 必需点验证' }}</button></td></tr>
      <tr v-if="!saved.length"><td colspan="3" class="empty">尚无已保存的设备映射；不会用草稿冒充保存结果。</td></tr>
    </tbody></table></div>
  </view>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue"
import { request } from "../../api/client"
import { nameOf } from "../../types/api"
import type { Snapshot } from "../../types/api"
import { batchCSV, batchVerificationOutcome, batchVerificationText, boundBusinessIDs, summarizeBatchVerification } from "./access"
import type { BatchAccessResponse, BatchBindingRow, BatchVerificationOutcome, BatchVerificationRecord, BatchVerificationSummary, LedgerDevice, EmsChannel, Profile } from "./access"

const props = withDefaults(defineProps<{ snapshot: Snapshot | null; channels: EmsChannel[]; profiles: Profile[]; selectedNodeIds?: string[]; readOnly?: boolean }>(), { selectedNodeIds: () => [], readOnly: false })
const emit = defineEmits<{ (event: "refresh"): void; (event: "close"): void }>()
const checked = ref(new Set<string>())
const channelID = ref("")
const profileID = ref("")
const startStation = ref(1)
const csv = ref("")
const csvError = computed(() => new TextEncoder().encode(csv.value).length > 256 * 1024 ? "CSV 超过 256 KiB，请拆分后重新粘贴；本次不会导入" : "")
const rows = ref<BatchBindingRow[]>([])
const previewedCSV = ref("")
const previewVersion = ref("")
const plans = ref<NonNullable<BatchAccessResponse["plans"]>>({})
type SavedBinding = { device: LedgerDevice; records: BatchVerificationRecord[]; error: string }
const saved = ref<SavedBinding[]>([])
const savedError = ref("")
const batchSummary = ref<BatchVerificationSummary | null>(null)
const batchProgress = ref("")
const levels = ["L1", "L2"] as const
const busy = ref("")
const error = ref("")
const notice = ref("")
const boundIDs = computed(() => boundBusinessIDs(props.snapshot?.nodes || []))
const devices = computed(() => (props.snapshot?.nodes || []).filter(node => node.type === "device" && node.parent !== "root").map(node => ({ id: node.id, name: nameOf(node), bound: boundIDs.value.has(node.id) })))
const usableProfiles = computed(() => props.profiles.filter(profile => (profile.point_specs?.length || 0) > 0))
watch(() => props.selectedNodeIds, ids => {
  checked.value = new Set(ids.filter(id => devices.value.some(device => device.id === id && !device.bound)))
}, { immediate: true })
watch([() => props.channels, () => props.profiles], () => {
  if (!channelID.value) channelID.value = props.channels[0]?.id || ""
  if (!profileID.value) profileID.value = usableProfiles.value[0]?.id || ""
}, { immediate: true, deep: true })
// uni-app throttles input events; invalidate on changed text, not delayed repeats.
watch(csv, invalidate, { flush: "sync" })
function syncCSV(event: { detail?: unknown }) {
  const detail = event.detail
  if (detail && typeof detail === "object" && "value" in detail && typeof detail.value === "string") csv.value = detail.value
}
function toggle(id: string) {
  const next = new Set(checked.value)
  if (next.has(id)) next.delete(id); else next.add(id)
  checked.value = next
  invalidate()
}
function invalidate() { rows.value = []; plans.value = {}; previewedCSV.value = ""; previewVersion.value = ""; notice.value = ""; batchSummary.value = null }
function makeDraft() {
  if (!Number.isInteger(startStation.value) || startStation.value < 1 || startStation.value > 247) { error.value = "起始站号必须是 1–247 的整数"; return }
  const selected = devices.value.filter(device => checked.value.has(device.id) && !device.bound)
  csv.value = batchCSV(selected, profileID.value, channelID.value, startStation.value)
  invalidate()
  error.value = ""
}
function channelName(id?: string) { return id ? nameOf(props.snapshot?.nodes.find(node => node.id === id) || { id, type: "", parent: "", points: [] }) : "" }
function deviceName(id?: string) { return id ? devices.value.find(device => device.id === id)?.name || id : "" }
function statusText(row: BatchBindingRow) { return row.status === "mapped" ? "映射已保存，待验证" : row.status === "ready" ? "可导入" : row.status === "blocked" ? "预检阻断" : "套用失败" }
async function run(dryRun: boolean) {
  if (csvError.value) { error.value = csvError.value; return }
  const submittedCSV = csv.value
  busy.value = dryRun ? "preview" : "apply"
  error.value = ""
  notice.value = ""
  try {
    const result = await request<BatchAccessResponse>("/access/batch/reviewed", "POST", { csv: submittedCSV, dry_run: dryRun, preview_version: dryRun ? undefined : previewVersion.value })
    if (dryRun && csv.value !== submittedCSV) { error.value = "CSV 草稿已改变，请重新预检"; return }
    rows.value = result.rows || []
    plans.value = result.plans || {}
    if (dryRun) { previewedCSV.value = csv.value; previewVersion.value = result.preview_version || "" }
    else {
      previewedCSV.value = ""
      previewVersion.value = ""
      notice.value = `已保存 ${result.applied} 台设备映射；阻断 ${rows.value.filter(row => row.status === 'blocked').length} 台，失败 ${rows.value.filter(row => row.status === 'failed').length} 台；未执行验证，请逐台完成 L1/L2`
      for (const row of rows.value) if (row.status === "mapped" && row.device_id) checked.value.delete(row.device_id)
      checked.value = new Set(checked.value)
      emit("refresh")
      await loadSaved()
    }
  } catch (cause) {
    previewVersion.value = ""
    const code = cause instanceof Error ? cause.message : "批量操作失败"
    const message = ({ batch_csv_too_large: "CSV 超过 256 KiB", batch_csv_too_many_rows: "每批最多 256 台，请拆分 CSV", batch_csv_field_too_large: "CSV 每个字段最多 256 字节", invalid_csv: "CSV 格式错误，每行须有四个字段", invalid_csv_header: "CSV 表头须包含设备、型号、通道、站号且不得重复" } as Record<string, string>)[code] || code
    error.value = message === "batch_preview_changed" ? "Profile、映射、通道或可见范围已变化，请重新预检；本次未应用" : `${message}${dryRun ? '' : '；结果未确认，请刷新已保存结果后重新预检，勿当作整批成功'}`
    if (!dryRun) await loadSaved()
  } finally { busy.value = "" }
}
async function preview() { await run(true) }
async function apply() { await run(false) }
function bindingPath(device: LedgerDevice) { return `/modbus/${encodeURIComponent(device.channel_id)}/units/${device.server_id}` }
async function loadSaved() {
  savedError.value = ""
  batchSummary.value = null
  try {
    const result = await request<{ devices: LedgerDevice[] }>("/device-ledger")
    saved.value = await Promise.all((result.devices || []).filter(device => devices.value.some(item => item.id === device.business_node_id)).map(async device => {
      try {
        const history = await request<{ records: BatchVerificationRecord[] }>(`${bindingPath(device)}/verify`)
        return { device, records: history.records || [], error: "" }
      } catch (cause) { return { device, records: [], error: `验证记录读取失败：${cause instanceof Error ? cause.message : '未知错误'}` } }
    }))
  } catch (cause) {
    saved.value = []
    savedError.value = `保存结果读取失败：${cause instanceof Error ? cause.message : '未知错误'}`
  }
}
async function refreshSaved() { busy.value = "reload"; try { await loadSaved() } finally { busy.value = "" } }
async function runVerification(item: SavedBinding, level: "L1" | "L2") {
  await request(`${bindingPath(item.device)}/verify`, "POST", { level })
  const history = await request<{ records: BatchVerificationRecord[] }>(`${bindingPath(item.device)}/verify`)
  item.records = history.records || []
}
function verificationFailure(level: "L1" | "L2", cause: unknown) {
  return `${level} 结果未确认：${cause instanceof Error ? cause.message : '请求失败'}；请刷新记录，不代表验证通过`
}
async function verify(item: SavedBinding, level: "L1" | "L2") {
  busy.value = `verify-${level}`
  item.error = ""
  batchSummary.value = null
  try { await runVerification(item, level) }
  catch (cause) { item.error = verificationFailure(level, cause) }
  finally { busy.value = "" }
}
async function verifyAll() {
  if (props.readOnly || !saved.value.length || busy.value !== "") return
  busy.value = "verify-all"
  batchSummary.value = null
  const outcomes: BatchVerificationOutcome[] = []
  for (const item of saved.value) {
    item.error = ""
    const device = item.device.business_node_name || item.device.name
    for (const level of levels) {
      batchProgress.value = `正在验证 ${device} · ${level}`
      let error = ""
      try { await runVerification(item, level) }
      catch (cause) { error = verificationFailure(level, cause); item.error = error }
      outcomes.push(batchVerificationOutcome(device, level, item.records, error))
    }
  }
  batchProgress.value = ""
  batchSummary.value = summarizeBatchVerification(outcomes)
  busy.value = ""
}
onMounted(refreshSaved)
</script>

<style scoped>
.batch-access { display: flex; flex-direction: column; gap: 10px; padding: 12px; border: 1px solid rgba(255,255,255,.72); border-radius: 12px; background: rgba(255,255,255,.28); }
.panel-head { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }.panel-head span { color: var(--faint); font-size: 12px; }.spacer { flex: 1; }
td small { display: block; color: var(--faint); font-size: 10px; margin-top: 2px; }
.batch-form { display: grid; grid-template-columns: repeat(4,minmax(0,1fr)); gap: 8px; align-items: end; }.batch-form label, .csv-label { display: flex; flex-direction: column; gap: 4px; color: var(--muted); font-size: 11px; }
.batch-form select, .batch-form input, textarea { width: 100%; border: 1px solid var(--glass-border); border-radius: 8px; padding: 7px 9px; background: rgba(255,255,255,.62); }
.actions { display: flex; gap: 8px; flex-wrap: wrap; }.empty { color: var(--faint); }
.batch-failures { margin: 4px 0 10px; padding-left: 18px; color: var(--amber); font-size: 11px; line-height: 1.7; }
@media(max-width:900px){.batch-form{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
