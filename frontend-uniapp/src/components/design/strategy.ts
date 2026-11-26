import type { Node, Point, StateValue } from "../../types/api"

function activePoint(node: Node, type: string): Point | undefined {
  return node.points.find(item => item.type === type && (item.tombstone || 0) % 2 === 0)
}

export type RuleAccess = { draft_restricted?: boolean; applied_restricted?: boolean; history_restricted?: boolean; trace_restricted?: boolean; diagnostics_restricted?: boolean }
export function getRuleAccess(node?: Node | null): RuleAccess {
  try { return JSON.parse(node ? activePoint(node, 'ruleAccess')?.text || '{}' : '{}') }
  catch { return { draft_restricted: true, applied_restricted: true } }
}

export type Condition = {
  join?: "AND" | "OR"
  point_id: string
  operator: string
  value: number
  kind?: "number" | "bool" | "enum" | "time"
  text?: string
  start?: string
  end?: string
  weekday_mask?: number
}

export type Readback = {
  target?: Target
  timeout_ms: number
  tolerance?: number
}
export type RuleNotification = { recipient_ids: string[]; title: string; body?: string }
export type Action = { action_id: string; value?: number; kind?: "notify"; notification?: RuleNotification; delay_ms?: number; readback?: Readback }
export type Row = {
  row_id: string
  conditions: Condition[]
  match?: "all" | "any"
  actions: Action[]
  hold_ms?: number
  cooldown_ms?: number
}
export type Target = { use?: 'decision' | 'protection' | 'interlock' | 'readback'; node_id: string; point_type?: string; key?: string; command_id?: string; parameter?: string }
export type DecisionDocument = {
  scope?: "self" | "descendants"
  revision?: string
  table: { rows: Row[] }
  bindings: { points: Record<string, Target>; actions: Record<string, Target> }
  scenarios?: unknown[]
}
export type Evaluation = {
  row_id?: string
  actions?: (Action & { blocked?: boolean; reason?: string })[]
  state?: string
  blocked?: string[]
  rows?: { row_id: string; state: string; reasons?: string[]; held_ms: number; required_ms: number }[]
  time_checks?: { start?: string; end?: string; weekday_mask?: number; satisfied: boolean }[]
}

export function parseDocument(text?: string): DecisionDocument {
  let raw: any = {}
  if (text) {
    try {
      raw = JSON.parse(text)
    } catch {
      throw new Error("草稿格式无法读取")
    }
  }
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new Error("草稿格式无法读取")
  const rows = Array.isArray(raw.table?.rows) ? raw.table.rows : []
  return {
    ...raw,
    table: {
      rows: rows.map((row: any) => ({
        ...row,
        row_id: String(row.row_id || ""),
        conditions: Array.isArray(row.conditions) ? row.conditions : [],
        actions: Array.isArray(row.actions) ? row.actions : [],
      })),
    },
    bindings: {
      points: { ...(raw.bindings?.points || {}) },
      actions: { ...(raw.bindings?.actions || {}) },
    },
  }
}

// Only traverse the authorized snapshot. A hidden ancestor is a hard boundary.
export function rulesForNode(rules: Node[], nodes: Node[], nodeID: string): Node[] {
  const ancestors = new Set<string>()
  let current = nodes.find(node => node.id === nodeID)
  while (current && !ancestors.has(current.id)) {
    ancestors.add(current.id)
    current = nodes.find(node => node.id === current!.parent)
  }
  return rules.filter(rule => {
    if (!ancestors.has(rule.parent)) return false
    if (rule.parent === nodeID) return true
    try { return parseDocument(activePoint(rule, "applied")?.text).scope === "descendants" }
    catch { return false }
  })
}

export function businessId(node: Node) {
  return activePoint(node, "businessId")?.text || node.id
}

export function isWritable(node: Node) {
  if (node.type === "variable") return true
  if (node.type !== "modbusIo") return false
  const ioType = activePoint(node, "modbusIoType")?.text
  const readOnly = activePoint(node, "readOnly")?.value || 0
  return readOnly === 0 && (ioType === "modbusCoil" || ioType === "modbusHoldingRegister")
}

