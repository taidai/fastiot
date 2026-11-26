import type { Node, Snapshot } from "../../types/api"

export type ChannelPoint = {
  id: string
  name: string
  function?: string
  address: number
  format?: string
  scale?: number
  offset?: number
  kind?: string
  read_only?: boolean
}

export type EmsChannel = {
  id: string
  node_id?: string
  persisted?: boolean
  protocol?: string
  address?: string
  period_ms?: number
  devices?: { id?: string; device_id?: string; server_id?: number; health: string; points?: ChannelPoint[] }[]
}

export type ProfilePointSpec = {
  name: string
  function?: string
  address?: number
  format?: string
  scale?: number
  offset?: number
  unit?: string
  read_only?: boolean
  role?: string
  note?: string
  required?: boolean
  enum?: Record<string, string>
}

export type Profile = {
  id: string
  vendor: string
  model: string
  protocol?: string
  points?: number
  commands?: number
  verified?: boolean
  verification?: string
  source?: string
  identity_hint?: string
  notes?: string[]
  custom?: boolean
  point_specs?: ProfilePointSpec[]
  command_specs?: {
    id: string
    name: string
    point?: string
    value?: number
    readback?: string
    readback_value?: number
    timeout_ms?: number
    readback_tolerance?: number
    parameters?: { name: string; label?: string; point?: string }[]
  }[]
}

export type LedgerDevice = {
  channel_id: string
  channel_name: string
  server_id: number
  name: string
  business_node_id?: string
  business_node_name?: string
  profile_id?: string
  vendor?: string
  model?: string
  protocol: string
  address?: string
  point_count: number
  command_count: number
  health: string
  success_rate: number
  avg_latency_ms: number
  verified: boolean
  bound?: boolean
  verifier?: string
  verified_at?: string
  device_serial?: string
  // Absent means "unknown" (no Profile resolved); 0 is a real zero. Neither is
  // evidence of access, so deviceAccessState keeps both pending.
  required_points?: number
  verified_points?: number
}

export type UnitPoint = {
  id: string
  name: string
  server_id: number
  address: number
  io_type: string
  data_format?: string
  read_only: boolean
  unit?: string
  scale: number
  offset?: number
}

export type UnitModelConfig = {
  name: string
  profile_id: string
  profile_version?: string
  mapping_version?: string
  business_node_id?: string
  point_map: Record<string, string>
  verified: boolean
  verifier?: string
  verified_at?: string
  device_serial?: string
  verification?: string
  replaced_from?: string
  verification_required?: boolean
  replacement?: unknown
}

export type UnitModel = {
  channel_id: string
  server_id: number
  model?: UnitModelConfig
  profiles: Profile[]
  profile_errors?: string[]
  points: UnitPoint[]
  commands?: { id: string; name: string; available: boolean; reason?: string }[]
  missing?: string[]
}

export type ProfilePointVerdict = {
  name: string
  point_id?: string
  verdict: "exact" | "scaled" | "mismatch" | "missing"
  reason?: string
  differences?: { point: string; field: string; profile: string; existing: string }[]
}

export type ProfileApplyResult = {
  server: number
  profile_id: string
  mapped: number
  exact: number
  scaled: number
  mismatched: number
  missing: string[]
  blocked: string[]
  points: ProfilePointVerdict[]
}

export type ProfileApplyResponse = {
  profile_id: string
  dry_run: boolean
  results: ProfileApplyResult[]
  applied: number
  preview_version?: string
  create_points?: { description: string; address: number; data_format: string; scale: number; units?: string; read_only: boolean }[]
}

export function requiredProfilePoints(profile: Pick<Profile, "point_specs"> | null) {
  return new Set((profile?.point_specs || []).filter(point => point.required ?? !point.note?.split(/\s+/).includes("OPTIONAL")).map(point => point.name))
}

export function profilePointCounts(profile: Pick<Profile, "point_specs"> | null) {
  const total = profile?.point_specs?.length || 0
  const required = requiredProfilePoints(profile).size
  return { total, required, optional: Math.max(0, total - required) }
}

export type DeviceAccessState = "unbound" | "pending" | "access"

export type DeviceAccessFacts = {
  bound?: boolean
  profile_id?: string
  verified?: boolean
  required_points?: number
  verified_points?: number
}

