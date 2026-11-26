import type { EmsAlarm, HistoryRecord, Node as ApiNode, Point, Snapshot, StateValue } from "../../types/api"

function point(node: ApiNode, type: string): Point | undefined {
  return node.points.find(item => item.type === type && (item.tombstone || 0) % 2 === 0)
}

function rawValueText(sample: Pick<StateValue, "value">) {
  if (sample.value.number !== undefined) return String(Number(sample.value.number.toPrecision(12)))
  if (sample.value.bool !== undefined) return sample.value.bool ? "是" : "否"
  return sample.value.enum ?? "—"
}

export type LedgerDevice = {
  channel_id: string
  channel_name?: string
  server_id: number
  device_id?: string
  name: string
  business_node_id?: string
  business_node_name?: string
  profile_id?: string
  vendor?: string
  model?: string
  protocol?: string
  point_count?: number
  command_count?: number
  health?: string
  success_rate?: number
  avg_latency_ms?: number
  verified?: boolean
  verifier?: string
  verified_at?: string
}

export type QualityStats = {
  channel_id: string
  device_id?: string
  health?: string
  polls?: number
  failures?: number
  success_rate?: number
  avg_latency_ms?: number
  last_success?: string
  last_error?: string
  window_5m?: { polls: number; failures: number; success_rate?: number }
  window_1h?: { polls: number; failures: number; success_rate?: number }
}

export type OverviewMetric = {
  id: string
  label: string
  unit: string
  value: string
  quality: string
  time: string
  target?: string
}

export type OverviewScreen = {
  blocks?: { type: string; binding?: { metrics?: { point_id?: string; target?: string }[] } }[]
}

export function screenMetricTarget(screen: OverviewScreen | null, pointID: string) {
  const metrics = screen?.blocks?.find(block => block.type === "kpi")?.binding?.metrics || []
  const target = metrics.find(metric => metric.point_id === pointID)?.target
  return typeof target === "string" ? target.trim() : ""
}

// The design workbench deep link carries history=1; the overview consumes it
// in place instead of navigating to the standalone history page.
export function historyOpenQuery(query?: Record<string, unknown>) {
  if (String(query?.history ?? "") !== "1") return { open: false, point: "" }
  return { open: true, point: typeof query?.point === "string" ? query.point : "" }
}

export function preferredHistoryPoint(requested: string, options: { id: string }[]) {
  return requested && options.some(option => option.id === requested) ? requested : options[0]?.id || ""
}

const businessTypes = new Set(["siteRoot", "site", "group", "area", "plant", "energyUnit", "energy_unit", "unit", "device", "modbus_device"])

export function isBusinessLayer(node: ApiNode) {
  return businessTypes.has(node.type)
}

export function deviceKey(device: Pick<LedgerDevice, "channel_id" | "server_id" | "device_id">) {
  return device.device_id || `${device.channel_id}/${device.server_id}`
}

export function businessName(node?: ApiNode, fallback = "未命名点位") {
  const name = node ? point(node, "description")?.text?.trim() : ""
  return name || fallback
}

export function unitOf(node?: ApiNode) {
  return node ? point(node, "units")?.text || "" : ""
}

export function qualityLabel(quality?: string) {
  return ({ good: "有效", stale: "过期", invalid: "无效", uninitialized: "未初始化" } as Record<string, string>)[quality || ""] || quality || "—"
}

export function healthLabel(health?: string) {
  return ({ healthy: "在线", degraded: "不稳定", offline: "离线", unknown: "未知" } as Record<string, string>)[health || ""] || health || "—"
}

export function displaySample(sample: Pick<StateValue, "value"> | undefined, label = "") {
  if (!sample) return "—"
  if (sample.value.bool !== undefined) {
    if (/运行反馈|运行状态/.test(label)) return sample.value.bool ? "运行" : "停止"
    if (/远程模式/.test(label)) return sample.value.bool ? "远程" : "就地"
    if (/故障/.test(label)) return sample.value.bool ? "故障" : "正常"
  }
  return rawValueText(sample)
}

export function shortTime(value?: string) {
  if (!value) return "—"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleTimeString("zh-CN", { hour12: false })
}

export function metricForPoint(pointId: string, snapshot: Snapshot | null, values: StateValue[]): OverviewMetric {
  const node = snapshot?.nodes.find(item => item.id === pointId)
  const sample = values.find(item => item.point_id === pointId)
  const label = businessName(node)
  const evidence = node && point(node, 'easyemsInput')?.text
  if (evidence) {
    try {
      const input = JSON.parse(evidence)
      const sources: Record<string, string> = { aggregate: '聚合', calculation: '计算', configuration: '配置输入', manual: '人工输入', simulation: '模拟输入' }
      const quality = [qualityLabel(input.quality), sources[input.source] || '未知来源', input.software ? '软件值，非物理回读' : '', input.simulated ? '含模拟输入' : '', input.window ? `窗口 ${input.window} · 允许滞后 ≤ 一个窗口 · ${input.time && Number.isFinite(input.lag_seconds) ? `当前 ${Math.ceil(input.lag_seconds)}s` : '滞后未知（等待闭合窗口）'}` : '', input.time_source || '无观测时间依据'].filter(Boolean).join(' · ')
      const value = input.quality !== 'good' ? '—' : input.kind === 'enum' ? input.text || '—' : typeof input.value === 'number' && Number.isFinite(input.value) ? (input.kind === 'bool' ? (input.value ? '是' : '否') : String(input.value)) : '—'
      return { id: pointId, label, unit: input.unit || '', value, quality, time: shortTime(input.time) }
    } catch {
      return { id: pointId, label, unit: '', value: '—', quality: '来源证据无效', time: '—' }
    }
  }
  return {
    id: pointId,
    label,
    unit: unitOf(node),
    value: sample?.quality === "good" ? displaySample(sample, label) : "—",
    quality: sample ? qualityLabel(sample.quality) : "无数据",
    time: shortTime(sample?.source_time),
  }
}