// A ramped output is moved gradually by the runtime. The sequenced action path
// waits for a single readback and is not wired for ramp steps, so new
// sequence-based rows must not offer it.
export function isRamped(node: Node) {
  const text = activePoint(node, "easyemsSafety")?.text
  if (!text) return false
  try {
    const config = JSON.parse(text)
    return Number(config?.ramp_per_second) > 0
  } catch {
    return false
  }
}

export function isDerivedInput(node: Node) {
  return node.type === 'virtualInput' || node.type === 'calculationPoint' || !!activePoint(node, 'easyemsAggregate')?.text
}

export function withBindings(document: DecisionDocument, nodes: Node[]): DecisionDocument {
  const result = JSON.parse(JSON.stringify(document)) as DecisionDocument
  for (const node of nodes) {
    const id = businessId(node)
    if (isDerivedInput(node)) {
      result.bindings.points[id] ||= { node_id: node.id, point_type: activePoint(node, 'easyemsAggregate')?.text ? 'easyemsAggregateValue' : 'value', use: 'decision' }
    }
    if (node.type === "variable" || node.type === "modbusIo") {
      result.bindings.points[id] ||= { node_id: node.id, point_type: "value" }
      if (isWritable(node)) result.bindings.actions[id] ||= { node_id: node.id, point_type: "valueSet" }
    }
    if (node.type === "group" && activePoint(node, "easyemsAllocator")?.text) {
      result.bindings.actions[id] ||= { node_id: node.id, point_type: "valueSet" }
    }
  }
  return result
}

export function referencedDocument(document: DecisionDocument): DecisionDocument {
  const points = new Set(document.table.rows.flatMap(row => row.conditions.filter(c => c.kind !== "time").map(c => c.point_id)))
  const actions = new Set(document.table.rows.flatMap(row => row.actions.filter(a => a.kind !== 'notify').map(a => a.action_id)))
  return { ...document, bindings: {
    points: Object.fromEntries(Object.entries(document.bindings.points).filter(([id]) => points.has(id))),
    actions: Object.fromEntries(Object.entries(document.bindings.actions).filter(([id]) => actions.has(id))),
  } }
}

export function moveRows<T>(rows: T[], index: number, direction: -1 | 1): T[] {
  const target = index + direction
  if (target < 0 || target >= rows.length) return [...rows]
  const result = [...rows]
  ;[result[index], result[target]] = [result[target], result[index]]
  return result
}

function conditionIdentity(condition: Condition): string {
  return JSON.stringify([
    condition.kind || "number",
    condition.point_id || "",
    condition.operator || "",
    condition.value ?? null,
    condition.text || "",
    condition.start || "",
    condition.end || "",
    condition.weekday_mask || 0,
    condition.join || "",
  ])
}

// The condition set of a row includes its point conditions and its time window.
// A time window alone is a meaningful difference, so it must not read as a
// duplicate.
function conditionSetKey(conditions: Condition[]): string {
  return conditions.map(conditionIdentity).sort().join("\u0000")
}

// Rows that share a condition set are meaningless duplicates. Member order does
// not matter, but a partial overlap with another row is not a duplicate.
export function duplicateRowIndexes(rows: Pick<Row, "conditions">[]): Set<number> {
  const keys = rows.map(row => conditionSetKey(row.conditions))
  const counts = new Map<string, number>()
  for (const key of keys) counts.set(key, (counts.get(key) || 0) + 1)
  const duplicates = new Set<number>()
  keys.forEach((key, index) => { if ((counts.get(key) || 0) > 1) duplicates.add(index) })
  return duplicates
}

export function ruleWeekdayMask(event: unknown): number {
  const values = (event as { detail?: { value?: unknown } }).detail?.value
  if (!Array.isArray(values) || values.some(value => !/^[0-6]$/.test(String(value)))) throw new Error('星期选择无效')
  return values.reduce((mask, value) => mask | (1 << Number(value)), 0)
}

export function ruleInputValue(event: unknown): string {
  const input = event as { detail?: { value?: unknown }; target?: { value?: unknown } }
  return String(input.detail?.value ?? input.target?.value ?? '')
}

