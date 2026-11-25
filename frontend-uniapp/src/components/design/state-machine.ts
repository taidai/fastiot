import type { Node, Point } from "../../types/api"
import type { Action, Condition, Target } from "./strategy"

function activePoint(node: Node, type: string): Point | undefined {
  return node.points.find(item => item.type === type && (item.tombstone || 0) % 2 === 0)
}
function nodeBusinessId(node: Node) { return activePoint(node, "businessId")?.text || node.id }
function nodeWritable(node: Node) {
  if (node.type === "variable") return true
  if (node.type !== "modbusIo") return false
  const ioType = activePoint(node, "modbusIoType")?.text
  return (activePoint(node, "readOnly")?.value || 0) === 0 && (ioType === "modbusCoil" || ioType === "modbusHoldingRegister")
}

export type StateTransition = {
  transition_id: string
  from: string
  event: string
  to: string
  conditions: Condition[]
  match?: "all" | "any"
  actions: Action[]
}
export type MachineCommandBinding = { device_id: string; command_id: string; parameters?: Record<string, number> }
export type MachineCommandOption = { device_id: string; device_name: string; command: import("../../pages/overview/operations").UnitCommand }
export type StateMachineDefinition = {
  initial_state: string
  scope: "self" | "subtree"
  states: string[]
  transitions: StateTransition[]
  state_actions?: Record<string, { entry?: Action[]; exit?: Action[] }>
  command_bindings?: Record<string, MachineCommandBinding>
  bindings: { points: Record<string, Target>; actions: Record<string, Target> }
}
export type StateMachineSimulation = {
  status: "matched" | "held"
  transition_id?: string
  from: string
  to: string
  reason?: string
  actions?: Action[]
  trace?: { transition_id: string; status: string; reason?: string }[]
}
export type StateMachineExecution = {
  request_id?: string
  version: number
  commands?: { request_id: string; node_id: string; status?: string; reason?: string }[]
  trace?: { transition_id: string; status: string; reason?: string }[]
  at: string
  event: string
  status: "confirmed" | "rejected" | "failed" | "unknown" | "held"
  transition_id?: string
  from: string
  to: string
  reason?: string
  actions?: { output: string; requested: number; applied: number; status: string; reason?: string }[]
}
export type StateMachineRecord = {
  id: string
  parent: string
  name: string
  revision: number
  version: number
  draft?: StateMachineDefinition
  restricted?: Record<string, string>
  applied?: StateMachineDefinition
  versions?: Record<number, StateMachineDefinition>
  current_state: string
  last_execution?: StateMachineExecution
  permissions: { edit?: boolean; publish?: boolean; execute?: boolean }
  simulation?: StateMachineSimulation
  execution?: StateMachineExecution
}

export function createStateMachineDefinition(inputId: string, outputId: string): StateMachineDefinition {
  const definition: StateMachineDefinition = {
    initial_state: "stopped", scope: "self", states: ["stopped", "running"], transitions: [],
    bindings: { points: {}, actions: {} },
  }
  if (!inputId || !outputId) return definition
  definition.bindings.points[inputId] = { node_id: inputId, point_type: "value" }
  definition.bindings.actions[outputId] = { node_id: outputId, point_type: "valueSet" }
  definition.transitions.push({
    transition_id: crypto.randomUUID(), from: "stopped", event: "start", to: "running",
    conditions: [{ point_id: inputId, operator: "eq", kind: "bool", value: 1 }],
    actions: [{ action_id: outputId, value: 1, readback: { timeout_ms: 5000, tolerance: 0 } }],
  })
  return definition
}

