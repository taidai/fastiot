<template>
  <section class="panel glass device-panel">
    <view class="card-title">设备状态<text>{{ device.name }}</text></view>
    <view class="tabs device-tabs" role="tablist" aria-label="设备数据">
      <button :class="{ on: tab === 'live' }" role="tab" :aria-selected="tab === 'live'" @click="tab = 'live'">实时数据</button>
      <button :class="{ on: tab === 'params' }" role="tab" :aria-selected="tab === 'params'" @click="tab = 'params'">设备参数</button>
    </view>
    <view v-if="modelError" class="error" role="alert">{{ modelError }}</view>
    <view v-else-if="loading" class="empty">正在读取设备 Profile 与点位…</view>
    <template v-else-if="tab === 'live'">
      <view class="table-wrap device-points">
        <table><thead><tr><th>点位</th><th>值</th><th>单位</th><th>质量</th></tr></thead>
          <tbody v-if="pointRows.length"><tr v-for="row in pointRows" :key="row.id">
            <td>{{ row.label }}</td><td class="num">{{ row.value }}</td><td>{{ row.unit || '—' }}</td><td>{{ row.quality }}</td>
          </tr></tbody>
          <tbody v-else><tr class="placeholder-row"><td>—</td><td>—</td><td>—</td><td>—</td></tr></tbody>
        </table>
      </view>
    </template>
    <view v-else class="device-params">
      <table><tbody>
        <tr><td>来源 Profile</td><td>{{ model?.model?.profile_id || device.profile_id || '—' }}</td></tr>
        <tr><td>型号</td><td>{{ device.model || '—' }}</td></tr>
        <tr><td>通道 / 站号</td><td>{{ device.channel_name || device.channel_id }} / {{ device.server_id }}</td></tr>
        <tr><td>点位数</td><td>{{ devicePoints.length || device.point_count || 0 }}</td></tr>
        <tr><td>最近验证</td><td>{{ verificationText }}</td></tr>
      </tbody></table>
    </view>

    <view class="device-actions">
      <button class="link open-operations" @click="drawerOpen = true">打开操作抽屉</button>
      <text>仅执行已声明命令；回读确认后才算完成。</text>
    </view>
    <view class="card-title operation-title">操作记录</view>
    <view v-if="recordError" class="error">{{ recordError }}</view>
    <view class="operation-records">
      <view v-for="record in records.slice(0, 5)" :key="record.request_id" class="operation-record">
        <span>{{ shortTime(record.updated_at || record.started_at) }} · {{ record.label || record.command_id || '设备命令' }}</span>
        <b :class="{ unknown: record.state === 'unknown', failed: record.state === 'failed' || record.state === 'rejected' }">{{ commandStateText(record.state) }}</b>
        <small>请求 {{ record.request_id }} · {{ record.actor }}<template v-if="record.reason"> · {{ record.reason }}</template><template v-for="sample in record.observed || []" :key="sample.point"> · 回读 {{ sample.value }}（{{ shortTime(sample.at) }}）</template></small>
      </view>
      <view v-if="!records.length" class="side-empty">—</view>
    </view>
  </section>

  <OperationDrawer v-if="drawerOpen" :key="deviceKey(device)" :device="device" :model="model" :nodes="nodes" :rows="pointRows" @close="drawerOpen = false" @changed="loadRecords" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue"
