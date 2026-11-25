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
      <text>命令与写点由后端校验；回读确认后才算完成。</text>
    </view>
    <view class="card-title operation-title">操作记录</view>
    <view v-if="recordError" class="error">{{ recordError }}</view>
    <view class="operation-records">
      <view v-for="record in records.slice(0, 5)" :key="record.request_id" class="operation-record">
        <span>{{ shortTime(record.updated_at || record.started_at) }} · {{ record.label || record.command_id || '设备命令' }}</span>
        <b :class="{ unknown: record.state === 'unknown', failed: record.state === 'failed' }">{{ commandStateText(record.state) }}</b>
      </view>
      <view v-if="!records.length" class="side-empty">—</view>
    </view>
  </section>

    <view v-if="drawerOpen" class="operation-drawer glass" role="dialog" aria-modal="true" :aria-label="`操作 · ${device.name}`">
      <view class="drawer-head"><h2>操作 · {{ device.name }}</h2><button class="drawer-close" aria-label="关闭操作抽屉" @click="drawerOpen = false">关闭</button></view>
      <view class="current-values">
        <view v-for="row in pointRows.slice(0, 3)" :key="row.id" class="row"><span>{{ row.label }}</span><b>{{ row.value }}{{ row.unit ? ` ${row.unit}` : '' }}</b></view>
      </view>

      <h3>Profile 命令</h3>
      <view v-if="model?.commands.length" class="command-list">
        <view v-for="command in model.commands" :key="command.id" class="command-item">
          <view v-for="parameter in command.parameters || []" :key="parameter.name" class="parameter-row">
            <label :for="`parameter-${command.id}-${parameter.name}`">{{ parameter.label || parameter.point_name }}<text v-if="parameter.unit">（{{ parameter.unit }}）</text></label>
            <input :id="`parameter-${command.id}-${parameter.name}`" v-model="parameterInputs[`${command.id}.${parameter.name}`]" type="number" inputmode="decimal"
              :min="parameter.has_range ? parameter.min : undefined" :max="parameter.has_range ? parameter.max : undefined" :disabled="busy || !command.available" />
          </view>
          <button class="command-button" :disabled="busy || !command.available" :title="command.available ? command.name : command.reason" @click="runCommand(command)">
            {{ busyCommand === command.id ? '等待结果…' : command.name }}
          </button>
          <text v-if="!command.available" class="unavailable">{{ command.reason || '命令不可用' }}</text>
        </view>
      </view>
      <view v-else class="empty">当前 Profile 没有可用命令。</view>

      <h3>手写点</h3>
      <view v-if="writablePoints.length" class="manual-write">
        <select v-model="writePointId" class="write-point" aria-label="写值点位" :disabled="busy">
          <option v-for="point in writablePoints" :key="point.id" :value="point.id">{{ point.name }}{{ point.unit ? `（${point.unit}）` : '' }}</option>
        </select>
        <input v-model="writeValue" class="write-value" type="number" inputmode="decimal" aria-label="写入值" :disabled="busy" />
        <button class="write-submit" :disabled="busy || !writePointId" @click="runWrite">写入并回读</button>
      </view>
      <view v-else class="empty">Profile 未声明可写输出点。</view>
      <view class="operation-result" role="status" aria-live="polite">{{ operationMessage }}</view>
      <p class="note">下发只是请求；已回读才表示观测一致。超时或查询中断显示“结果未知”，不伪装成功。</p>
    </view>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue"
import type { Node as ApiNode, StateValue } from "../../types/api"
import { businessName, commandStateText, deviceKey, displaySample, isTerminalCommandState, qualityLabel, shortTime, unitOf, unitPointsForServer, writableUnitPoints } from "./overview"
import type { LedgerDevice } from "./overview"
import { loadOperationRecords, loadUnitCommand, loadUnitModel, submitUnitCommand, writeUnitPoint } from "./operations"
import type { ManualWriteResult, OperationRecord, UnitCommand, UnitModel } from "./operations"

type DisplayRecord = OperationRecord & { label?: string; target?: number }

const props = defineProps<{ device: LedgerDevice; values: StateValue[]; nodes: ApiNode[] }>()
const tab = ref<"live" | "params">("live")
const drawerOpen = ref(false)
const model = ref<UnitModel | null>(null)
const loading = ref(false)
const modelError = ref("")
const recordError = ref("")
const serverRecords = ref<OperationRecord[]>([])
const manualResults = ref<Record<string, DisplayRecord>>({})
const operationMessage = ref("")
const busyCommand = ref("")
const busy = ref(false)
const writePointId = ref("")
const writeValue = ref("")
const parameterInputs = ref<Record<string, string>>({})
let loadSerial = 0
let recordSerial = 0
let operationSerial = 0
let pollTimer: ReturnType<typeof setTimeout> | undefined

