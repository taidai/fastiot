<template>
  <view class="machine-action-editor">
    <view v-for="(action, index) in actions" :key="index" class="action-row">
      <span>{{ index + 1 }}</span>
      <select aria-label="动作类型" :value="binding(action) ? 'command' : 'point'" @change="changeKind(action, $event)"><option value="point">软件／未映射点位</option><option value="command">设备声明命令</option></select>
      <template v-if="binding(action)">
        <select aria-label="声明设备命令" :value="commandKey(action)" @change="selectCommand(action, $event)"><option value="">选择设备与命令</option><option v-for="option in commands" :key="keyOf(option)" :value="keyOf(option)">{{ option.device_name }} · {{ option.command.name }}{{ option.command.available ? '' : '（当前不可执行）' }}</option></select>
        <span v-if="!selected(action)" class="note">命令引用尚未绑定或已不可用。</span>
        <label v-for="parameter in selected(action)?.command.parameters || []" :key="parameter.name">{{ parameter.label || parameter.name }}<input type="number" :min="parameter.has_range ? parameter.min : undefined" :max="parameter.has_range ? parameter.max : undefined" :value="binding(action)?.parameters?.[parameter.name] ?? parameter.default" @input="setParameter(action, parameter.name, $event)"></label>
        <button type="button" @click="toggleReadback(action)">{{ action.readback ? '移除过程完成条件' : '＋ 过程完成条件' }}</button>
      </template>
      <template v-else>
        <select aria-label="动作点位" v-model="action.action_id" @change="changed"><option v-for="node in outputs" :key="node.id" :value="businessId(node)">{{ nameOf(node) }}</option></select>
        <label>目标值<input type="number" step="any" v-model.number="action.value" @input="changed"></label>
      </template>
      <label>延时秒<input type="number" min="0" step="0.1" :value="(action.delay_ms || 0) / 1000" @input="action.delay_ms = Math.max(0, Math.round(number($event) * 1000)); changed()"></label>
      <template v-if="action.readback">
        <select aria-label="过程回读点位" :value="action.readback.target?.node_id || ''" @change="setReadbackTarget(action, $event)"><option value="" :disabled="!!binding(action)">同动作点位</option><option v-for="node in inputs" :key="node.id" :value="node.id">{{ nameOf(node) }}</option></select>
        <label v-if="binding(action)">过程期望值<input type="number" step="any" v-model.number="action.value" @input="changed"></label>
        <label>回读秒<input type="number" min="0.1" max="120" step="0.1" :value="action.readback.timeout_ms / 1000" @input="action.readback.timeout_ms = Math.min(120000, Math.max(100, Math.round(number($event) * 1000))); changed()"></label>
        <label>容差<input type="number" min="0" step="any" v-model.number="action.readback.tolerance" @input="changed"></label>
      </template>
      <button type="button" @click="actions.splice(index, 1); changed()">删除</button>
    </view>
    <button type="button" :disabled="!outputs.length && !commands.length" @click="add">＋ 动作</button>
    <span class="note">声明命令使用设备 Profile 的确认；过程条件不代表设备物理状态。每步未确认即停止。</span>
  </view>
</template>

<script setup lang="ts">
import type { Node } from "../../types/api"
import { nameOf } from "../../types/api"
import { businessId } from "./strategy"
import type { Action } from "./strategy"
import type { MachineCommandOption, StateMachineDefinition } from "./state-machine"
import { stateMachineInputNumber as number } from "./state-machine"
const props = defineProps<{ actions: Action[]; definition: StateMachineDefinition; inputs: Node[]; outputs: Node[]; commands: MachineCommandOption[] }>()
const emit = defineEmits<{ (event: "change"): void }>()
function changed() { emit("change") }
function binding(action: Action) { return props.definition.command_bindings?.[action.action_id] }
function keyOf(option: MachineCommandOption) { return JSON.stringify([option.device_id, option.command.id]) }
function commandKey(action: Action) { const bound = binding(action); return bound ? JSON.stringify([bound.device_id, bound.command_id]) : "" }
function selected(action: Action) { return props.commands.find(option => keyOf(option) === commandKey(action)) }
function changeKind(action: Action, event: Event) {
  if ((event.target as HTMLSelectElement).value === "command") {
    action.action_id = crypto.randomUUID()
    props.definition.command_bindings ||= {}
    props.definition.command_bindings[action.action_id] = { device_id: "", command_id: "", parameters: {} }
    delete action.readback
  } else {
    action.action_id = props.outputs[0] ? businessId(props.outputs[0]) : ""
    action.readback = { timeout_ms: 5000, tolerance: 0 }
  }
  changed()
}
function selectCommand(action: Action, event: Event) {
  const option = props.commands.find(item => keyOf(item) === (event.target as HTMLSelectElement).value)
  if (!option) return
  props.definition.command_bindings ||= {}
  props.definition.command_bindings[action.action_id] = { device_id: option.device_id, command_id: option.command.id, parameters: Object.fromEntries((option.command.parameters || []).map(parameter => [parameter.name, parameter.default])) }
  changed()
}
function setParameter(action: Action, name: string, event: Event) { const bound = binding(action); if (bound) { bound.parameters ||= {}; bound.parameters[name] = number(event); changed() } }
function toggleReadback(action: Action) { if (action.readback) delete action.readback; else action.readback = { target: { node_id: "", point_type: "value" }, timeout_ms: 5000, tolerance: 0 }; changed() }
function setReadbackTarget(action: Action, event: Event) { if (action.readback) { action.readback.target = { node_id: (event.target as HTMLSelectElement).value, point_type: "value" }; changed() } }
function add() {
  const action: Action = { action_id: props.outputs[0] ? businessId(props.outputs[0]) : crypto.randomUUID(), value: 0, readback: { timeout_ms: 5000, tolerance: 0 } }
  if (!props.outputs.length) { props.definition.command_bindings ||= {}; props.definition.command_bindings[action.action_id] = { device_id: "", command_id: "", parameters: {} }; delete action.readback }
  props.actions.push(action); changed()
}
</script>

<style scoped>
.machine-action-editor { display: grid; gap: 8px; margin: 8px 0; }
.action-row { display: flex; align-items: end; gap: 8px; flex-wrap: wrap; }
label { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--muted); }
input, select { min-height: 36px; border: 1px solid var(--line); border-radius: 8px; padding: 6px; background: rgba(255,255,255,.72); }
input { width: 100px; }
.note { color: var(--faint); font-size: 12px; }
</style>