export function ruleInputNumber(event: unknown): number {
  const raw = ruleInputValue(event).trim()
  const value = Number(raw)
  return raw && Number.isFinite(value) ? value : Number.NaN
}

export function liveSimulationInputs(document: DecisionDocument, values: StateValue[], nodes: Node[] = []) {
  const inputs: Record<string, number> = {}
  const textInputs: Record<string, string> = {}
  for (const [id, target] of Object.entries(document.bindings.points)) {
    const node = nodes.find(node => node.id === target.node_id)
    if (node && isDerivedInput(node)) {
      try {
        const input = JSON.parse(activePoint(node, 'easyemsInput')?.text || '{}')
        if (input.quality !== 'good' || !input.time) continue
        if (input.kind === 'enum' && input.text) textInputs[id] = input.text
        else if (typeof input.value === 'number' && Number.isFinite(input.value)) inputs[id] = input.value
      } catch { /* No assumed value when source evidence is absent or invalid. */ }
      continue
    }
    const sample = values.find(value => value.point_id === target.node_id)
    if (!sample || sample.quality !== 'good' || !sample.source_time) continue
    if (sample.expires_at && Date.parse(sample.expires_at) <= Date.now()) continue
    if (sample.value.kind === 'number' && Number.isFinite(sample.value.number)) inputs[id] = sample.value.number!
    else if (sample.value.kind === 'bool' && sample.value.bool !== undefined) inputs[id] = sample.value.bool ? 1 : 0
    else if (sample.value.kind === 'enum' && sample.value.enum) textInputs[id] = sample.value.enum
  }
  return { inputs, textInputs }
}

export function createSimulationPayload(
  document: DecisionDocument,
  inputs: Record<string, number>,
  textInputs: Record<string, string>,
  at: string,
  heldMS = 0,
) {
  if (!Number.isFinite(heldMS) || heldMS < 0 || Object.values(inputs).some(value => !Number.isFinite(value))) throw new Error('模拟数值无效，不会提交请求')
  const simulationDocument = heldMS > 0 ? { ...document, simulation: { held_ms: heldMS } } : document
  return {
    document: simulationDocument,
    inputs,
    text_inputs: Object.fromEntries(Object.entries(textInputs).filter(([, value]) => value !== "")),
    ...(at ? { at } : {}),
  }
}

const operators: Record<string, string> = { gt: ">", ge: "≥", eq: "=", ne: "≠", le: "≤", lt: "<" }
const weekdays: [string, number][] = [["日", 0], ["一", 1], ["二", 2], ["三", 3], ["四", 4], ["五", 5], ["六", 6]]

export function timeConditionText(condition: Pick<Condition, "start" | "end" | "weekday_mask">) {
  const mask = condition.weekday_mask || 0
  const days = !mask || mask === 127 ? "每天" : weekdays.filter(([, bit]) => mask & (1 << bit)).map(([day]) => `周${day}`).join("、")
  const start = condition.start || ""
  const end = condition.end || ""
  const window = !start || !end ? "全天" : start > end ? `${start}–次日${end}` : `${start}–${end}`
  return `${days} ${window}`
}

export function actionText(action: Action, label: (id: string) => string) {
  const parts: string[] = []
  if (action.delay_ms) parts.push(`延时 ${action.delay_ms / 1000}s`)
  if (action.kind === 'notify') {
    parts.push(`站内通知「${action.notification?.title || '未填写标题'}」→ ${action.notification?.recipient_ids.length || 0} 个指定账号（成功仅指持久创建）`)
    return parts.join(' → ')
  }
  parts.push(`${label(action.action_id)} = ${action.value}`)
  if (action.readback) parts.push(`回读 ${action.readback.timeout_ms / 1000}s（容差 ${action.readback.tolerance || 0}）`)
  return parts.join(" → ")
}

export function conditionText(condition: Condition, label: (id: string) => string) {
  if (condition.kind === "time") return timeConditionText(condition)
  const value = condition.kind === "bool" ? (condition.value ? "是" : "否") : condition.kind === "enum" ? condition.text || "—" : condition.value
  return `${label(condition.point_id)} ${operators[condition.operator] || condition.operator} ${value}`
}