import type { Node as ApiNode, StateValue } from "../../types/api"
import { businessName, commandStateText, deviceKey, displaySample, qualityLabel, shortTime, unitOf, unitPointsForServer } from "./overview"
import type { LedgerDevice } from "./overview"
import { loadOperationRecords, loadUnitModel } from "./operations"
import type { OperationRecord, UnitModel } from "./operations"
import OperationDrawer from './OperationDrawer.vue'
const props = defineProps<{ device: LedgerDevice; values: StateValue[]; nodes: ApiNode[] }>()
const tab = ref<"live" | "params">("live")
const drawerOpen = ref(false)
const model = ref<UnitModel | null>(null)
const loading = ref(false), modelError = ref(""), recordError = ref("")
const serverRecords = ref<OperationRecord[]>([])
let loadSerial = 0, recordSerial = 0
const devicePoints = computed(() => unitPointsForServer(model.value?.points || [], props.device.server_id))
const verificationText = computed(() => {
  const verified = model.value?.model?.verified ?? props.device.verified
  if (!verified) return "未验证"
  const actor = model.value?.model?.verifier || props.device.verifier || "未记录人员"
  const at = model.value?.model?.verified_at || props.device.verified_at
  return `已验证 · ${actor}${at ? ` · ${at}` : ""}`
})
const pointRows = computed(() => devicePoints.value.map(point => {
  const node = props.nodes.find(item => item.id === point.id)
  const sample = props.values.find(item => item.point_id === point.id && item.device_id === deviceKey(props.device))
  const label = businessName(node, point.name || "未命名点位")
  return { id: point.id, label, value: sample?.quality === 'good' ? displaySample(sample, label) : '—', unit: unitOf(node) || point.unit || "", quality: sample ? qualityLabel(sample.quality) : '无数据' }
}))
const records = computed(() => serverRecords.value.map(record => ({...record,
  label: model.value?.commands.find(command => command.id === record.command_id)?.name || record.command_id || '设备命令',
})).sort((a,b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()))
function errorText(error:unknown) { return error instanceof Error ? error.message : String(error) }
async function loadRecords() {
  const serial = ++recordSerial
  recordError.value = ''
  try { const result = await loadOperationRecords(props.device); if (serial === recordSerial) serverRecords.value = result }
  catch (error) { if (serial === recordSerial) recordError.value = `操作记录读取失败：${errorText(error)}` }
}
async function loadDevice() {
  const serial = ++loadSerial
  recordSerial += 1
  model.value = null; serverRecords.value = []; modelError.value = ''; recordError.value = ''; loading.value = true
  try { const result = await loadUnitModel(props.device); if (serial === loadSerial) model.value = result }
  catch (error) { if (serial === loadSerial) modelError.value = `设备 Profile 读取失败：${errorText(error)}` }
  finally { if (serial === loadSerial) { loading.value = false; void loadRecords() } }
}
watch(() => deviceKey(props.device), () => { drawerOpen.value = false; void loadDevice() }, { immediate: true })
onBeforeUnmount(() => { loadSerial += 1; recordSerial += 1 })
</script>

<style scoped>
.device-panel { position: relative; }
.device-panel > .card-title > text { color: var(--ink); font-size: 12px; }
.tabs { display: flex; gap: 4px; margin-bottom: 8px; border-bottom: 1px solid var(--line); }
.tabs button { min-height: 32px; padding: 6px 12px; border: 0; border-bottom: 2px solid transparent; background: transparent; color: var(--muted); font-size: 12px; }
.tabs button.on { border-bottom-color: var(--accent); color: var(--ink); font-weight: 600; }
.device-actions { display: flex; align-items: center; gap: 10px; margin-top: 10px; }
.device-actions text { color: var(--muted); font-size: 10px; line-height: 1.5; }
.operation-title { margin-top: 10px; }
.operation-record { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 10px; padding: 6px 0; border-top: 1px solid var(--line); font-size: 11px; }
.operation-record small { grid-column: 1 / -1; overflow-wrap: anywhere; color: var(--muted); }
.operation-record b { flex: none; color: var(--accent); font-weight: 500; }
.operation-record b.unknown, .operation-record b.failed { color: var(--amber); }
.side-empty { color: var(--muted); }
.placeholder-row td { color: var(--faint); text-align: center; }
@media (max-width: 760px) {
  .device-actions { align-items: flex-start; flex-direction: column; gap: 0; }
}
</style>