export function descendantNodeIds(layerId: string, nodes: ApiNode[]) {
  const scope = new Set([layerId])
  let changed = true
  while (changed) {
    changed = false
    for (const node of nodes) {
      if (scope.has(node.id) || !scope.has(node.parent)) continue
      scope.add(node.id)
      changed = true
    }
  }
  return scope
}

export function boundDevicesForLayer<T extends Pick<LedgerDevice, "business_node_id">>(layerId: string, nodes: ApiNode[], devices: T[], memberIds: string[] = []) {
  const scope = descendantNodeIds(layerId, nodes)
  for (const id of memberIds) {
    if (nodes.some(node => node.id === id)) for (const child of descendantNodeIds(id, nodes)) scope.add(child)
  }
  return devices.filter(device => !!device.business_node_id && scope.has(device.business_node_id))
}

export function devicePointIds(nodes: ApiNode[], devices: LedgerDevice[]) {
  return nodes.filter(node => node.type === "modbusIo" && devices.some(device =>
    node.parent === device.channel_id && point(node, "id")?.value === device.server_id,
  )).map(node => node.id)
}

export function valueInLayer(value: Pick<StateValue, "device_id" | "point_id">, nodeIds: Set<string>, deviceIds: Set<string>, knownDeviceIds: Set<string>) {
  if (deviceIds.has(value.device_id)) return true
  if (knownDeviceIds.has(value.device_id)) return false
  return nodeIds.has(value.point_id) && nodeIds.has(value.device_id)
}

export function alarmInLayer(alarm: Pick<EmsAlarm, "device_id" | "point_id">, nodeIds: Set<string>, deviceIds: Set<string>) {
  return !!alarm.device_id && (deviceIds.has(alarm.device_id) || nodeIds.has(alarm.device_id)) || !!alarm.point_id && nodeIds.has(alarm.point_id)
}

export function formatPercent(value: number | undefined, polls: number | undefined) {
  return value === undefined || !polls ? "—" : `${value.toFixed(1)}%`
}

export function summarizeSuccessRate(channels: QualityStats[]) {
  const polls = channels.reduce((sum, channel) => sum + (channel.window_5m?.polls || 0), 0)
  const failures = channels.reduce((sum, channel) => sum + (channel.window_5m?.failures || 0), 0)
  return polls ? `${((polls - failures) / polls * 100).toFixed(1)}%` : "—"
}

export function commandStateText(state: string) {
  return ({
    accepted: "等待回读",
    executing: "执行中 · 等待回读",
    confirmed: "已回读",
    rejected: "已拒绝",
    failed: "失败",
    unknown: "结果未知",
    superseded: "已被后续命令取代",
    submitted: "已下发 · 无回读确认",
  } as Record<string, string>)[state] || state || "—"
}

export function isTerminalCommandState(state: string) {
  return ["confirmed", "rejected", "failed", "unknown", "superseded", "submitted"].includes(state)
}

export type UnitPoint = {
  id: string
  name: string
  server_id: number
  io_type: string
  read_only: boolean
  unit?: string
}

export function unitPointsForServer<T extends Pick<UnitPoint, "server_id">>(points: T[], serverID: number) {
  return points.filter(point => point.server_id === serverID)
}

export function writableUnitPoints<T extends Pick<UnitPoint, "io_type" | "read_only">>(points: T[]) {
  return points.filter(point => !point.read_only && ["modbusCoil", "modbusHoldingRegister"].includes(point.io_type))
}

export type HourlyTrendBucket = {
  time: Date
  record?: HistoryRecord
  value?: number
  height: number
  baseline: number
  negative: boolean
}

export function buildHourlyTrend(records: HistoryRecord[], start: Date, end: Date) {
  const startMs = start.getTime()
  const endMs = end.getTime()
  const byHour = new Map<number, HistoryRecord>()
  for (const record of records) {
    const time = new Date(record.source_time || record.received_time || "").getTime()
    if (!Number.isFinite(time) || time < startMs || time > endMs) continue
    if (record.kind === "quality_event" || record.quality === "invalid" || record.value === undefined || !Number.isFinite(record.value)) continue
    byHour.set(time, record)
  }
  const buckets: HourlyTrendBucket[] = Array.from({ length: 24 }, (_, index) => {
    const time = new Date(startMs + index * 60 * 60 * 1000)
    const record = byHour.get(time.getTime())
    return { time, record, value: record?.value, height: 0, baseline: 100, negative: false }
  })
  const values = buckets.flatMap(bucket => bucket.value === undefined ? [] : [bucket.value])
  const max = Math.max(...values, 0)
  const min = Math.min(...values, 0)
  const mixed = min < 0 && max > 0
  const scale = Math.max(Math.abs(max), Math.abs(min)) || 1
  for (const bucket of buckets) {
    bucket.baseline = mixed ? 50 : max === 0 && min < 0 ? 0 : 100
    if (bucket.value === undefined) continue
    bucket.height = Math.abs(bucket.value) / scale * (mixed ? 50 : 100)
    bucket.negative = bucket.value < 0
  }
  return { buckets, hasData: values.length > 0 }
}