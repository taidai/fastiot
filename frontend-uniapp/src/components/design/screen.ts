import type { ScreenConfig } from "../../types/api"

export type ScreenPointOption = { id: string; label: string }
export type ScreenMetric = { point_id?: string; metric?: string; label?: string; target?: string }
export type ScreenBlock = { type: string; binding: Record<string, any>; span: 1 | 2 }
export type ScreenDocument = {
  layer: string
  version: number
  title: string
  columns: number
  point_ids: string[]
  slots: Record<"top" | "main" | "side", string[]>
  blocks: ScreenBlock[]
  inherited_from?: string
}
export type ScreenLike = ScreenConfig | ScreenDocument

export const screenCatalog = [
  { type: "status", label: "状态条", slot: "top" }, { type: "kpi", label: "指标组", slot: "top" },
  { type: "flow", label: "工艺图 / 能流", slot: "main", kinds: ["现场", "区域"] },
  { type: "children", label: "下级对象", slot: "main", kinds: ["现场", "区域"] },
  { type: "members", label: "成员", slot: "main", kinds: ["单元"] },
  { type: "deviceData", label: "设备数据", slot: "main", kinds: ["设备"] },
  { type: "devices", label: "设备清单", slot: "main", kinds: ["现场", "区域", "单元"] },
  { type: "recent", label: "最近数据", slot: "main" }, { type: "alarm", label: "告警", slot: "side" },
  { type: "today", label: "今日执行", slot: "side" }, { type: "oplog", label: "操作记录", slot: "side", kinds: ["设备"] },
  { type: "trend", label: "趋势", slot: "side" }, { type: "summary", label: "运行摘要", slot: "side" },
] as const

export const automaticMetrics = [
  { key: "online_devices", label: "在线设备" }, { key: "active_alarms", label: "活动告警" },
  { key: "communication_success_rate", label: "通信成功率" }, { key: "data_freshness", label: "数据新鲜度" },
  { key: "today_energy", label: "今日电量" }, { key: "today_supply", label: "今日供水" },
  { key: "run_duration", label: "运行时长" },
] as const

const defaults: Record<string, string[]> = {
  现场: ["status", "kpi", "flow", "devices", "recent", "alarm", "today", "trend", "summary"],
  区域: ["status", "kpi", "children", "devices", "recent", "alarm", "today", "trend", "summary"],
  单元: ["status", "kpi", "members", "devices", "recent", "alarm", "today", "trend", "summary"],
  设备: ["status", "kpi", "deviceData", "recent", "alarm", "today", "oplog", "trend"],
}

export function screenBlockDefinition(type: string) {
  return screenCatalog.find(block => block.type === type)
}

export function automaticMetricLabel(key?: string) {
  return automaticMetrics.find(metric => metric.key === key)?.label || "—"
}

export function screenMetricKey(metric: ScreenMetric) {
  return metric.point_id ? `point:${metric.point_id}` : `auto:${metric.metric || ""}`
}

export function defaultScreen(layer: string, title: string, kind: string, points: ScreenPointOption[]): ScreenDocument {
  const types = defaults[kind] || defaults.现场
  const draft: ScreenDocument = { layer, version: 0, title, columns: 2, point_ids: [], slots: { top: [], main: [], side: [] }, blocks: [] }
  for (const type of types) addScreenBlock(draft, type, kind, points)
  return draft
}

export function makeScreenDraft(screen: ScreenLike | null, layer: string, title: string, kind: string, points: ScreenPointOption[]): ScreenDocument {
  if (!screen) return defaultScreen(layer, title, kind, points)
  if (isStructuredScreen(screen)) {
    const draft = clone(screen)
    const inherited = !!screen.inherited_from
    draft.layer = layer
    draft.title = title
    draft.version = inherited ? 0 : screen.version || 0
    delete draft.inherited_from
    if (inherited) dropOutOfScopeBindings(draft, points)
    return syncScreenPointIDs(draft)
  }
  const draft = defaultScreen(layer, title, kind, points)
  const inherited = !!(screen as ScreenDocument).inherited_from
  const allowed = new Set(points.map(point => point.id))
  const legacyIDs = [...new Set(screen.point_ids || [])].filter(id => !inherited || allowed.has(id))
  const metrics: ScreenMetric[] = legacyIDs.slice(0, 6).map(id => ({ point_id: id, label: points.find(point => point.id === id)?.label || id, target: "" }))
  for (const automatic of automaticMetrics) {
    if (metrics.length >= 6) break
    metrics.push({ metric: automatic.key, label: automatic.label, target: "" })
  }
  setBlockBinding(draft, "kpi", { metrics })
  setBlockBinding(draft, "recent", { point_ids: legacyIDs.slice(0, 4), columns: ["value", "quality", "time"], rows: 5 })
  setBlockBinding(draft, "trend", { point_id: legacyIDs[0] || "" })
  return syncScreenPointIDs(draft)
}