const devicePoints = computed(() => unitPointsForServer(model.value?.points || [], props.device.server_id))
const writablePoints = computed(() => writableUnitPoints(devicePoints.value))
const verificationText = computed(() => {
  const verified = model.value?.model?.verified ?? props.device.verified
  if (!verified) return "未验证"
  const actor = model.value?.model?.verifier || props.device.verifier || "未记录人员"
  const at = model.value?.model?.verified_at || props.device.verified_at
  return `已验证 · ${actor}${at ? ` · ${at}` : ""}`
})
const pointRows = computed(() => devicePoints.value.map(point => {
  const node = props.nodes.find(item => item.id === point.id)
  const sample = props.values.find(item => item.point_id === point.id)
  const label = businessName(node, point.name || "未命名点位")
  return { id: point.id, label, value: displaySample(sample, label), unit: unitOf(node) || point.unit || "", quality: qualityLabel(sample?.quality) }
}))
const storedManualRecords = computed(() => {
  const result: DisplayRecord[] = []
  const pointIDs = new Set(devicePoints.value.map(point => point.id))
  for (const node of props.nodes) {
    if (!pointIDs.has(node.id)) continue
    const point = node.points.find(item => item.type === "lastCommand" && (item.tombstone || 0) % 2 === 0)
    if (!point?.text) continue
    try {
      const stored = JSON.parse(point.text) as ManualWriteResult
      result.push({
        request_id: stored.request_id, device_id: deviceKey(props.device), command_id: "", actor: stored.actor,
        state: stored.status, reason: stored.reason, started_at: stored.created_at, updated_at: point.time || stored.created_at,
        label: `写 ${businessName(node)}`, target: stored.target,
      })
    } catch { /* Ignore malformed legacy projections; live API errors remain visible. */ }
  }
  return result
})
const records = computed<DisplayRecord[]>(() => {
  const named = serverRecords.value.map(record => ({
    ...record,
    label: model.value?.commands.find(command => command.id === record.command_id)?.name || record.command_id || "设备命令",
  }))
  const merged = new Map<string, DisplayRecord>()
  for (const record of [...storedManualRecords.value, ...named, ...Object.values(manualResults.value)]) merged.set(record.request_id, record)
  return [...merged.values()].sort((a, b) => new Date(b.updated_at || b.started_at).getTime() - new Date(a.updated_at || a.started_at).getTime())
})

function errorText(error: unknown) { return error instanceof Error ? error.message : String(error) }
async function loadRecords() {
  const serial = ++recordSerial
  recordError.value = ""
  try {
    const result = await loadOperationRecords(props.device)
    if (serial === recordSerial) serverRecords.value = result
  } catch (error) {
    if (serial === recordSerial) recordError.value = `操作记录读取失败：${errorText(error)}`
  }
}
async function loadDevice() {
  const serial = ++loadSerial
  recordSerial += 1
  recordError.value = ""
  loading.value = true
  modelError.value = ""
  model.value = null
  serverRecords.value = []
  manualResults.value = {}
  try {
    const result = await loadUnitModel(props.device)
    if (serial !== loadSerial) return
    model.value = result
    writePointId.value = writableUnitPoints(unitPointsForServer(result.points || [], props.device.server_id))[0]?.id || ""
    for (const command of result.commands || []) {
      for (const parameter of command.parameters || []) parameterInputs.value[`${command.id}.${parameter.name}`] = String(parameter.default)
    }
  } catch (error) {
    if (serial === loadSerial) modelError.value = `设备 Profile 读取失败：${errorText(error)}`
  } finally {
    if (serial === loadSerial) loading.value = false
  }
  await loadRecords()
}
function finishOperation(serial: number, message: string) {
  if (serial !== operationSerial) return
  operationMessage.value = message
  busy.value = false
  busyCommand.value = ""
  void loadRecords()
}
async function runCommand(command: UnitCommand) {
  if (busy.value) return
  const serial = ++operationSerial
  const parameters: Record<string, number> = {}
  for (const parameter of command.parameters || []) {
    const raw = parameterInputs.value[`${command.id}.${parameter.name}`]
    const value = Number(raw)
    if (raw.trim() === "" || !Number.isFinite(value)) {
      operationMessage.value = `${parameter.label || parameter.point_name} 必须是有效数字`
      return
    }
    parameters[parameter.name] = value
  }
  busy.value = true
  busyCommand.value = command.id
  operationMessage.value = "正在提交命令…"
  try {
    let outcome = await submitUnitCommand(props.device, command.id, parameters)
    if (serial !== operationSerial) return
    operationMessage.value = commandStateText(outcome.state)
    const deadline = Date.now() + Math.min(15000, Math.max(3000, (command.timeout_ms || 5000) + 2000))
    while (!isTerminalCommandState(outcome.state) && Date.now() < deadline) {
      await new Promise<void>(resolve => { pollTimer = setTimeout(resolve, 500) })
      if (serial !== operationSerial) return
      outcome = await loadUnitCommand(props.device, outcome.request_id)
      if (serial !== operationSerial) return
      operationMessage.value = commandStateText(outcome.state)
    }
    if (!isTerminalCommandState(outcome.state)) {
      finishOperation(serial, "结果未知 · 超时未获得命令结果")
      return
    }
    finishOperation(serial, `${commandStateText(outcome.state)}${outcome.reason ? ` · ${outcome.reason}` : ""}`)
  } catch (error) {
    finishOperation(serial, `结果未知或命令未接受 · ${errorText(error)}`)
  }
}
async function runWrite() {
  if (busy.value || !writePointId.value) return
  const value = Number(writeValue.value)
  if (writeValue.value.trim() === "" || !Number.isFinite(value)) {
    operationMessage.value = "写入值必须是有效数字"
    return
  }
  const serial = ++operationSerial
  const pointId = writePointId.value
  const device = props.device
  busy.value = true
  busyCommand.value = ""
  operationMessage.value = "正在写入并等待回读…"
  try {
    const result = await writeUnitPoint(pointId, value)
    if (serial !== operationSerial) return
    const point = devicePoints.value.find(item => item.id === pointId)
    manualResults.value[result.request_id] = {
      request_id: result.request_id, device_id: deviceKey(device), command_id: "", actor: result.actor,
      state: result.status, reason: result.reason, started_at: result.created_at, updated_at: result.created_at,
      label: `写 ${point?.name || pointId}`, target: result.target,
    }
    finishOperation(serial, `${commandStateText(result.status)}${result.reason ? ` · ${result.reason}` : ""}`)
  } catch (error) {
    finishOperation(serial, `写入未完成 · ${errorText(error)}`)
  }
}

