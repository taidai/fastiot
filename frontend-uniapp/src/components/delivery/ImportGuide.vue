<template>
  <view class="import-guide">
    <view class="pipeline" aria-label="导入到运行步骤">
      <span class="ok">导出</span><i>→</i><span class="ok">导入</span><i>→</i><span class="current">现场重绑</span><i>→</i><span>运行</span>
    </view>
    <view class="actions">
      <button class="primary" @click="rebindOpen = !rebindOpen">{{ rebindOpen ? "收起现场重绑" : "重绑通道与设备" }}</button>
      <button class="ghost" :disabled="refreshing" @click="refresh">{{ refreshing ? "刷新中…" : "刷新真实状态" }}</button>
      <button class="ghost" @click="openStrategyRebind">打开策略重绑</button>
    </view>
    <p class="note">重绑只列出本次导入的通道，可修改真实连接并保存；失败显示后端原因，不自动把重绑标成完成，也不会回退到既有 Site 的设备。通知接收账号须在设计 → 策略中显式选择当前范围账号，保存和发布会复核权限，不自动启用。</p>
    <RebindPanel v-if="rebindOpen" :snapshot="snapshot" :node-ids="importedIDList" @saved="rebindSaved" />

    <view v-if="result.evidence?.id" class="evidence-card">
      <view class="card-title">导入证据（已保存）</view>
      <view class="row"><span>来源包</span><span class="src">{{ result.evidence.source_package }}</span></view>
      <view class="row"><span>来源版本</span><span class="src">{{ result.evidence.source_version || "未发布草稿" }}</span></view>
      <view class="row"><span>来源指纹</span><span class="src">{{ result.evidence.source_fingerprint }}</span></view>
      <view class="row"><span>目标 / 结果</span><span class="src">{{ result.evidence.target }} · {{ result.evidence.result }}</span></view>
      <view class="row"><span>操作者 / 时间</span><span class="src">{{ result.evidence.actor }} · {{ clock(result.evidence.time) }}</span></view>
      <p class="note">这里只证明指定包已导入当前实例，不是部署成功或控制授权。</p>
    </view>

    <view class="validation-card">
      <view class="card-title">采集验证</view>
      <p class="note">L2 验证复用现有接入接口，只核对当前映射的必需点读值、质量与时间；不写设备，不等于部署、投运或控制授权。</p>
      <view class="actions"><button class="primary" :disabled="!verificationChannel || validating" @click="verifyAcquisition">{{ validating ? "验证中…" : "验证模拟采集" }}</button></view>
      <view v-if="validationError" class="error" role="alert">{{ validationError }}</view>
    </view>
    <view v-if="verificationRecord" class="evidence-card">
      <view class="card-title">采集验证（已保存）</view>
      <view class="row"><span>结果</span><span class="src">{{ verificationRecord.level }} · {{ verificationRecord.passed ? "通过" : "未通过" }}<template v-if="!verificationRecord.current"> · 非当前配置</template></span></view>
      <view class="row"><span>目标</span><span class="src">{{ verificationRecord.device_id }}</span></view>
      <view class="row"><span>操作者 / 时间</span><span class="src">{{ verificationRecord.actor }} · {{ clock(verificationRecord.at) }}</span></view>
      <view class="row"><span>Profile / 映射版本</span><span class="src">{{ verificationRecord.profile_version }} · {{ verificationRecord.mapping_version }}</span></view>
      <view v-if="verificationRecord.reasons?.length" class="row"><span>原因</span><span class="src">{{ verificationRecord.reasons.join("；") }}</span></view>
      <view v-if="verificationRecord.passed && verificationRecord.current" class="notice" role="status">采集验证通过；证据已保存</view>
      <view v-else class="notice warn" role="alert">验证未通过或记录已失效；不能据此宣称采集恢复</view>
    </view>

    <view class="runtime-card">
      <view class="card-title">运行状态（真实快照）</view>
      <view class="row"><span>采集</span><span class="src">采集样本 {{ runtimeValues.length }} 个</span></view>
      <view class="row"><span>最新样本</span><span class="src">{{ latestSampleTime }}</span></view>
      <view class="row"><span>通道质量记录</span><span class="src">{{ runtimeChannels.length }} 条</span></view>
      <view class="row"><span>最近预检</span><span class="src">{{ preflightText }}</span></view>
      <p v-if="!runtimeValues.length" class="note">当前没有这批导入节点的实时样本，不能据此宣称“运行中”。重绑后请运行预检并核对真实采集。</p>
      <p v-else class="note">只说明后端已收到这些节点的样本，不把采集样本等同于控制已启动或现场验收通过。</p>
    </view>
  </view>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue"
import RebindPanel from "./RebindPanel.vue"
import { request } from "../../api/client"
import { selectLayer } from "../../stores/session"
import type { Snapshot, StateValue } from "../../types/api"
import { clock, deliveryError, statusText } from "./contracts"

type ImportEvidence = {
  id: string; actor: string; source_package: string; source_version?: string; source_fingerprint: string
  target: string; time: string; result: string; reason?: string; node_ids: string[]; created: number; channels: number; rules: number
}
type ImportResult = { site_id: string; node_ids?: string[]; version_id?: string; evidence?: ImportEvidence }
type VerificationRecord = {
  at: string; actor: string; level: string; passed: boolean; reasons?: string[]; basis?: string
  profile_version?: string; mapping_version?: string; device_id?: string; current: boolean
}
type PreflightReport = { site?: string; generated_at?: string; status: string }
type ChannelQuality = { channel_id?: string; id?: string; node_id?: string }
const props = defineProps<{
  result: ImportResult
  snapshot: Snapshot | null
  values: StateValue[]
  quality: { channels: unknown[]; devices: unknown[] }
}>()
const emit = defineEmits<{ (event: "refresh"): void }>()
const rebindOpen = ref(false)
const refreshing = ref(false)
const latestPreflight = ref<PreflightReport | null>(null)
const preflightError = ref("")
const verificationChannel = ref("")
const verificationRecord = ref<VerificationRecord | null>(null)
const validating = ref(false)
const validationError = ref("")

