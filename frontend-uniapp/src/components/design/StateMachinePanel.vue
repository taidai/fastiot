<template>
  <view class="machine-panel">
    <view class="panel-heading">
      <view><strong>通用状态机</strong><p class="note">发布定义后，由显式事件驱动；全部动作回读确认才保存下一状态。</p></view>
      <button class="ghost" :disabled="readOnly || busy || !parent || !canCreate" @click="createMachine">＋ 新建状态机</button>
    </view>
    <view v-if="!parent" class="empty">未选择作用对象，不能创建状态机。</view>
    <template v-else>
      <view v-if="machines.length" class="machine-tabs">
        <button v-for="machine in machines" :key="machine.id" :class="{ active: machine.id === selectedId }" :disabled="dirty && machine.id !== selectedId" @click="selectMachine(machine.id)">{{ machine.name }}</button>
      </view>
      <view v-if="!machines.length" class="empty">此对象还没有状态机。新建只保存草稿，不会执行动作。</view>
      <template v-if="record && draft">
        <view class="machine-summary">
          <span class="chip ok">过程状态 {{ record.current_state }}（非设备反馈）</span>
          <span class="chip">{{ record.version ? `已发布 v${record.version}` : '尚未发布' }}</span>
          <span v-if="dirty" class="chip warn">有未保存修改</span>
        </view>
        <view class="event-panel">
          <strong>事件模拟与执行</strong>
          <view class="chips machine-events"><button v-for="event in machineEvents" :key="event" @click="runEvent(event)">{{ event }}</button><button @click="resetSimulation">复位模拟</button></view>
          <view class="event-grid">
            <label>事件<input v-model="eventName" @input="simulation = null"></label>
            <label v-for="id in referencedInputs" :key="id">{{ displayName(id) }}
              <input v-if="inputKind(id) === 'enum'" :value="textInputs[id] || ''" @input="textInputs[id] = inputValue($event); simulation = null">
              <select v-else-if="inputKind(id) === 'bool'" :value="inputs[id] ?? 0" @change="inputs[id] = inputNumber($event); simulation = null"><option :value="1">是</option><option :value="0">否</option></select>
              <input v-else type="number" step="any" :value="inputs[id] ?? 0" @input="inputs[id] = inputNumber($event); simulation = null">
            </label>
            <label>模拟时刻<input type="datetime-local" v-model="simAt"></label>
          </view>
          <view class="machine-actions"><button class="ghost" :disabled="busy" @click="simulate">模拟守卫</button><checkbox-group class="confirm" @change="confirmed = $event.detail.value.includes('execute')"><label><checkbox value="execute" :checked="confirmed" /> 确认执行真实动作</label></checkbox-group><button class="primary" :disabled="readOnly || busy || !confirmed || !record.version || dirty || !record.permissions.execute" @click="execute">{{ pendingEvent ? `查询原事件 ${pendingEvent.event}` : '执行事件' }}</button></view>
          <view v-if="simulation" class="execution-result"><strong>{{ simulation.status === 'matched' ? `可转移：${simulation.from} → ${simulation.to}` : `保持 ${simulation.to}` }}</strong><span>{{ simulation.reason || '模拟未写设备' }}</span><view v-for="trace in simulation.trace || []" :key="trace.transition_id">{{ trace.transition_id }}：{{ trace.status }} {{ trace.reason || '' }}</view></view>
          <view v-if="record.last_execution" class="execution-result" :class="record.last_execution.status"><strong>{{ executionStatusText(record.last_execution.status) }}：{{ record.last_execution.from }} → {{ record.last_execution.to }}</strong><span>{{ record.last_execution.reason || '执行结果已持久化' }}</span><span>来源 v{{ record.last_execution.version }} · 事件 {{ record.last_execution.request_id || '旧记录' }}</span><view v-for="command in record.last_execution.commands || []" :key="command.request_id">命令 {{ command.request_id }}：{{ executionStatusText(command.status || 'unknown') }} {{ command.reason || '' }}</view><view v-for="(action, index) in record.last_execution.actions || []" :key="index">{{ executionActionName(action.output) }}：{{ executionStatusText(action.status) }} {{ action.reason || '' }}</view></view>
        </view>
        <view class="transition-heading"><strong>转移表</strong><button class="ghost" @click="addTransition">＋ 转移</button></view>
        <div class="table-wrap"><table class="mini-table machine-table"><thead><tr><th>当前</th><th>事件</th><th>守卫</th><th>下一状态</th><th>动作</th><th></th></tr></thead><tbody><tr v-for="transition in draft.transitions" :key="transition.transition_id"><td>{{ transition.from === '*' ? '任意' : transition.from }}</td><td>{{ transition.event }}</td><td>{{ guardText(transition) }}</td><td>{{ transition.to }}</td><td>{{ transitionActionText(transition) }}</td><td class="row-actions"><button class="ghost" @click="editTransition(transition.transition_id)">编辑</button><button class="ghost danger" :disabled="readOnly || busy || !record.permissions.edit" @click="removeTransition(transition.transition_id)">删除</button></td></tr></tbody></table></div>
        <details class="definition-editor" :open="editorOpen" @toggle="syncEditorOpen"><summary>编辑状态与转移定义</summary>
        <fieldset class="definition" :disabled="readOnly || busy || !record.permissions.edit">
          <view class="form-grid">
            <label>状态（逗号分隔）<input :value="statesText" @input="statesText = inputValue($event); changed()"></label>
            <label>初始状态<select v-model="draft.initial_state" @change="changed"><option v-for="state in states" :key="state">{{ state }}</option></select></label>
            <label>作用范围<select v-model="draft.scope" @change="changed"><option value="self">仅本对象直接点位</option><option value="subtree">本对象及下级</option></select></label>
          </view>
          <view class="transition-heading"><strong>转移表</strong><button class="ghost" @click="addTransition">＋ 转移</button></view>
          <view v-for="(transition, index) in draft.transitions" v-show="!expandedTransition || expandedTransition === transition.transition_id" :key="transition.transition_id" class="transition">
            <view class="transition-grid">
              <label>当前<select v-model="transition.from" @change="changed"><option value="*">任意状态</option><option v-for="state in states" :key="state">{{ state }}</option></select></label>
              <label>事件<input v-model="transition.event" @input="changed"></label>
              <label>下一状态<select v-model="transition.to" @change="changed"><option v-for="state in states" :key="state">{{ state }}</option></select></label>
              <label>守卫关系<select v-model="transition.match" @change="changed"><option value="all">全部满足 AND</option><option value="any">任一满足 OR</option></select></label>
              <button class="ghost danger" @click="draft.transitions.splice(index, 1); changed()">删除转移</button>
            </view>
            <view class="transition-detail">
              <strong>守卫</strong>
              <view v-for="(condition, conditionIndex) in transition.conditions" :key="conditionIndex" class="inline-editor">
                <select :value="condition.point_id" @change="condition.point_id = inputValue($event); changed()"><option v-for="node in available" :key="node.id" :value="businessId(node)">{{ nameOf(node) }}</option></select>
                <select :value="condition.kind || 'number'" @change="changeGuardKind(condition, $event)"><option value="number">数值</option><option value="bool">布尔</option><option value="enum">枚举</option></select>
                <select v-model="condition.operator" @change="changed"><option value="eq">=</option><option value="ne">≠</option><option v-if="!condition.kind || condition.kind === 'number'" value="gt">></option><option v-if="!condition.kind || condition.kind === 'number'" value="ge">≥</option><option v-if="!condition.kind || condition.kind === 'number'" value="lt"><</option><option v-if="!condition.kind || condition.kind === 'number'" value="le">≤</option></select>
                <input v-if="condition.kind === 'enum'" :value="condition.text" @input="condition.text = inputValue($event); changed()">
                <select v-else-if="condition.kind === 'bool'" :value="condition.value" @change="condition.value = inputNumber($event); changed()"><option :value="1">是</option><option :value="0">否</option></select>
                <input v-else type="number" step="any" :value="condition.value" @input="condition.value = inputNumber($event); changed()">
                <button class="ghost" @click="transition.conditions.splice(conditionIndex, 1); changed()">×</button>
              </view>
              <button class="link" :disabled="!available.length" @click="addGuard(transition)">＋ 守卫条件</button>
            </view>
            <view class="transition-detail">
              <strong>动作序列</strong>
              <StateMachineActionEditor :actions="transition.actions" :definition="draft" :inputs="available" :outputs="outputs" :commands="commandOptions" @change="changed" />
              <span v-if="!transition.actions.length" class="src">无动作：仅记录状态转移</span>
            </view>
          </view>
          <view v-if="!draft.transitions.length" class="empty">尚无转移。</view>
          <details v-for="state in states" :key="state" class="transition-detail"><summary>{{ state }} · 进入／退出动作</summary>
            <strong>退出 {{ state }}</strong><StateMachineActionEditor :actions="lifecycleActions(state, 'exit')" :definition="draft" :inputs="available" :outputs="outputs" :commands="commandOptions" @change="changed" />
            <strong>进入 {{ state }}</strong><StateMachineActionEditor :actions="lifecycleActions(state, 'entry')" :definition="draft" :inputs="available" :outputs="outputs" :commands="commandOptions" @change="changed" />
          </details>
          <p class="note">执行顺序：退出动作 → 转移动作 → 进入动作；全部确认后才推进过程状态。</p>
        </fieldset>

        <view class="machine-actions">
          <button class="ghost" :disabled="readOnly || busy || !dirty || !record.permissions.edit" @click="saveDraft">保存草稿</button>
          <button class="primary" :disabled="readOnly || busy || !record.permissions.publish" @click="confirmPublish = true">发布草稿</button>
        </view>
        <view v-if="confirmPublish" class="publish-confirm" role="alert"><span>发布后只能通过当前版本执行；草稿修改不会改变已发布定义。</span><button class="ghost" @click="confirmPublish = false">取消</button><button class="primary" @click="publish">确认发布</button></view>
        </details>
      </template>
    </template>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p><p v-if="error" class="error" role="alert">{{ error }}</p>
  </view>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue"
