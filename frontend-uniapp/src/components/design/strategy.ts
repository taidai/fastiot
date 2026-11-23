import type { Node, Point } from "../../types/api"

function activePoint(node: Node, type: string): Point | undefined {
  return node.points.find(item => item.type === type && (item.tombstone || 0) % 2 === 0)
}

export type Condition = {
  point_id: string
  operator: string
  value: number
  kind?: "number" | "bool" | "enum" | "time"
  text?: string
  start?: string
  end?: string
  weekday_mask?: number
}

export type Action = { action_id: string; value: number }
export type Row = {
  row_id: string
  conditions: Condition[]
  actions: Action[]
  hold_ms?: number
  cooldown_ms?: number
}
export type Target = { node_id: string; point_type?: string; key?: string }
export type DecisionDocument = {
  table: { rows: Row[] }
  bindings: { points: Record<string, Target>; actions: Record<string, Target> }
  scenarios?: unknown[]
}
export type Evaluation = {
  row_id?: string
  actions?: (Action & { blocked?: boolean; reason?: string })[]
  state?: string
  blocked?: string[]
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

export function withBindings(document: DecisionDocument, nodes: Node[]): DecisionDocument {
  const result = JSON.parse(JSON.stringify(document)) as DecisionDocument
  for (const node of nodes) {
    const id = businessId(node)
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

export function moveRows<T>(rows: T[], index: number, direction: -1 | 1): T[] {
  const target = index + direction
  if (target < 0 || target >= rows.length) return [...rows]
  const result = [...rows]
  ;[result[index], result[target]] = [result[target], result[index]]
  return result
}

export function createSimulationPayload(
  document: DecisionDocument,
  inputs: Record<string, number>,
  textInputs: Record<string, string>,
  at: string,
) {
  return {
    document,
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

export function conditionText(condition: Condition, label: (id: string) => string) {
  if (condition.kind === "time") return timeConditionText(condition)
  const value = condition.kind === "bool" ? (condition.value ? "是" : "否") : condition.kind === "enum" ? condition.text || "—" : condition.value
  return `${label(condition.point_id)} ${operators[condition.operator] || condition.operator} ${value}`
}