// ADR 0011: a bound device is not yet connected. "access" needs machine L2
// evidence for every required point: a positive required-point count that the
// verified-point count meets. The manual `verified` sign-off is not evidence,
// and unknown counts (legacy snapshots or a missing Profile) or a zero required
// count are not evidence either. Those devices stay "已绑定待验证".
export function deviceAccessState(device: DeviceAccessFacts): DeviceAccessState {
  const bound = device.bound ?? !!device.profile_id
  if (!bound) return "unbound"
  const required = device.required_points
  const verified = device.verified_points
  if (typeof required === "number" && required > 0 && typeof verified === "number" && verified >= required) {
    return "access"
  }
  return "pending"
}

export function deviceAccessLabel(state: DeviceAccessState) {
  if (state === "access") return "已接入"
  return state === "pending" ? "已绑定待验证" : "未绑定"
}

export function deviceAccessChipClass(state: DeviceAccessState) {
  return state === "access" ? "ok" : state === "unbound" ? "warn" : ""
}

export type BusinessBindingState = { bound: boolean; verified: boolean }

// Reads only persisted UnitModel bindings from Modbus channels. A persisted
// model carries the bound flag and the manual sign-off; it never carries L2
// point evidence, so deviceBindingProgress derives the state with the same
// deviceAccessState rather than counting `verified` as access.
export function businessBindingStates(nodes: Node[]) {
  const states = new Map<string, BusinessBindingState>()
  for (const node of nodes) {
    if (node.type !== "modbus") continue
    for (const point of node.points) {
      if (point.type !== "easyemsUnitModel" || (point.tombstone || 0) % 2 !== 0 || !point.text) continue
      try {
        const model = JSON.parse(point.text) as { business_node_id?: string; verified?: boolean }
        if (!model.business_node_id) continue
        const previous = states.get(model.business_node_id)
        states.set(model.business_node_id, {
          bound: true,
          verified: (previous?.verified || false) || model.verified === true,
        })
      } catch { /* Invalid persisted models remain unbound rather than guessed. */ }
    }
  }
  return states
}

export function boundBusinessIDs(nodes: Node[]) {
  return new Set(businessBindingStates(nodes).keys())
}

export function deviceBindingProgress(nodes: Node[]) {
  const progress = { devices: 0, bound: 0, verified: 0 }
  const states = businessBindingStates(nodes)
  for (const node of nodes) {
    if (node.type !== "device" || node.parent === "root") continue
    progress.devices++
    const state = states.get(node.id)
    if (!state) continue
    progress.bound++
    if (deviceAccessState({ bound: state.bound, verified: state.verified }) === "access") progress.verified++
  }
  return progress
}

export type BatchBindingRow = {
  line: number
  device: string
  model: string
  channel: string
  station: number
  device_id?: string
  profile_id?: string
  channel_id?: string
  valid: boolean
  status: "ready" | "mapped" | "blocked" | "failed"
  errors: string[]
  created_points: number
  mapped: number
  missing: string[]
}

export type BatchAccessResponse = {
  dry_run: boolean; rows: BatchBindingRow[]; applied: number; preview_version?: string
  plans?: Record<number, { match: ProfileApplyResult; create_points: NonNullable<ProfileApplyResponse["create_points"]>; missing_required: string[] }>
}

export type BatchVerificationRecord = { at: string; actor: string; level: string; passed: boolean; reasons?: string[]; current?: boolean }

export function batchVerificationText(records: BatchVerificationRecord[], level: "L1" | "L2") {
  const record = records.filter(record => record.level === level).slice(-1)[0]
  if (!record) return `${level}：未验证`
  return `${level}：${record.current ? record.passed ? "通过" : "未通过" : "已失效"} · ${record.at} · ${record.actor}${record.reasons?.length ? ` · ${record.reasons.join("；")}` : ""}`
}

export type BatchVerificationOutcome = {
  device: string
  level: "L1" | "L2"
  state: "passed" | "failed" | "unknown" | "unverified"
  detail?: string
}

export function batchVerificationOutcome(device: string, level: "L1" | "L2", records: BatchVerificationRecord[], error = ""): BatchVerificationOutcome {
  if (error) return { device, level, state: "unknown", detail: error }
  const record = records.filter(item => item.level === level).slice(-1)[0]
  if (!record) return { device, level, state: "unverified" }
  if (!record.current) return { device, level, state: "unknown", detail: "记录已失效，需重新验证" }
  if (record.passed) return { device, level, state: "passed", detail: record.at }
  return { device, level, state: "failed", detail: [record.at, ...(record.reasons || [])].filter(Boolean).join(" · ") }
}

export type BatchVerificationSummary = {
  total: number
  passed: number
  failed: number
  unknown: number
  unverified: number
  text: string
  failures: string[]
}

