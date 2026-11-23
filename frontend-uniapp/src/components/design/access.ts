import type { Snapshot } from "../../types/api"

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
  verifier?: string
  verified_at?: string
  device_serial?: string
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