import { request } from "../../api/client"
import { nameOf } from "../../types/api"
import type { Snapshot } from "../../types/api"
import { businessId, conditionText, actionText, isRamped, isWritable } from "./strategy"
import type { Condition, Action } from "./strategy"
import { bindStateMachineDefinition, createStateMachineDefinition, executionStatusText, stateMachineSimulationPayload, stateMachineInputValue as inputValue, stateMachineInputNumber as inputNumber, stateMachineCommandText } from "./state-machine"
import type { StateMachineDefinition, StateMachineRecord, StateMachineSimulation, StateTransition, MachineCommandOption } from "./state-machine"
import StateMachineActionEditor from "./StateMachineActionEditor.vue"
import { loadUnitModel } from "../../pages/overview/operations"
import type { LedgerDevice } from "../../pages/overview/overview"

const props = withDefaults(defineProps<{ snapshot: Snapshot | null; parent: string; readOnly?: boolean }>(), { readOnly: false })
const emit = defineEmits<{ (event: "refresh"): void }>()
const machines = ref<StateMachineRecord[]>([])
const selectedId = ref("")
const record = ref<StateMachineRecord | null>(null)
const draft = ref<StateMachineDefinition | null>(null)
const statesText = ref("stopped,running")
const canCreate = ref(false)
const dirty = ref(false)
const busy = ref(false)
const error = ref("")
const notice = ref("")
const confirmPublish = ref(false)
const editorOpen = ref(false)
const expandedTransition = ref("")
const eventName = ref("start")
const confirmed = ref(false)
const inputs = ref<Record<string, number>>({})
const textInputs = ref<Record<string, string>>({})
const simAt = ref("")
const simulation = ref<StateMachineSimulation | null>(null)
const commandOptions = ref<MachineCommandOption[]>([])
const pendingEvent = ref<{ request_id: string; event: string; revision: number; confirm: true } | null>(null)
const eventStorageKey = (id: string) => `fastiot.machine-event.${id}`
const nodes = computed(() => props.snapshot?.nodes || [])
const available = computed(() => nodes.value.filter(node => node.type === "variable" || node.type === "modbusIo"))
const outputs = computed(() => available.value.filter(node => isWritable(node) && !isRamped(node)))
const states = computed(() => statesText.value.split(/[,，]/).map(value => value.trim()).filter(Boolean))
const referencedInputs = computed(() => [...new Set((draft.value?.transitions || []).flatMap(transition => transition.conditions.map(condition => condition.point_id)))])
const machineEvents = computed(() => [...new Set((draft.value?.transitions || []).map(transition => transition.event).filter(Boolean))])

