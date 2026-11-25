export type Point = {
  type: string
  key: string
  time: string
  value?: number
  text?: string
  tombstone?: number
  origin?: string
}

export type Node = {
  id: string
  type: string
  parent: string
  points: Point[]
}

export type Snapshot = {
  user: {
    user_id: string
    email?: string
    policy: { admin: boolean; role: string; actions?: string[] }
    actions?: string[]
  }
  roots: string[]
  nodes: Node[]
  session_timeout_seconds?: number
  industry?: { id: string; version: string; installed: boolean; required: boolean; blocked: boolean }
}

export type StateValue = {
  device_id: string
  point_id: string
  value: { kind: string; number?: number; bool?: boolean; enum?: string }
  quality: string
  source_time?: string
  received_time?: string
  reason?: string
  time_source?: string
  last_attempt_time?: string
  expires_at?: string
}

export type EmsAlarm = {
  id: string
  key: string
  revision: number
  severity: "info" | "warning" | "critical"
  title: string
  detail: string
  device_id?: string
  point_id?: string
  active: boolean
  state: "active" | "acknowledged" | "resolved" | "closed"
  created_at: string
  updated_at: string
  acked_by?: string
  acked_at?: string
  resolved_at?: string
  closed_at?: string
  permissions?: { ack: boolean; maintain: boolean }
}

export type HistoryRecord = {
  point_id: string
  device_id?: string
  source_time?: string
  received_time?: string
  bucket_end?: string
  time_source?: string
  late?: boolean
  aggregate?: string
  sample_count?: number
  origin?: string
  quality?: string
  value?: number
  text?: string
  source: string
  unit?: string
  kind?: "observation" | "quality_event"
  reason?: string
}

export type HistoryPointResult = {
  node_id: string
  device_id?: string
  source: string
  records: HistoryRecord[]
  matched: number
  returned: number
  truncated: boolean
  next_before?: string
  next_before_count?: number
  sample_interval_ms?: number
  actual_interval_ms?: number
  aggregation?: "raw" | "mean"
  retention?: { kind: "unknown" | "count_per_point_per_kind"; limit?: number }
}

export type HistoryQueryResponse = {
  results: HistoryPointResult[]
  start?: string
  end?: string
  aggregate?: string
  total_matched: number
}

export type ScreenConfig = {
  title: string
  columns: number
  point_ids: string[]
}

export function point(node: Node, type: string) {
  return node.points.find((item) => item.type === type && (item.tombstone || 0) % 2 === 0)
}

export function nameOf(node: Node) {
  return point(node, "description")?.text || node.id
}

export function kindOf(nodeType: string) {
  if (nodeType === "site") return "site"
  if (["energyUnit", "energy_unit", "unit"].includes(nodeType)) return "unit"
  if (["device", "modbus_device"].includes(nodeType)) return "device"
  if (nodeType === "group" || nodeType === "variable") return "area"
  return "area"
}

export function valueText(sample?: StateValue) {
  if (!sample) return "—"
  if (sample.value.number !== undefined) return String(sample.value.number)
  if (sample.value.bool !== undefined) return sample.value.bool ? "是" : "否"
  return sample.value.enum ?? "—"
}