export function addScreenBlock(draft: ScreenDocument, type: string, kind: string, points: ScreenPointOption[]) {
  const definition = screenBlockDefinition(type)
  if (!definition || draft.blocks.some(block => block.type === type) || "kinds" in definition && !definition.kinds.includes(kind as never)) return false
  draft.blocks.push({ type, binding: defaultBinding(type, points), span: 2 })
  draft.slots[definition.slot].push(type)
  syncScreenPointIDs(draft)
  return true
}

export function removeScreenBlock(draft: ScreenDocument, type: string) {
  draft.blocks = draft.blocks.filter(block => block.type !== type)
  for (const slot of ["top", "main", "side"] as const) draft.slots[slot] = draft.slots[slot].filter(id => id !== type)
  syncScreenPointIDs(draft)
}

export function moveScreenBlock(draft: ScreenDocument, type: string, direction: -1 | 1) {
  const definition = screenBlockDefinition(type)
  if (!definition) return
  const ids = draft.slots[definition.slot]
  const index = ids.indexOf(type)
  const target = index + direction
  if (index < 0 || target < 0 || target >= ids.length) return
  ;[ids[index], ids[target]] = [ids[target], ids[index]]
}

export function setScreenMetric(draft: ScreenDocument, index: number, key: string, points: ScreenPointOption[]) {
  const block = draft.blocks.find(item => item.type === "kpi")
  const current = block?.binding.metrics?.[index] as ScreenMetric | undefined
  if (!block || !current) return
  if (key.startsWith("point:")) {
    const id = key.slice(6)
    current.point_id = id
    current.label = points.find(point => point.id === id)?.label || id
    delete current.metric
  } else {
    current.metric = key.slice(5)
    current.label = automaticMetricLabel(current.metric)
    delete current.point_id
  }
  current.target ||= ""
  syncScreenPointIDs(draft)
}

export function syncScreenPointIDs(draft: ScreenDocument) {
  const metrics = draft.blocks.find(block => block.type === "kpi")?.binding.metrics as ScreenMetric[] | undefined
  draft.point_ids = [...new Set((metrics || []).map(metric => metric.point_id).filter((id): id is string => !!id))]
  return draft
}

export function toScreenPayload(draft: ScreenDocument, applyToSame: boolean) {
  const payload = clone(syncScreenPointIDs(draft)) as ScreenDocument & { apply_to_same: boolean }
  delete payload.inherited_from
  payload.apply_to_same = applyToSame
  return payload
}

function dropOutOfScopeBindings(draft: ScreenDocument, points: ScreenPointOption[]) {
  const allowed = new Set(points.map(point => point.id))
  const metrics = draft.blocks.find(block => block.type === "kpi")?.binding.metrics as ScreenMetric[] | undefined
  for (const metric of metrics || []) {
    if (!metric.point_id || allowed.has(metric.point_id)) continue
    const used = new Set((metrics || []).map(item => item.metric).filter(Boolean))
    const fallback = automaticMetrics.find(item => !used.has(item.key)) || automaticMetrics[0]
    delete metric.point_id
    metric.metric = fallback.key
    metric.label = fallback.label
    metric.target = ""
  }
  const recent = draft.blocks.find(block => block.type === "recent")?.binding
  if (recent) recent.point_ids = (recent.point_ids || []).filter((id: string) => allowed.has(id))
  const trend = draft.blocks.find(block => block.type === "trend")?.binding
  if (trend && trend.point_id && !allowed.has(trend.point_id)) trend.point_id = ""
}

function defaultBinding(type: string, points: ScreenPointOption[]): Record<string, any> {
  if (type === "kpi") {
    const metrics: ScreenMetric[] = points.slice(0, 2).map(point => ({ point_id: point.id, label: point.label, target: "" }))
    for (const automatic of automaticMetrics) {
      if (metrics.length >= 6) break
      metrics.push({ metric: automatic.key, label: automatic.label, target: "" })
    }
    return { metrics }
  }
  if (type === "recent") return { point_ids: points.slice(0, 4).map(point => point.id), columns: ["value", "quality", "time"], rows: 5 }
  if (type === "trend") return { point_id: points[0]?.id || "" }
  if (type === "devices") return { columns: ["type", "state", "value", "communication", "updated"] }
  if (type === "deviceData") return { tabs: ["realtime", "parameters"] }
  if (type === "flow") return { detail: true }
  return {}
}

function setBlockBinding(draft: ScreenDocument, type: string, binding: Record<string, any>) {
  const block = draft.blocks.find(item => item.type === type)
  if (block) block.binding = binding
}

function isStructuredScreen(screen: ScreenLike): screen is ScreenDocument {
  return Array.isArray((screen as ScreenDocument).blocks) && !!(screen as ScreenDocument).slots
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}