onMounted(load)
watch(() => props.parent, async () => {
  const discarded = dirty.value
  dirty.value = false
  await load()
  if (discarded) notice.value = "对象已切换；上一个状态机的未保存修改未保存。"
})
function displayName(id: string) { const node = nodes.value.find(item => item.id === id) || nodes.value.find(item => businessId(item) === id); return node ? nameOf(node) : id || "—" }
function executionActionName(id: string) {
  const version = record.value?.last_execution?.version
  const source = version ? record.value?.versions?.[version] : undefined
  return stateMachineCommandText(id, source, displayName) || displayName(id)
}
function inputKind(id: string) { return (draft.value?.transitions || []).flatMap(transition => transition.conditions).find(condition => condition.point_id === id)?.kind || "number" }
function syncEditorOpen(event: Event) { editorOpen.value = (event.target as HTMLDetailsElement).open }
function editTransition(id: string) { expandedTransition.value = id; editorOpen.value = true }
function removeTransition(id: string) {
  if (!draft.value) return
  const index = draft.value.transitions.findIndex(transition => transition.transition_id === id)
  if (index >= 0) draft.value.transitions.splice(index, 1)
  if (expandedTransition.value === id) expandedTransition.value = ""
  changed()
}
function guardText(transition: StateTransition) { return transition.conditions.length ? transition.conditions.map(condition => conditionText(condition, displayName)).join(transition.match === "any" ? " ∨ " : " ∧ ") : "—" }
function transitionActionText(transition: StateTransition) { return transition.actions.length ? transition.actions.map(action => stateMachineCommandText(action.action_id, draft.value, displayName) || actionText(action, displayName)).join(" → ") : "仅记录状态转移" }
async function runEvent(event: string) { eventName.value = event; await simulate() }
function resetSimulation() { simulation.value = null; confirmed.value = false; notice.value = "模拟结果已复位；真实当前状态未改变。" }
function changed() { dirty.value = true; simulation.value = null; error.value = ""; notice.value = "" }
async function load() {
  busy.value = true; error.value = ""
  try {
    const result = await request<{ state_machines: StateMachineRecord[]; can_create: boolean }>("/state-machines")
    machines.value = result.state_machines.filter(machine => machine.parent === props.parent)
    canCreate.value = result.can_create
    if (!machines.value.some(machine => machine.id === selectedId.value)) selectedId.value = machines.value[0]?.id || ""
    selectMachine(selectedId.value)
    await loadCommands()
  } catch (cause) { machines.value = []; record.value = null; error.value = cause instanceof Error ? cause.message : "状态机读取失败" }
  finally { busy.value = false }
}
async function loadCommands() {
  commandOptions.value = []
  try {
    const ledger = await request<{ devices: (LedgerDevice & { bound?: boolean })[] }>("/device-ledger")
    for (const device of ledger.devices.filter(item => item.bound && item.business_node_id)) {
      const model = await loadUnitModel(device)
      for (const command of model.commands || []) commandOptions.value.push({ device_id: device.business_node_id!, device_name: device.business_node_name || device.name, command })
    }
  } catch (cause) { notice.value = cause instanceof Error ? `声明命令目录不可用：${cause.message}` : "声明命令目录不可用" }
}
function lifecycleActions(state: string, phase: 'entry' | 'exit'): Action[] {
  if (!draft.value) return []
  draft.value.state_actions ||= {}
  draft.value.state_actions[state] ||= {}
  draft.value.state_actions[state][phase] ||= []
  return draft.value.state_actions[state][phase]!
}
function selectMachine(id: string) {
  if (dirty.value && id !== selectedId.value) return
  selectedId.value = id
  try { pendingEvent.value = JSON.parse(localStorage.getItem(eventStorageKey(id)) || 'null') } catch { pendingEvent.value = null }
  record.value = machines.value.find(machine => machine.id === id) || null
  draft.value = record.value ? bindStateMachineDefinition(record.value.draft, nodes.value) : null
  statesText.value = draft.value?.states.join(",") || "stopped,running"
  dirty.value = false; simulation.value = null; confirmed.value = false; confirmPublish.value = false
  expandedTransition.value = ""; editorOpen.value = false
}
async function createMachine() {
  busy.value = true; error.value = ""
  try {
    const definition = createStateMachineDefinition("", "")
    const result = await request<StateMachineRecord>("/state-machines", "POST", { parent: props.parent, name: "设备过程状态机", definition })
    machines.value.push(result); selectMachine(result.id); notice.value = "草稿已创建，尚未发布。"
  } catch (cause) { error.value = cause instanceof Error ? cause.message : "创建失败" }
  finally { busy.value = false }
}
function currentDefinition() {
  if (!draft.value) throw new Error("没有可保存的状态机")
  draft.value.states = states.value
  if (!draft.value.states.includes(draft.value.initial_state)) draft.value.initial_state = draft.value.states[0] || ""
  return bindStateMachineDefinition(draft.value, nodes.value)
}
async function saveDraft() {
  if (!record.value || busy.value) return
  busy.value = true; error.value = ""
  try {
    const result = await request<StateMachineRecord>(`/state-machines/${record.value.id}/draft`, "POST", { definition: currentDefinition(), revision: record.value.revision })
    replaceRecord(result); notice.value = "草稿已保存，运行版本未改变。"
  } catch (cause) { error.value = cause instanceof Error ? cause.message : "保存失败" }
  finally { busy.value = false }
}
async function publish() {
  if (!record.value || busy.value) return
  error.value = ""
  try {
    if (dirty.value) await saveDraft()
    if (!record.value || dirty.value) return
    busy.value = true
    const result = await request<StateMachineRecord>(`/state-machines/${record.value.id}/publish`, "POST", { revision: record.value.revision })
    replaceRecord(result); confirmPublish.value = false; notice.value = "发布成功；仅在显式事件执行时动作。"
  } catch (cause) { error.value = cause instanceof Error ? cause.message : "发布失败" }
  finally { busy.value = false }
}
function replaceRecord(result: StateMachineRecord) {
  const index = machines.value.findIndex(machine => machine.id === result.id)
  if (index >= 0) machines.value[index] = result
  record.value = result; draft.value = bindStateMachineDefinition(result.draft, nodes.value); statesText.value = result.draft.states.join(","); dirty.value = false
}
function addTransition() {
  if (!draft.value || states.value.length < 1) return
  const transition: StateTransition = { transition_id: crypto.randomUUID(), from: states.value[0], event: "event", to: states.value[0], conditions: [], match: "all", actions: [] }
  draft.value.transitions.push(transition)
  expandedTransition.value = transition.transition_id
  editorOpen.value = true
  changed()
}
function addGuard(transition: StateTransition) { const first = available.value[0]; if (first) { transition.conditions.push({ point_id: businessId(first), operator: "gt", value: 0 }); changed() } }
function changeGuardKind(condition: Condition, event: Event) { const kind = inputValue(event) as Condition["kind"]; condition.kind = kind; condition.operator = kind === "number" ? "gt" : "eq"; condition.value = kind === "bool" ? 1 : 0; condition.text = kind === "enum" ? condition.text || "" : ""; changed() }
async function simulate() {
  if (!record.value || busy.value) return
  busy.value = true; error.value = ""
  try {
    const result = await request<StateMachineRecord>(`/state-machines/${record.value.id}/simulate`, "POST", {
      ...stateMachineSimulationPayload(eventName.value, inputs.value, textInputs.value, simAt.value),
      definition: currentDefinition(),
    })
    simulation.value = result.simulation || null
  } catch (cause) { error.value = cause instanceof Error ? cause.message : "模拟失败" }
  finally { busy.value = false }
}
async function execute() {
  if (!record.value || busy.value || !confirmed.value) return
  busy.value = true; error.value = ""; simulation.value = null
  try {
    pendingEvent.value ||= { request_id: crypto.randomUUID(), event: eventName.value, revision: record.value.revision, confirm: true }
    localStorage.setItem(eventStorageKey(record.value.id), JSON.stringify(pendingEvent.value))
    const result = await request<StateMachineRecord>(`/state-machines/${record.value.id}/execute`, "POST", pendingEvent.value)
    localStorage.removeItem(eventStorageKey(record.value.id)); pendingEvent.value = null
    replaceRecord(result); confirmed.value = false; notice.value = result.execution?.status === "confirmed" ? "事件已回读确认。" : "事件未完整确认，未保存下一状态。"
    emit("refresh")
  } catch (cause) {
    const code = (cause as { code?: string })?.code
    error.value = code === "state_persistence_unconfirmed" || code === "state_machine_run_unconfirmed"
      ? "执行记录保存未确认。再次确认只查询原事件，不重复下发；请核对过程状态与设备回读。"
      : cause instanceof Error ? cause.message : "执行失败"
  }
  finally { busy.value = false }
}
</script>