// Start with the exact created IDs, then include children added later during
// rebind (new points under an imported channel, or channels under the Site).
const importedIDs = computed(() => {
  const ids = new Set(props.result.node_ids?.length ? props.result.node_ids : [props.result.site_id])
  const nodes = props.snapshot?.nodes || []
  for (let changed = true; changed;) {
    changed = false
    for (const node of nodes) {
      if (node.parent && ids.has(node.parent) && !ids.has(node.id)) {
        ids.add(node.id)
        changed = true
      }
    }
  }
  return ids
})
const importedIDList = computed(() => [...importedIDs.value])
const importedChannels = computed(() => (props.snapshot?.nodes || []).filter(node => importedIDs.value.has(node.id) && node.type === "modbus"))
const runtimeValues = computed(() => props.values.filter(value => importedIDs.value.has(value.point_id)))
const runtimeChannels = computed(() => (props.quality.channels as ChannelQuality[]).filter(channel => importedIDs.value.has(channel.channel_id || channel.node_id || channel.id || "")))
const latestSampleTime = computed(() => {
  const times = runtimeValues.value.map(value => value.source_time || value.received_time || "").filter(Boolean).sort()
  return times.length ? clock(times[times.length - 1]) : "—"
})
const preflightText = computed(() => {
  if (preflightError.value) return preflightError.value
  return latestPreflight.value ? `${statusText(latestPreflight.value.status)} · ${clock(latestPreflight.value.generated_at)}` : "当前 Site 尚未保存预检"
})
watch(() => props.result.site_id, () => {
  verificationRecord.value = null
  validationError.value = ""
  void loadPreflight()
}, { immediate: true })
watch(importedChannels, channels => {
  if (!channels.some(channel => channel.id === verificationChannel.value)) verificationChannel.value = channels[0]?.id || ""
  if (verificationChannel.value) void loadVerification(verificationChannel.value)
}, { immediate: true })
async function loadVerification(channelID: string) {
  if (!channelID) return
  try {
    const result = await request<{ records?: VerificationRecord[] }>(`/modbus/${encodeURIComponent(channelID)}/units/1/verify`)
    if (verificationChannel.value === channelID) verificationRecord.value = result.records?.length ? result.records[result.records.length - 1] : null
  } catch (cause) {
    if (verificationChannel.value === channelID) validationError.value = deliveryError(cause)
  }
}
function rebindSaved(channelID: string) {
  verificationChannel.value = channelID
  verificationRecord.value = null
  validationError.value = ""
  emit("refresh")
  void loadVerification(channelID)
}
async function verifyAcquisition() {
  const channelID = verificationChannel.value
  if (!channelID || validating.value) return
  validating.value = true
  validationError.value = ""
  try {
    const result = await request<{ passed: boolean; recorded: boolean; reasons?: string[]; record: VerificationRecord }>(`/modbus/${encodeURIComponent(channelID)}/units/1/verify`, "POST", { level: "L2" })
    verificationRecord.value = result.record
    if (!result.recorded) validationError.value = "验证记录保存失败，不能把本次结果当作已保存证据"
    else if (!result.passed) validationError.value = result.reasons?.join("；") || "采集验证未通过"
  } catch (cause) {
    validationError.value = deliveryError(cause)
  } finally { validating.value = false }
}
function openStrategyRebind() {
  selectLayer(props.result.site_id)
  uni.navigateTo({ url: "/pages/design/index?step=strategy" })
}
async function loadPreflight() {
  preflightError.value = ""
  try {
    const result = await request<{ latest: PreflightReport | null }>(`/delivery/preflight?site=${encodeURIComponent(props.result.site_id)}`)
    latestPreflight.value = result.latest || null
  } catch (cause) {
    latestPreflight.value = null
    preflightError.value = deliveryError(cause)
  }
}
function refresh() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    emit("refresh")
    void loadPreflight()
    void loadVerification(verificationChannel.value)
  } finally { refreshing.value = false }
}
</script>

<style scoped>
.import-guide { display: grid; gap: 10px; margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--line); }
.pipeline { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.pipeline span { padding: 4px 9px; border: 1px solid var(--line); border-radius: 999px; color: var(--faint); font-size: 11.5px; }
.pipeline span.ok { border-color: rgba(35,112,95,.25); background: rgba(230,243,238,.8); color: var(--mint); }
.pipeline span.current { border-color: var(--accent); color: var(--accent); }
.pipeline i { color: var(--faint); font-style: normal; }
.actions { display: flex; gap: 8px; flex-wrap: wrap; margin: 0; }
.note { margin: 0; color: var(--faint); font-size: 12px; line-height: 1.7; }
.runtime-card, .evidence-card, .validation-card { min-width: 0; padding: 12px; border: 1px solid var(--line); border-radius: 12px; background: rgba(255,255,255,.3); }
.card-title { margin-bottom: 6px; font-size: 12px; font-weight: 600; }
.row { min-width: 0; }
.src { overflow-wrap: anywhere; }
@media (max-width: 760px) {
  .runtime-card .row, .evidence-card .row { align-items: flex-start; flex-direction: column; }
  .runtime-card .src, .evidence-card .src { max-width: 100%; text-align: left; }
}
</style>
