<template>
  <Teleport :to="target">
  <view class="operation-drawer glass" role="dialog" aria-modal="true" :aria-label="`操作 · ${device.name}`">
    <view class="drawer-head"><h2>操作 · {{ device.name }}</h2><button class="drawer-close" aria-label="关闭操作抽屉" @click="$emit('close')">关闭</button></view>
    <view class="current-values"><view v-for="row in rows.slice(0, 3)" :key="row.id" class="row"><span>{{ row.label }}</span><b>{{ row.value }} {{ row.unit }}</b></view></view>
    <h3>Profile 命令</h3>
    <view v-for="command in model?.commands || []" :key="command.id" class="command-item">
      <view v-for="parameter in command.parameters || []" :key="parameter.name" class="parameter-row">
        <label :for="`parameter-${command.id}-${parameter.name}`">{{ parameter.label || parameter.point_name }} {{ parameter.unit || '' }}</label>
        <input :id="`parameter-${command.id}-${parameter.name}`" v-model="inputs[`${command.id}.${parameter.name}`]" type="number" :disabled="busy || !command.available" :min="parameter.has_range ? parameter.min : undefined" :max="parameter.has_range ? parameter.max : undefined" />
      </view>
      <button class="command-button" :disabled="busy || !command.available" @click="prepare(command)">{{ command.name }}</button>
      <text v-if="!command.available" class="unavailable">{{ command.reason || '命令不可用' }}</text>
    </view>
    <view v-if="!model?.commands.length" class="empty">当前 Profile 没有可用命令。</view>
    <h3>人工接管 / 恢复自动</h3>
    <view v-for="point in manualPoints" :key="point.id" class="command-item">
      <span>{{ point.name }} · {{ manual(point.id) ? '人工接管' : '未接管' }}</span>
      <button class="manual-toggle" :disabled="busy || !model?.manual_allowed" @click="pending = { kind: 'manual', point: point.id, name: point.name, manual: !manual(point.id) }">{{ manual(point.id) ? '恢复自动' : '人工接管' }}</button>
    </view>
    <p class="note">仅暂停所选输出的相关自动控制，安全保护继续有效。恢复自动须另行确认；关闭、切页或刷新不改变授权。</p>
    <view v-if="pending" class="confirmation" role="alertdialog" aria-label="确认设备操作">
      <b>{{ pending.kind === 'command' ? pending.command.name : pending.manual ? '人工接管' : '恢复自动' }} · {{ device.name }}</b>
      <p v-if="pending.kind === 'command'">参数：{{ JSON.stringify(pending.parameters) }}。下发后须核对真实回读。</p>
      <p v-else>目标输出：{{ pending.name }}。{{ pending.manual ? '暂停相关自动控制，安全保护不解除。' : '允许相关自动逻辑再次参与控制，请核对现场条件。' }}</p>
      <button class="operation-confirm" :disabled="busy" @click="confirmOperation">确认操作</button><button :disabled="busy" @click="pending = null">取消</button>
    </view>
    <view class="operation-result" role="status" aria-live="polite">{{ message }}</view>
    <view v-if="requestID" class="request-identity">请求：{{ requestID }} <button class="query-command" :disabled="busy" @click="query">查询结果（不重发）</button></view>
    <p class="note">仅已下发不等于执行成功。超时或断线不自动重发物理动作；保留请求身份供刷新后查询。</p>
  </view>
  </Teleport>
</template>

