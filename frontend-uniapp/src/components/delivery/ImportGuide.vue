<template>
  <view v-if="PREVIEW_MODE" class="import-guide preview-import-guide">
    <view class="pipeline" aria-label="原型导入到运行步骤">
      <span class="ok">导出</span><i>→</i><span class="ok">导入</span><i>→</i><span :class="step === 'imported' ? 'current' : 'ok'">现场重绑</span><i>→</i><span :class="step === 'running' ? 'ok' : step === 'rebound' ? 'current' : ''">运行</span>
    </view>
    <template v-if="step === 'imported'">
      <view class="row"><span>包版本</span><span class="src">{{ result.version_id }} · 依赖已锁定</span></view>
      <view class="row"><span>必须重绑</span><span class="src">通道、设备地址与凭据；运行值不带入</span></view>
      <view class="actions"><button class="primary" :disabled="previewBusy" @click="advance('rebind')">完成现场重绑</button></view>
    </template>
    <template v-else-if="step === 'rebound'">
      <view class="row"><span>重绑结果</span><span class="src">Modbus TCP · 站号已重新分配 · 凭据未展示</span></view>
      <view class="actions"><button class="primary" :disabled="previewBusy" @click="advance('run')">在新实例运行</button></view>
    </template>
    <template v-else-if="step === 'running'">
      <view class="card-title">新实例运行</view>
      <view class="row"><span>状态</span><span class="chip ok">运行中</span></view>
      <view class="row"><span>采集</span><span class="src">采集恢复 · {{ runtimeValues.length }} 个演示样本 · 最新样本 {{ latestSampleTime }}</span></view>
      <view class="row"><span>策略</span><span class="src">包内规则与状态机生效 · 未重放旧命令</span></view>
      <p class="note">导入后的历史从本实例重新开始；原实例运行历史与审计不被覆盖。</p>
    </template>
    <view v-if="previewError" class="error">{{ previewError }}</view>
    <p class="note">原型演练：仅更新内存中的交付状态，不连接现场，不执行真实控制。</p>
  </view>
  <view v-else class="import-guide">
    <view class="pipeline" aria-label="导入到运行步骤">
      <span class="ok">导出</span><i>→</i><span class="ok">导入</span><i>→</i><span class="current">现场重绑</span><i>→</i><span>运行</span>
    </view>
    <view class="actions">
      <button class="primary" @click="rebindOpen = !rebindOpen">{{ rebindOpen ? "收起现场重绑" : "重绑通道与设备" }}</button>
      <button class="ghost" :disabled="refreshing" @click="refresh">{{ refreshing ? "刷新中…" : "刷新真实状态" }}</button>
    </view>
    <p class="note">重绑只列出本次导入的通道，可修改真实连接并保存；失败显示后端原因，不自动把重绑标成完成，也不会回退到既有 Site 的设备。</p>
    <RebindPanel v-if="rebindOpen" :snapshot="snapshot" :node-ids="importedIDList" @saved="refresh" />

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
import { PREVIEW_MODE } from "../../api/source"
import { deliveryPreviewState } from "../../api/preview/delivery"
import type { Snapshot, StateValue } from "../../types/api"
import { clock, deliveryError, statusText } from "./contracts"

type ImportResult = { site_id: string; node_ids?: string[]; version_id?: string }
type PreflightReport = { site?: string; generated_at?: string; status: string }
type ChannelQuality = { channel_id?: string; id?: string; node_id?: string }
const props = defineProps<{
  result: ImportResult
  snapshot: Snapshot | null
  values: StateValue[]
  quality: { channels: unknown[]; devices: unknown[] }
}>()
const emit = defineEmits<{ (event: "refresh"): void }>()
const step = computed(() => deliveryPreviewState.importStep)
const previewBusy = ref(false)
const previewError = ref("")
async function advance(action: "rebind" | "run") {
  if (!PREVIEW_MODE || previewBusy.value) return
  previewBusy.value = true; previewError.value = ""
  try {
    await request(`/delivery/preview/${action}`, "POST")
    emit("refresh")
  } catch (error) { previewError.value = deliveryError(error) } finally { previewBusy.value = false }
}
const rebindOpen = ref(false)
const refreshing = ref(false)
const latestPreflight = ref<PreflightReport | null>(null)
const preflightError = ref("")

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
watch(() => props.result.site_id, () => { void loadPreflight() }, { immediate: true })
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
.runtime-card { min-width: 0; padding: 12px; border: 1px solid var(--line); border-radius: 12px; background: rgba(255,255,255,.3); }
.card-title { margin-bottom: 6px; font-size: 12px; font-weight: 600; }
.row { min-width: 0; }
.src { overflow-wrap: anywhere; }
@media (max-width: 760px) {
  .runtime-card .row { align-items: flex-start; flex-direction: column; }
  .runtime-card .src { max-width: 100%; text-align: left; }
}
</style>