<style scoped>
.machine-panel { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; min-width: 0; }
.panel-heading, .machine-summary, .machine-actions, .transition-heading { display: flex; align-items: center; justify-content: space-between; gap: 10px; flex-wrap: wrap; }
.note, .src { margin: 4px 0 0; color: var(--faint); font-size: 12px; }
.machine-tabs { display: flex; gap: 6px; overflow-x: auto; }
.machine-tabs button { flex: none; border: 1px solid var(--glass-border); border-radius: 999px; padding: 6px 12px; background: rgba(255,255,255,.45); }
.machine-tabs button.active { border-color: var(--accent); color: var(--accent); }
.definition { border: 0; padding: 0; margin: 12px 0 0; }
.definition-editor { border-top: 1px solid var(--line); padding-top: 12px; }
.definition-editor summary { cursor: pointer; font-weight: 600; }
.chips { display: flex; gap: 4px; flex-wrap: wrap; }
.chips button { border: 1px solid rgba(255,255,255,.7); border-radius: 999px; padding: 4px 10px; background: rgba(255,255,255,.48); color: var(--muted); font-size: 11.5px; }
.chips button:hover { border-color: var(--accent); color: var(--accent); }
.machine-table { font-size: 12px; }
.machine-table td { vertical-align: top; }
.row-actions { white-space: nowrap; text-align: right; }
.form-grid, .transition-grid, .event-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 9px; }
.form-grid label, .transition-grid label, .event-grid label, .inline-editor label { display: flex; flex-direction: column; gap: 4px; color: var(--muted); font-size: 11px; }
input, select { min-height: 38px; border: 1px solid var(--line); border-radius: 8px; padding: 6px 8px; background: rgba(255,255,255,.72); }
.transition { margin-top: 10px; border-left: 3px solid rgba(35,112,95,.28); border-radius: 0 10px 10px 0; padding: 11px; background: rgba(255,255,255,.32); }
.transition-detail { margin-top: 10px; font-size: 12px; }
.inline-editor { display: flex; align-items: end; gap: 6px; flex-wrap: wrap; margin: 6px 0; }
.inline-editor label { flex-direction: row; align-items: center; }
.inline-editor input { width: 100px; }
.event-panel { display: grid; gap: 10px; }
.confirm { display: flex; align-items: center; gap: 5px; color: var(--muted); font-size: 12px; }
.execution-result { display: grid; overflow-wrap: anywhere; gap: 5px; border: 1px solid var(--line); border-radius: 10px; padding: 10px; background: rgba(255,255,255,.42); font-size: 12px; }
.execution-result.unknown { border-color: #d3a34d; }.execution-result.failed { border-color: var(--danger); }
.publish-confirm { display: flex; align-items: center; gap: 9px; padding: 10px; border: 1px solid #eeddb9; border-radius: 10px; background: #fdf7ea; }
.notice { margin: 0; color: var(--mint); }.error { margin: 0; }.danger { color: var(--danger); }
@media (max-width: 760px) { .inline-editor { align-items: stretch; }.machine-actions { justify-content: flex-start; } }
</style>