<script setup lang="ts">
import { Teleport, computed, onBeforeUnmount, ref } from 'vue'
import { ApiError } from '../../api/client'
import type { Node as ApiNode } from '../../types/api'
import type { LedgerDevice } from './overview'
import { commandStateText, deviceKey, isTerminalCommandState } from './overview'
import { loadUnitCommand, setManualMode, submitUnitCommand } from './operations'
import type { UnitCommand, UnitModel } from './operations'
const props = defineProps<{target: string | Element; device: LedgerDevice; model: UnitModel | null; nodes: ApiNode[]; rows: {id: string; label: string; value: string; unit: string}[]}>()
const emit = defineEmits<{(event: 'close'): void; (event: 'changed'): void}>()
const busy = ref(false), message = ref('')
const inputs = ref<Record<string,string>>({})
for (const command of props.model?.commands || []) for (const parameter of command.parameters || []) inputs.value[`${command.id}.${parameter.name}`] = String(parameter.default)
const key = `operation-request:${deviceKey(props.device)}`
const requestID = ref(sessionStorage.getItem(key) || '')
const manualOverrides = ref<Record<string,boolean>>({})
const pending = ref<{kind:'command'; command:UnitCommand; parameters:Record<string,number>} | {kind:'manual'; point:string; name:string; manual:boolean} | null>(null)
let active = true
onBeforeUnmount(() => { active = false })
const manualPoints = computed(() => {
  const ids = new Set((props.model?.commands || []).flatMap(command => [command.point_id, ...(command.parameters || []).map(p => p.point_id)]).filter(Boolean))
  return (props.model?.points || []).filter(point => point.server_id === props.device.server_id && ids.has(point.id) && !point.read_only)
})
function manual(id:string) {
  return manualOverrides.value[id] ?? !!props.nodes.find(n => n.id === id)?.points.find(p => p.type === 'easyemsManual' && (p.tombstone || 0) % 2 === 0)?.value
}
function prepare(command:UnitCommand) {
  const parameters:Record<string,number> = {}
  for (const parameter of command.parameters || []) {
    const raw = inputs.value[`${command.id}.${parameter.name}`] || ''
    const value = Number(raw)
    if (!raw.trim() || !Number.isFinite(value) || (parameter.has_range && (value < parameter.min || value > parameter.max)) || (parameter.kind === 'bool' && value !== 0 && value !== 1)) {
      message.value = `已拒绝 · ${parameter.label || parameter.name} 的类型或范围无效`; return
    }
    parameters[parameter.name] = value
  }
  pending.value = {kind:'command', command, parameters}
}
function showError(error:unknown) {
  const reason = error instanceof Error ? error.message : String(error)
  // A received rejection is different from a lost response to a possible write.
  message.value = `${error instanceof ApiError && error.status >= 400 && error.status < 500 && error.code !== 'project_context_changed' && error.code !== 'session_changed' ? '已拒绝' : '结果未知'} · ${reason}`
}
async function query() {
  busy.value = true
  try {
    const outcome = await loadUnitCommand(props.device, requestID.value)
    if (active) message.value = `${commandStateText(outcome.state)}${outcome.reason ? ` · ${outcome.reason}` : ''}`
  } catch (error) { if (active) message.value = `结果查询不可用 · ${error instanceof Error ? error.message : String(error)}。未重发操作。` }
  finally { if (active) { busy.value = false; emit('changed') } }
}
async function confirmOperation() {
  if (!pending.value || busy.value) return
  const selected = pending.value
  pending.value = null; busy.value = true
  try {
    if (selected.kind === 'manual') {
      const result = await setManualMode(selected.point, selected.manual)
      if (active) { manualOverrides.value[selected.point] = result.manual; message.value = result.manual ? '已人工接管 · 安全保护继续有效' : '已恢复自动 · 不代表设备动作已完成' }
    } else {
      requestID.value = crypto.randomUUID()
      sessionStorage.setItem(key, requestID.value)
      message.value = '正在提交命令…'
      let outcome = await submitUnitCommand(props.device, selected.command.id, selected.parameters, requestID.value)
      const deadline = Date.now() + (selected.command.timeout_ms || 5000) + 2000
      while (active && !isTerminalCommandState(outcome.state) && Date.now() < deadline) {
        message.value = commandStateText(outcome.state)
        await new Promise(resolve => setTimeout(resolve, 500))
        if (!active) return
        outcome = await loadUnitCommand(props.device, requestID.value)
      }
      if (active) message.value = isTerminalCommandState(outcome.state) ? `${commandStateText(outcome.state)}${outcome.reason ? ` · ${outcome.reason}` : ''}` : '结果未知 · 未获得截止时间内的确认，请查询结果；不会自动重发。'
    }
  } catch (error) { if (active) showError(error) }
  finally { if (active) { busy.value = false; emit('changed') } }
}
</script>

<style scoped>
.operation-drawer { position: fixed; z-index: 60; top: 16px; right: 16px; bottom: 16px; width: 400px; max-width: 92vw; padding: 20px; overflow: auto; background: var(--glass-strong); }
.drawer-head { display:flex; align-items:center; justify-content:space-between; gap:12px; }
.drawer-head h2 { margin:0; font-size:15px; }
button { min-height:34px; padding:6px 12px; border:1px solid var(--glass-border); border-radius:9px; font-size:12px; }
.command-button, .operation-confirm { background:var(--accent); color:var(--on-accent,white); }
.current-values { margin:14px 0; }
h3 { margin:18px 0 8px; color:var(--muted); font-size:12px; }
.command-item { padding:10px 0; border-top:1px solid var(--line); }
.parameter-row { display:grid; grid-template-columns:1fr 130px; align-items:center; gap:10px; margin-bottom:8px; font-size:12px; }
.parameter-row input { min-height:34px; border:1px solid var(--line); border-radius:8px; padding:5px 8px; }
.unavailable, .note { display:block; margin-top:5px; color:var(--muted); font-size:11px; line-height:1.7; }
.operation-result, .confirmation { margin-top:12px; font-size:12px; line-height:1.6; }
.request-identity { overflow-wrap:anywhere; font-size:11px; }
.manual-toggle { display:block; margin-top:5px; }
@media (max-width:760px) { .operation-drawer { inset:8px; width:auto; max-width:none; } }
</style>