watch(() => deviceKey(props.device), () => {
  operationSerial += 1
  if (pollTimer) clearTimeout(pollTimer)
  busy.value = false
  busyCommand.value = ""
  drawerOpen.value = false
  operationMessage.value = ""
  void loadDevice()
}, { immediate: true })
onBeforeUnmount(() => {
  operationSerial += 1
  if (pollTimer) clearTimeout(pollTimer)
})
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
.operation-record { display: flex; justify-content: space-between; gap: 10px; padding: 6px 0; border-top: 1px solid var(--line); font-size: 11px; }
.operation-record b { flex: none; color: var(--accent); font-weight: 500; }
.operation-record b.unknown, .operation-record b.failed { color: var(--amber); }
.side-empty { color: var(--muted); }
.operation-drawer { position: fixed; z-index: 60; top: 16px; right: 16px; bottom: 16px; width: 400px; max-width: 92vw; padding: 20px; overflow: auto; background: var(--glass-strong); }
.drawer-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.drawer-head h2 { margin: 0; font-size: 15px; }
.drawer-close { min-height: 32px; padding: 4px 10px; border: 1px solid var(--glass-border); border-radius: 8px; background: var(--control-bg, rgba(255,255,255,.4)); font-size: 11px; }
.current-values { margin: 14px 0; }
.operation-drawer h3 { margin: 18px 0 8px; color: var(--muted); font-size: 12px; }
.command-item { padding: 10px 0; border-top: 1px solid var(--line); }
.command-button, .write-submit { min-height: 34px; padding: 6px 12px; border: 1px solid var(--glass-border); border-radius: 9px; background: var(--accent); color: var(--on-accent, white); font-size: 12px; }
.parameter-row { display: grid; grid-template-columns: 1fr 130px; align-items: center; gap: 10px; margin-bottom: 8px; font-size: 12px; }
.parameter-row input, .manual-write input, .manual-write select { min-height: 34px; border: 1px solid var(--line); border-radius: 8px; background: var(--control-bg, rgba(255,255,255,.5)); padding: 5px 8px; }
.unavailable { display: block; margin-top: 5px; color: var(--muted); font-size: 10px; }
.manual-write { display: grid; grid-template-columns: minmax(0, 1fr) 100px; gap: 8px; }
.manual-write .write-submit { grid-column: 1 / -1; }
.operation-result { min-height: 24px; margin-top: 12px; color: var(--ink); font-size: 12px; line-height: 1.6; }
.note { color: var(--muted); font-size: 11px; line-height: 1.7; }
.placeholder-row td { color: var(--faint); text-align: center; }
@media (max-width: 760px) {
  .operation-drawer { inset: 8px; width: auto; max-width: none; }
  .device-actions { align-items: flex-start; flex-direction: column; gap: 0; }
}
</style>
