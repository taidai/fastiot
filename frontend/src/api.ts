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

export type CalculationDefinition = {
  operator: "add" | "divide"; inputs: string[]; kind: "number"; unit: string; max_skew_ms?: number
}
export type CalculationSample = {
  id: string; kind: string; unit: string; value?: number; quality: string; reason?: string
  source: string; simulated: boolean; version: string; time_source?: string
  source_time?: string; received_time?: string; changed_at?: string; valid_until?: string
}
export type CalculationResult = {
  id: string; batch_id: string; version: number; definition: CalculationDefinition
  quality: string; reason?: string; value?: number; unit: string; calculated_at?: string; valid_until?: string
  inputs?: CalculationSample[]; leaves?: CalculationSample[]; simulated?: boolean; retained?: CalculationResult
}
export type CalculationPoint = {
  id: string; parent: string; name: string; revision: number; version: number
  draft?: CalculationDefinition; applied?: CalculationDefinition; state: CalculationResult
  permissions: { edit?: boolean; apply?: boolean }
}
export type CalculationSource = { id: string; name: string; kind: string; unit: string; source: string }
export type CalculationList = {
  calculations: CalculationPoint[]; sources: CalculationSource[]; can_create: boolean; evaluated_at: string; batch_id: string
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
export type Row = { row_id: string; conditions: Condition[]; actions: Action[]; hold_ms?: number; cooldown_ms?: number }
export type Target = { node_id: string; point_type?: string; key?: string }
export type Scenario = {
  scenario_id: string
  name?: string
  inputs?: Record<string, number>
  text_inputs?: Record<string, string>
  at?: string
  expect_row?: string
  expect_actions?: Action[]
}
export type ScenarioResult = {
  scenario_id: string
  name?: string
  passed: boolean
  row_id?: string
  actions?: { action_id: string; value: number; blocked?: boolean; reason?: string }[]
  reason?: string
  detail?: string
}
export type Document = {
  table: { rows: Row[] }
  bindings: { points: Record<string, Target>; actions: Record<string, Target> }
  scenarios?: Scenario[]
}
export type Snapshot = {
  user: { user_id: string; email?: string; policy: { admin: boolean; role: string; actions?: string[] }; actions?: string[] }
  roots: string[]
  nodes: Node[]
  session_timeout_seconds?: number
}
export type TimeCheck = { start?: string; end?: string; weekday_mask?: number; satisfied: boolean }
export type Evaluation = { row_id?: string; actions?: Action[]; time_checks?: TimeCheck[] }

export class ApiError extends Error {
  constructor(message: string, public status: number, public code = "") {
    super(message)
  }
}

export async function login(email: string, password: string) {
  const response = await fetch("/api/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  })
  if (!response.ok) {
    const result = await response.json().catch(() => ({ error: "" }))
    const friendly = response.status === 403 ? "账号或密码不正确"
      : response.status === 409 ? "该账号同时登录的会话数已达上限，请稍后再试或退出其他会话"
      : response.status === 503 ? "认证服务暂不可用，请稍后重试"
      : ""
    throw new ApiError(friendly || "登录失败，请稍后重试", response.status)
  }
  return (await response.json()) as { token: string }
}

export async function request<T>(token: string, path: string, method = "GET", body?: unknown, signal?: AbortSignal): Promise<T> {
  const response = await fetch(`/api${path}`, {
    signal,
    method,
    headers: {
      Authorization: `Bearer ${token}`,
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!response.ok) {
    const result = await response.json().catch(() => ({ error: "" }))
    throw new ApiError(errorMessage(result.error, response.status), response.status, result.error || "")
  }
  return (await response.json()) as T
}

// 服务端错误码 → 面向值班员的业务文案；未知码按状态给出可操作提示。
const errorTexts: Record<string, string> = {
  alarm_evidence_unconfirmed: "处理可能已生效，但过程留痕未确认；请重新读取并联系维护人员，不要重复提交",
  alarm_history_unavailable: "报警过程记录不可用，不能据此判断没有报警",
  alarm_objects_unavailable: "报警对象配置暂不可读，当前范围和状态无法确认",
  invalid_input_definition: "输入定义不合法，请核对类型、单位、用途、范围与有效期",
  invalid_input_value: "输入值类型不符或超出允许范围，原值未改变",
  input_not_writable: "此输入尚未应用、未开放运行修改或属于配置常量",
  revision_conflict: "输入已被其他操作更新，请刷新后重新确认",
  operation_conflict: "此操作标识已使用，请读取当前状态后重新操作",
  impact_confirmation_conflict: "变更内容或下游引用已变化，请重新预览并确认",
  input_persistence_unconfirmed: "保存结果尚未确认，请先重新读取，不要盲目重复提交",
  invalid_input_document: "输入存储内容不可用，请联系工程人员核查",
  virtual_input_rollback_unsupported: "项目含虚拟输入，暂不支持项目版本回退；未执行写入",
  history_export_limit_exceeded: "完整导出超过 10000 条上限，请缩小点位或时间范围后重试",
  export_required: "当前账号没有导出权限",
  read_only: "该点位只读，不能写入",
  node_disabled: "该点位已停用，不能写入",
  manual_takeover: "该点位处于人工接管状态，自动写入已拒绝",
  invalid_safety_config: "安全配置无效，已拒绝写入；请检查点位安全设置",
  invalid_command: "命令格式不完整，已拒绝写入",
  operation_not_allowed: "当前状态下不允许此操作",
  request_id_conflict: "请求标识已被其他写入使用，请读取当前状态后再试",
  command_not_accepted: "命令未被接受",
  command_state_unavailable: "命令状态暂不可用，请刷新确认后再试",
  admin_required: "需要管理员权限",
  forbidden: "当前账号无权执行此操作",
  not_found: "对象不存在或已删除",
  requires_energy_unit: "只能对能源设备执行此操作",
  requires_plant: "只能对电站执行此操作",
  energy_unit_not_found: "能源设备不存在",
  plant_not_found: "电站不存在",
  no_rule: "还没有规则，请先配置并保存草稿",
  rule_id_required: "规则缺少标识",
  invalid_rule: "规则内容不合法",
  invalid_members: "成员配置不合法",
  description_required: "请填写名称",
  rated_power_required: "请填写大于 0 的额定功率",
  meter_point_required: "请选择 PCC 主表点位",
  save_failed: "保存失败，请稍后重试",
  create_failed: "创建失败，请稍后重试",
  list_failed: "读取失败，请稍后刷新",
  invalid_login: "账号或密码不正确",
  authentication_unavailable: "认证服务暂不可用，请稍后重试",
  session_limit_reached: "该账号同时登录的会话数已达上限",
  capability_load_failed: "读取设备能力失败",
  plant_resolve_failed: "无法确定所属电站，请先创建电站",
  channel_busy: "该地址正被运行中的通道占用",
  source_not_accepted: "源项目还没有发布版本，不能作为复用源",
  source_must_be_site: "只能从站点创建新项目",
  target_parent_unavailable: "当前账号无权在项目下新建站点",
  preflight_failed: "预检存在阻断项，确认后才能冻结版本",
  preflight_unavailable: "预检暂不可用，请检查运行服务",
  version_save_failed: "版本保存失败，请稍后重试",
  version_not_found: "版本不存在或已删除",
  requires_site: "只能对站点执行此操作",
  servers_required: "请填写至少一个站号",
  unknown_profile: "Profile 不存在，请重新选择",
  slots_must_be_96: "计划必须是 96 个时段",
  slot_out_of_range: "计划值超出允许范围",
  deepseek_not_configured: "尚未配置 DeepSeek 凭据；本次未发送任何内容",
  outbound_blocked: "出站内容含疑似敏感的现场信息，已阻止发送",
  confirmation_required: "请先核对出站清单并确认后再生成",
  model_unavailable: "模型调用失败，请检查凭据与网关后重试",
  invalid_request: "请求内容不合法，或截图超过大小上限",
}

export function errorMessage(code: string, status: number): string {
  if (code && errorTexts[code]) return errorTexts[code]
  // Safety refusals carry an explanatory sentence, not a code. Show the
  // server's reason verbatim so the operator sees why the write was refused.
  if (code && /\s/.test(code)) return code
  if (status === 401) return "登录已失效，请重新登录"
  if (status === 403) return "当前账号无权执行此操作"
  if (status === 404) return "对象不存在或已删除"
  if (status >= 500) return "服务暂时不可用，请稍后重试"
  return "操作未完成，请稍后重试"
}

export async function importCSV(token: string, path: string, csv: string) {
  const response = await fetch(`/api${path}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "text/csv" },
    body: csv,
  })
  if (!response.ok) {
    const result = await response.json().catch(() => ({ error: "CSV 导入失败" }))
    throw new ApiError(result.error || "CSV 导入失败", response.status)
  }
  return (await response.json()) as { imported?: number; preview?: boolean; count?: number }
}

export async function downloadCSV(token: string, path: string, filename = "fastiot-modbus-points.csv") {
  const response = await fetch(`/api${path}`, { headers: { Authorization: `Bearer ${token}` } })
  if (!response.ok) throw new ApiError("CSV 导出失败", response.status)
  const url = URL.createObjectURL(await response.blob())
  const link = document.createElement("a")
  link.href = url
  link.download = filename
  link.click()
  URL.revokeObjectURL(url)
}

export function point(node: Node, type: string) {
  return node.points.find((item) => item.type === type && (item.tombstone || 0) % 2 === 0)
}

export function nameOf(node: Node) {
  return point(node, "description")?.text || node.id
}

export function businessId(node: Node) {
  return point(node, "businessId")?.text || node.id
}

export type AlarmInputEvidence = { point_id: string; value: number; kind: string; unit?: string; quality: string; source: string; source_time?: string; received_time?: string; config_stamp?: string }

export type EmsAlarm = {
  id: string
  key: string
  revision: number
  input_state?: "abnormal" | "normal" | "unknown"
  input_reason?: string
  input?: AlarmInputEvidence
  acked_at?: string
  resolved_at?: string
  closed_at?: string
  target?: { kind: "point" | "device" | "node"; node_id?: string; device_id?: string; point_id?: string; channel_id?: string; server_id?: number; point_ids?: string[]; mapping_changed?: boolean }
  permissions?: { ack: boolean; maintain: boolean }
  severity: "info" | "warning" | "critical"
  title: string
  detail: string
  device_id?: string
  point_id?: string
  root_cause?: string
  group_id?: string
  count?: number
  last_raised_at?: string
  active: boolean
  state: "active" | "acknowledged" | "resolved" | "closed"
  created_at: string
  updated_at: string
  acked_by?: string
  ack_note?: string
  maintenance_until?: string
  maintenance_reason?: string
  maintained_by?: string
}

export type AlarmEvent = { time: string; occurrence_id?: string; revision?: number; transition: string; severity: string; title: string; detail?: string; point_id?: string; state: string; acked_by?: string; ack_note?: string; maintained_by?: string; maintenance_reason?: string; input_state?: string; input_reason?: string; input?: AlarmInputEvidence }

export async function fetchAlarms(token: string, signal?: AbortSignal) {
  return request<{ alarms: EmsAlarm[] }>(token, "/ems/alarms", "GET", undefined, signal)
}

export async function ackAlarm(token: string, id: string, note = "") {
  return request<EmsAlarm>(token, `/ems/alarms/${encodeURIComponent(id)}/ack`, "POST", { note })
}

export async function closeAlarm(token: string, id: string) {
  return request<EmsAlarm>(token, `/ems/alarms/${encodeURIComponent(id)}/close`, "POST", {})
}

export async function maintainAlarm(token: string, id: string, until: string, reason: string) {
  return request<EmsAlarm>(token, `/ems/alarms/${encodeURIComponent(id)}/maintenance`, "POST", { until, reason })
}

export async function releaseAlarm(token: string, id: string) {
  return request<EmsAlarm>(token, `/ems/alarms/${encodeURIComponent(id)}/maintenance`, "DELETE")
}

export type HistoryResponse = { node_id: string; type: string; points: Point[] }
export type HistoryRecord = {
  point_id: string; device_id?: string; source_time?: string; received_time?: string; bucket_end?: string
  quality?: string; value?: number; text?: string; source: string; unit?: string; config_stamp?: string; sample_count?: number; aggregate?: string
  kind?: "observation" | "quality_event"; reason?: string; late?: boolean; time_source?: string
}
export type HistoryPointResult = {
  node_id: string; device_id?: string; source: string; records: HistoryRecord[]
  matched: number; returned: number; truncated: boolean; next_before?: string; next_before_count?: number; sample_interval_ms?: number
}
export type HistoryQueryResponse = { results: HistoryPointResult[]; start?: string; end?: string; aggregate?: string; total_matched: number }

export async function fetchHistory(token: string, ids: string | string[], start: Date, end: Date, aggregate = "raw", before?: Date, signal?: AbortSignal, beforeCount?: number) {
  const query = new URLSearchParams({ type: "value", start: start.toISOString(), end: end.toISOString(), limit: "2000", aggregate })
  for (const id of Array.isArray(ids) ? ids : [ids]) query.append("node_id", id)
  if (before) {
    query.set("before", before.toISOString())
    if (beforeCount !== undefined) query.set("before_count", String(beforeCount))
  }
  return request<HistoryQueryResponse & HistoryResponse>(token, `/history?${query.toString()}`, "GET", undefined, signal)
}

export async function downloadHistory(token: string, ids: string | string[], start: Date, end: Date, aggregate = "raw") {
  const query = new URLSearchParams({ type: "value", start: start.toISOString(), end: end.toISOString(), aggregate })
  for (const id of Array.isArray(ids) ? ids : [ids]) query.append("node_id", id)
  const response = await fetch(`/api/history/export?${query.toString()}`, { headers: { Authorization: `Bearer ${token}` } })
  if (!response.ok) {
    const result = await response.json().catch(() => ({ error: "" }))
    throw new ApiError(errorMessage(result.error || "history_export_failed", response.status), response.status, result.error || "history_export_failed")
  }
  const text = await response.text()
  const url = URL.createObjectURL(new Blob(["﻿" + text], { type: "text/csv;charset=utf-8" }))
  const link = document.createElement("a")
  link.href = url
  link.download = `fastiot-history-${start.toISOString().slice(0, 10)}-${Array.isArray(ids) ? ids.length : 1}points.csv`
  link.click()
  URL.revokeObjectURL(url)
  return { text, returned: Number(response.headers.get("X-History-Returned") || Math.max(0, text.trim().split(/\r?\n/).length - 1)), filename: link.download }
}

export type EmsStateValue = {
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

export type EmsHistorySample = {
  value: { kind: string; number?: number; bool?: boolean; enum?: string }
  quality: string
  source_time: string
}

export async function fetchEmsState(token: string, signal?: AbortSignal) {
  return request<{ values: EmsStateValue[]; evaluated_at: string }>(token, "/ems/state", "GET", undefined, signal)
}

export type ScreenConfig = {
  title: string
  columns: number
  point_ids: string[]
}

export async function fetchScreen(token: string, nodeId: string) {
  return request<ScreenConfig>(token, `/ems/screen/${nodeId}`)
}

export async function saveScreen(token: string, nodeId: string, config: ScreenConfig) {
  return request<{ saved: boolean }>(token, `/ems/screen/${nodeId}`, "PUT", config)
}

export type MemberConstraint = {
  kind: string
  target: Target
  operator: string
  limit: number
  direction?: "charge" | "discharge" | "both"
}

export type AllocatorMember = {
  id: string
  target: Target
  min_power?: number
  max_charge: number
  max_discharge: number
  available_charge?: Target
  available_discharge?: Target
  readback?: Target
  fault?: Target
  priority?: number
  weight?: number
  constraints?: MemberConstraint[]
}

export type AllocatorConfig = {
  grace_ms?: number
  timeout_ms?: number
  deadband_kw?: number
  members: AllocatorMember[]
}

export async function fetchAllocator(token: string, nodeId: string) {
  return request<AllocatorConfig>(token, `/ems/allocators/${nodeId}`)
}

export async function saveAllocator(token: string, nodeId: string, config: AllocatorConfig) {
  return request<{ saved: boolean }>(token, `/ems/allocators/${nodeId}`, "PUT", config)
}

// 能力级调度（领域模型重构）：规则条件引用 EnergyUnit 能力，动作是功率请求。
export type CapabilityCondition = {
  ref: { energy_unit_id: string; field: string }
  operator: string
  value: number
}

export type CapabilityAction = {
  energy_unit_id: string
  request_kw: number
}

export type CapabilityRule = {
  id: string
  state?: string
  published_at?: string
  conditions: CapabilityCondition[]
  actions: CapabilityAction[]
}

export type EnergyMember = {
  energy_unit_id: string
  min_power?: number
  priority?: number
  weight?: number
}

export async function fetchEnergyRule(token: string, energyUnitId: string) {
  return request<CapabilityRule>(token, `/energy-units/${energyUnitId}/rule`)
}

export type EnergyUnitSummary = { id: string; asset_code: string; description: string }

export async function fetchEnergyUnits(token: string) {
  return request<{ units: EnergyUnitSummary[] }>(token, `/energy-units`)
}

export async function createEnergyUnit(token: string, input: { asset_code?: string; description: string; rated_power_kw: number; energy_type?: string }) {
  return request<{ id: string; asset_code: string; plant_id: string }>(token, `/energy-units`, "POST", input)
}

export type EnergyDevice = { id: string; asset_code: string; description: string; rated_power_kw: number; power_setpoint_id: string }

export async function fetchEnergyDevices(token: string, energyUnitId: string) {
  return request<{ devices: EnergyDevice[] }>(token, `/energy-units/${energyUnitId}/devices`)
}

export async function createEnergyDevice(token: string, energyUnitId: string, input: { asset_code?: string; description: string; rated_power_kw: number; power_setpoint_id: string }) {
  return request<{ id: string; asset_code: string }>(token, `/energy-units/${energyUnitId}/devices`, "POST", input)
}

// 电站策略（防逆流/需量）：PCC 反馈绑定。
export type PlantSummary = { id: string; asset_code: string; description: string }
export type PCCBinding = { MeterPointID: string; Sign: number; MaxAgeMS: number; UpperLimitKW: number; LowerLimitKW: number; PhasePointIDs?: string[] }

export async function fetchPlants(token: string) {
  return request<{ plants: PlantSummary[] }>(token, `/plants`)
}

export async function fetchPlantPCC(token: string, plantId: string) {
  return request<{ binding: PCCBinding; configured: boolean }>(token, `/plants/${plantId}/pcc`)
}

export async function savePlantPCC(token: string, plantId: string, binding: PCCBinding) {
  return request<{ saved: boolean }>(token, `/plants/${plantId}/pcc`, "PUT", binding)
}

export type TariffPeriod = { StartMin: number; EndMin: number; Kind: string; Price: number }
export type PlantStrategy = { peak_valley_power_kw: number; periods: TariffPeriod[] }

export async function fetchPlantStrategy(token: string, plantId: string) {
  return request<{ strategy: PlantStrategy; configured: boolean }>(token, `/plants/${plantId}/strategy`)
}

export async function savePlantStrategy(token: string, plantId: string, strategy: PlantStrategy) {
  return request<{ saved: boolean }>(token, `/plants/${plantId}/strategy`, "PUT", strategy)
}

export async function saveEnergyRule(token: string, energyUnitId: string, rule: CapabilityRule) {
  return request<{ saved: boolean }>(token, `/energy-units/${energyUnitId}/rule`, "PUT", rule)
}

export async function publishEnergyRule(token: string, energyUnitId: string) {
  return request<{ saved: boolean }>(token, `/energy-units/${energyUnitId}/rule/publish`, "POST", {})
}

export async function simulateEnergyRule(token: string, energyUnitId: string, rule: CapabilityRule, values: Record<string, Record<string, number>>) {
  return request<{ actions: CapabilityAction[] }>(token, `/energy-units/${energyUnitId}/rule/simulate`, "POST", { rule, values })
}

export async function fetchEnergyAllocator(token: string, energyUnitId: string) {
  return request<{ members: EnergyMember[] }>(token, `/energy-units/${energyUnitId}/allocator`)
}

export async function saveEnergyAllocator(token: string, energyUnitId: string, members: EnergyMember[]) {
  return request<{ saved: boolean }>(token, `/energy-units/${energyUnitId}/allocator`, "PUT", { members })
}

export type CapabilityInputs = {
  soc_point_id: string
  temp_point_id: string
  charge_limit_point_id: string
  discharge_limit_point_id: string
  soc_min: number
  soc_max: number
  temp_derate: number
  temp_limit: number
  max_age_ms: number
  ramp_rate_kw: number
  deadband_kw: number
}

export async function fetchCapabilityInputs(token: string, energyUnitId: string) {
  return request<{ inputs: CapabilityInputs; configured: boolean }>(token, `/energy-units/${energyUnitId}/capability-inputs`)
}

export async function saveCapabilityInputs(token: string, energyUnitId: string, inputs: CapabilityInputs) {
  return request<{ saved: boolean }>(token, `/energy-units/${energyUnitId}/capability-inputs`, "PUT", inputs)
}

export type ResourceGroup = { id: string; description: string; shared_limit_kw: number; members: string[] }

export async function fetchResourceGroups(token: string) {
  return request<{ groups: ResourceGroup[] }>(token, `/resource-groups`)
}

export async function createResourceGroup(token: string, input: { description: string; shared_limit_kw: number; members: string[] }) {
  return request<{ id: string }>(token, `/resource-groups`, "POST", input)
}

export type GroupDispatchResult = {
  allocations: { energy_unit_id: string; setpoint_kw: number; excluded: boolean; reason?: string }[]
  delivered_kw: number
  shortfall_kw: number
  limited_by?: string
}

export async function dispatchResourceGroup(token: string, groupId: string, requestKW: number) {
  return request<GroupDispatchResult>(token, `/resource-groups/${groupId}/dispatch`, "POST", { request_kw: requestKW })
}

export type DispatchRecord = {
  dispatch_id: string
  energy_unit_id: string
  at: string
  cabinet_state: string
  rule_id: string
  source?: string
  priority?: number
  actor?: string
  stage?: string
  request_kw: number
  delivered_kw: number
  shortfall_kw: number
  capability: Record<string, Record<string, number>>
  dispatches: { device_id: string; setpoint_kw: number; excluded: boolean; reason?: string }[]
}

export async function fetchDispatchRecords(token: string, energyUnitId: string) {
  return request<{ records: DispatchRecord[] }>(token, `/energy-units/${energyUnitId}/dispatch-records`)
}

export type AllocShare = {
  member_id: string
  capacity: number
  setpoint: number
  actual?: number
  confirmed?: boolean
  excluded: boolean
  held: boolean
  reason: string
}

export type AllocationSnapshot = {
  at: string
  requested: number
  delivered: number
  shortfall: number
  status: string
  reason?: string
  shares: AllocShare[]
}

export async function simulateAllocator(token: string, nodeId: string, requestValue: number) {
  return request<{ requested: number; delivered: number; shortfall: number; shares: AllocShare[]; notes?: string[] }>(
    token, `/ems/allocators/${nodeId}/simulate`, "POST", { request: requestValue })
}

export async function fetchAllocatorHistory(token: string, nodeId: string) {
  return request<{ history: AllocationSnapshot[] }>(token, `/ems/allocators/${nodeId}/history`)
}

export type RowDiff = {
  row_id: string
  kind: "added" | "removed" | "changed"
  from?: string
  to?: string
  detail?: string[]
}

export type DependencyNode = { id: string; kind: "input" | "output" | "row"; label: string }
export type DependencyEdge = { from: string; to: string; kind: "condition" | "action" }
export type DependencyGraph = { nodes: DependencyNode[]; edges: DependencyEdge[] }

export async function diffRule(token: string, ruleId: string, from: string, to: string) {
  return request<{ diffs: RowDiff[] }>(token, `/rules/${ruleId}/diff`, "POST", { from, to })
}

export async function replayRule(token: string, ruleId: string, versionId: string, inputs: Record<string, number>, textInputs: Record<string, string>, at?: string) {
  return request<Evaluation>(token, `/rules/${ruleId}/replay`, "POST", {
    version_id: versionId, inputs, text_inputs: textInputs, at: at || undefined,
  })
}

export async function fetchRuleDeps(token: string, ruleId: string, document: Document) {
  return request<DependencyGraph>(token, `/rules/${ruleId}/deps`, "POST", { document })
}

export const permissionActions: { key: string; label: string }[] = [
  { key: "configure", label: "配置（通道/设备/点位/规则/画面）" },
  { key: "write_value", label: "写值（控制写入）" },
  { key: "virtual_input_apply", label: "应用虚拟输入定义" },
  { key: "calculation_apply", label: "应用只读计算定义" },
  { key: "virtual_input_write", label: "修改已开放的运行输入" },
  { key: "command", label: "命令执行" },
  { key: "rule_publish", label: "规则发布" },
  { key: "alarm_ack", label: "报警确认" },
  { key: "alarm_maintain", label: "报警维护" },
  { key: "delete", label: "删除" },
  { key: "export", label: "导出" },
  { key: "account_manage", label: "账号管理" },
  { key: "audit_view", label: "审计查看" },
  { key: "site_settings", label: "站点设置" },
]

export type UserAccount = {
  id: string
  email: string
  policy: { admin: boolean; role: string; root_node_ids?: string[]; actions?: string[] }
}

export async function listUsers(token: string) {
  return request<{ users: UserAccount[]; actions: string[] }>(token, "/users")
}

export async function updateUserPolicy(token: string, id: string, role: string, actions: string[]) {
  return request<{ saved: boolean }>(token, `/users/${encodeURIComponent(id)}`, "PUT", { role, actions })
}

export async function resetUserPassword(token: string, id: string, password: string) {
  return request<{ reset: boolean }>(token, `/users/${encodeURIComponent(id)}/password`, "POST", { password })
}

// 控制器人工操作（运行层）：启停/检修走命令回读，模式切换立即停止自动调度。
export async function sendEnergyUnitEvent(token: string, energyUnitId: string, event: string, requestId: string) {
  return request<{ state: string; request_id: string; command_state?: string }>(token, `/energy-units/${energyUnitId}/events`, "POST", { event, request_id: requestId })
}

export async function setEnergyUnitMode(token: string, energyUnitId: string, mode: "auto" | "manual") {
  return request<{ mode: string }>(token, `/energy-units/${energyUnitId}/mode`, "PUT", { mode })
}

// 站级计划：Site 总功率目标按 Plant→EnergyUnit 分层分配，进入统一仲裁。
export async function fetchSiteTarget(token: string, siteId: string) {
  return request<{ target_kw: number; configured: boolean }>(token, `/sites/${siteId}/target`)
}

export async function saveSiteTarget(token: string, siteId: string, targetKW: number) {
  return request<{ saved: boolean; target_kw: number }>(token, `/sites/${siteId}/target`, "PUT", { target_kw: targetKW })
}

// 光伏短期预测输入（历史估计，不冒充精确预测）。
export type ForecastInputs = { source_point_id: string; interval_sec: number; window_minutes: number }

export async function fetchForecastInputs(token: string, energyUnitId: string) {
  return request<{ inputs: ForecastInputs; configured: boolean }>(token, `/energy-units/${energyUnitId}/forecast-inputs`)
}

export async function saveForecastInputs(token: string, energyUnitId: string, inputs: ForecastInputs) {
  return request<{ saved: boolean }>(token, `/energy-units/${energyUnitId}/forecast-inputs`, "PUT", inputs)
}

// OPC-UA 接入（接入建模层）：端点通道 + 完整 NodeID 测点。
export type OpcuaChannel = { id: string; description: string; endpoint: string; poll_ms: number; online: boolean; last_error?: string; last_good?: string; points: number }
export type OpcuaPoint = { id: string; description: string; node_id: string; read_only: boolean; value?: number }

export async function fetchOpcuaChannels(token: string) {
  return request<{ channels: OpcuaChannel[] }>(token, "/opcua/channels")
}
export async function createOpcuaChannel(token: string, input: { description: string; endpoint: string; poll_ms: number }) {
  return request<{ id: string }>(token, "/opcua/channels", "POST", input)
}
export async function fetchOpcuaPoints(token: string, channelId: string) {
  return request<{ points: OpcuaPoint[] }>(token, `/opcua/channels/${channelId}/points`)
}
export async function createOpcuaPoint(token: string, channelId: string, input: { node_id: string; description: string; read_only: boolean }) {
  return request<{ id: string }>(token, `/opcua/channels/${channelId}/points`, "POST", input)
}

// 逐相判定：三相点位都新鲜时返回各相反送；执行器不支持独立调相时如实说明。
export type PhaseVerdict = { available: boolean; reason?: string; net_kw?: number; phase_reverse_kw?: number[]; can_per_phase?: boolean; note?: string }
export async function fetchPhaseVerdict(token: string, plantId: string) {
  return request<PhaseVerdict>(token, `/plants/${plantId}/phase-verdict`)
}