// ADR 0007: a batch summary must surface every failed or unconfirmed device;
// counts alone must never read as an all-clear.
export function summarizeBatchVerification(outcomes: BatchVerificationOutcome[]): BatchVerificationSummary {
  const passed = outcomes.filter(item => item.state === "passed").length
  const failed = outcomes.filter(item => item.state === "failed").length
  const unknown = outcomes.filter(item => item.state === "unknown").length
  const unverified = outcomes.filter(item => item.state === "unverified").length
  const total = outcomes.length
  const failures = outcomes.filter(item => item.state !== "passed").map(item => {
    const state = item.state === "failed" ? "未通过" : item.state === "unknown" ? "结果未确认" : "未验证"
    return `${item.device} · ${item.level}：${state}${item.detail ? `（${item.detail}）` : ""}`
  })
  const text = !total
    ? "没有已套用设备，未执行验证。"
    : passed === total
      ? `全部验证完成：${total} 项全部通过。`
      : `全部验证完成：${passed}/${total} 项通过；${failed} 项未通过，${unknown + unverified} 项未验证或结果未确认。逐台结果保留在下方，失败项不会被汇总掩盖。`
  return { total, passed, failed, unknown, unverified, text, failures }
}

export function batchCSV(devices: { id: string; name: string }[], profileID: string, channelID: string, startStation: number) {
  const cell = (value: string | number) => {
    const text = String(value)
    return /[",\r\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text
  }
  const rows = devices.map((device, index) => [device.id, profileID, channelID, startStation + index].map(cell).join(","))
  return ["设备,型号,通道,站号", ...rows].join("\n")
}

function normalizedName(value: string) {
  return value.trim().replace(/[\s　]/g, "").replace(/／/g, "/").toLowerCase()
}

export function automaticPointMap(profile: Pick<Profile, "point_specs">, points: UnitPoint[], serverID: number) {
  const byName = new Map<string, UnitPoint>()
  for (const point of points) {
    const key = normalizedName(point.name)
    if (point.server_id === serverID && key && !byName.has(key)) byName.set(key, point)
  }
  const result: Record<string, string> = {}
  for (const spec of profile.point_specs || []) {
    const point = byName.get(normalizedName(spec.name))
    if (point) result[spec.name] = point.id
  }
  return result
}

export function prepareModelSave(config: UnitModelConfig): UnitModelConfig {
  const pointMap: Record<string, string> = {}
  for (const [name, id] of Object.entries(config.point_map || {})) {
    if (id.trim()) pointMap[name] = id.trim()
  }
  return {
    ...config,
    name: config.name.trim(),
    point_map: pointMap,
    verifier: config.verifier?.trim() || "",
    device_serial: config.device_serial?.trim() || "",
    verification: config.verification?.trim() || "",
  }
}

export function canConfigureAccess(snapshot: Snapshot | null) {
  if (!snapshot) return false
  const policy = snapshot.user.policy
  if (policy.admin) return true
  if (snapshot.user.actions) return snapshot.user.actions.includes("configure")
  if (policy.actions?.length) return policy.actions.includes("configure")
  return policy.role === "admin" || policy.role === "implementer"
}

export function canCommandAccess(snapshot: Snapshot | null) {
  if (!snapshot) return false
  const policy = snapshot.user.policy
  if (policy.admin) return true
  if (snapshot.user.actions) return snapshot.user.actions.includes("command")
  if (policy.actions?.length) return policy.actions.includes("command")
  return ["admin", "implementer", "operator"].includes(policy.role)
}

export function safeVerificationCommands(profile: Pick<Profile, "point_specs" | "command_specs">) {
  const points = new Map((profile.point_specs || []).map(point => [point.name, point]))
  return (profile.command_specs || []).filter(command => {
    const point = points.get(command.point || "")
    return !!point && point.read_only === false && point.note?.split(/\s+/).includes("L3_SAFE") && !!command.readback && (command.parameters?.length || 0) === 0 && typeof command.value === "number" && Number.isFinite(command.value)
  })
}

export function verificationError(config: Pick<UnitModelConfig, "profile_id" | "verified" | "verifier" | "verified_at" | "device_serial" | "point_map">, missingRequired: string[] = []) {
  if (!config.profile_id) return "先选择并保存设备 Profile"
  if (config.verified && (!config.verifier?.trim() || !config.verified_at || !config.device_serial?.trim())) {
    return "标记已验证时必须填写验证人、日期和设备序列号"
  }
  if (config.verified && !Object.values(config.point_map || {}).some(Boolean)) {
    return "至少映射并核对一个 Profile 点位，才能标记已验证"
  }
  if (config.verified && missingRequired.length) {
    return `命令必需点未映射，不能标记已验证：${missingRequired.join("、")}`
  }
  return ""
}