export function bindStateMachineDefinition(definition: StateMachineDefinition, nodes: Node[]) {
  const result = JSON.parse(JSON.stringify(definition, (_key, value) => {
    if (typeof value === "number" && !Number.isFinite(value)) throw new Error("请输入有效数字，不能把空输入当作 0。")
    return value
  })) as StateMachineDefinition
  // Go nil slices/maps are legal wire values. Normalize only this editor copy;
  // the record's draft, applied definition and published versions stay exact.
  result.transitions ??= []
  for (const transition of result.transitions) {
    transition.conditions ??= []
    transition.actions ??= []
  }
  result.bindings.points ??= {}
  result.bindings.actions ??= {}
  for (const [state, actions] of Object.entries(result.state_actions || {})) {
    if (!result.states.includes(state) && !actions.entry?.length && !actions.exit?.length) delete result.state_actions![state]
  }
  const discovered = withDiscoveredBindings(nodes)
  const referencedPoints = new Set(result.transitions.flatMap(transition => transition.conditions.filter(condition => condition.kind !== "time").map(condition => condition.point_id)))
  const stateActions = Object.values(result.state_actions || {}).flatMap(actions => [...(actions.entry || []), ...(actions.exit || [])])
  const referencedActions = new Set([...result.transitions.flatMap(transition => transition.actions), ...stateActions].map(action => action.action_id))
  for (const id of Object.keys(result.bindings.points)) {
    if (!referencedPoints.has(id)) delete result.bindings.points[id]
  }
  for (const id of Object.keys(result.bindings.actions)) {
    if (!referencedActions.has(id)) delete result.bindings.actions[id]
  }
  for (const id of referencedPoints) {
    const target = discovered.points[id]
    if (target && !result.bindings.points[id]?.node_id) result.bindings.points[id] = target
  }
  for (const id of Object.keys(result.command_bindings || {})) {
    if (!referencedActions.has(id)) delete result.command_bindings![id]
  }
  for (const id of referencedActions) {
    if (result.command_bindings?.[id]) continue
    const target = discovered.actions[id]
    if (target && !result.bindings.actions[id]?.node_id) result.bindings.actions[id] = target
  }
  return result
}

function withDiscoveredBindings(nodes: Node[]) {
  const bindings: StateMachineDefinition["bindings"] = { points: {}, actions: {} }
  for (const node of nodes) {
    const aggregate = !!activePoint(node, 'easyemsAggregate')?.text
    const derived = aggregate || node.type === 'virtualInput' || node.type === 'calculationPoint'
    if (node.type !== "variable" && node.type !== "modbusIo" && !derived) continue
    const id = nodeBusinessId(node)
    bindings.points[id] = { node_id: node.id, point_type: aggregate ? 'easyemsAggregateValue' : 'value', ...(derived ? { use: 'decision' as const } : {}) }
    if (nodeWritable(node)) bindings.actions[id] = { node_id: node.id, point_type: "valueSet" }
  }
  return bindings
}

export function stateMachineSimulationPayload(event: string, inputs: Record<string, number>, textInputs: Record<string, string>, at: string) {
  return { event, inputs, text_inputs: Object.fromEntries(Object.entries(textInputs).filter(([, value]) => value !== "")), ...(at ? { at } : {}) }
}

export function stateMachineInputValue(event: Event): string {
  return (event as CustomEvent<{ value?: string }>).detail?.value ?? (event.target as HTMLInputElement | null)?.value ?? ""
}

export function stateMachineInputNumber(event: Event) {
  const raw = stateMachineInputValue(event)
  return raw === "" ? Number.NaN : Number(raw)
}

export function stateMachineCommandText(actionID: string, definition: StateMachineDefinition | null | undefined, label: (id: string) => string) {
  const binding = definition?.command_bindings?.[actionID]
  if (!binding) return undefined
  const parameters = Object.entries(binding.parameters || {}).map(([name, value]) => `${name}=${value}`).join(", ")
  return `${label(binding.device_id)} · ${binding.command_id}（${parameters || "Profile 默认参数"}）`
}

export function executionStatusText(status: string) {
  return ({ confirmed: "已回读确认", accepted: "已接受，尚未确认", executing: "等待回读", submitted: "已提交，尚未确认", superseded: "已被替代", rejected: "请求被拒绝", failed: "执行失败", unknown: "结果未知", held: "保持原状态", pending: "等待执行", skipped: "未执行" } as Record<string, string>)[status] || status || "—"
}
