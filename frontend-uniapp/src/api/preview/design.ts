import type { EmsAlarm, Node, Point, Snapshot, StateValue } from "../../types/api"
import type { Profile, ProfilePointSpec, UnitModelConfig, UnitPoint } from "../../components/design/access"

export type DesignPreviewResult = { handled: boolean; status?: number; data?: unknown }

type JsonObject = Record<string, any>
type TemplateNode = { name: string; type: string; children?: TemplateNode[] }
type SiteTemplate = { id: string; name: string; description: string; nodes: TemplateNode[] }
type VerificationRecord = { at: string; actor: string; level: string; passed: boolean; reasons: string[] }
type PreviewUnit = {
  channel_id: string
  server_id: number
  points: UnitPoint[]
  model?: UnitModelConfig
  records: VerificationRecord[]
}
type StateMachine = {
  id: string
  parent: string
  name: string
  revision: number
  version: number
  draft: JsonObject
  applied?: JsonObject
  current_state: string
  last_execution?: JsonObject
  simulation?: JsonObject
  execution?: JsonObject
}
type DesignState = {
  counter: number
  nodes: Node[]
  values: StateValue[]
  alarms: EmsAlarm[]
  units: Record<string, PreviewUnit>
  screens: Record<string, JsonObject>
  machines: StateMachine[]
  aggregates: Record<string, JsonObject>
  versions: JsonObject[]
  latestPreflight: JsonObject | null
  handovers: Record<string, JsonObject>
  emsInstalled: boolean
}

const NOW = "2026-11-23T13:34:00.000Z"
let ROOT = "site-pump"
const CHANNEL = "channel-pump"
const PUMP_PROFILE = "centrifugal-pump-v1.3"

const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T
const ok = (data: unknown, status = 200): DesignPreviewResult => ({ handled: true, status, data })
const fail = (status: number, error: string): DesignPreviewResult => ok({ error }, status)
const unhandled: DesignPreviewResult = { handled: false }
const asBody = (body: unknown): JsonObject => body && typeof body === "object" && !Array.isArray(body) ? body as JsonObject : {}

function point(type: string, text?: string, value?: number, key = ""): Point {
  return { type, key, time: NOW, ...(text !== undefined ? { text } : {}), ...(value !== undefined ? { value } : {}) }
}

function makeNode(id: string, type: string, parent: string, name: string, points: Point[] = []): Node {
  return { id, type, parent, points: [point("description", name), ...points] }
}

function nodeName(node: Node | undefined) {
  return node?.points.find(item => item.type === "description" && (item.tombstone || 0) % 2 === 0)?.text || node?.id || ""
}

function activePoint(node: Node | undefined, type: string) {
  return node?.points.find(item => item.type === type && (item.tombstone || 0) % 2 === 0)
}

function upsertPoint(node: Node, next: Point) {
  const index = node.points.findIndex(item => item.type === next.type && item.key === next.key && (item.tombstone || 0) % 2 === 0)
  if (index >= 0) node.points[index] = next
  else node.points.push(next)
}

function nextID(state: DesignState, prefix: string) {
  state.counter += 1
  return `${prefix}-${state.counter}`
}

function interactionTime(state: DesignState) {
  return new Date(Date.parse(NOW) + state.counter * 1000).toISOString()
}

const pointKeys: Record<string, string> = {
  "出口压力": "pressure", "目标压力": "target-pressure", "运行状态": "running", "电流": "current",
  "故障": "fault", "水箱液位": "level", "瞬时流量": "flow", "运行频率": "frequency",
  "总功率": "power", "今日电量": "energy", "泵启停": "command", "采集有效": "valid",
  "振动": "vibration", "轴承温度": "bearing-temperature",
}

const pumpSpecs = [
  { name: "出口压力", unit: "MPa", read_only: true, role: "telemetry", address: 40101, scale: 0.01 },
  { name: "目标压力", unit: "MPa", read_only: false, role: "setpoint", note: "L3_SAFE 固定测试", address: 40102, scale: 0.01 },
  { name: "运行状态", read_only: true, role: "status", address: 40103, scale: 1 },
  { name: "电流", unit: "A", read_only: true, role: "telemetry", address: 40104, scale: 0.1 },
  { name: "故障", read_only: true, role: "status", address: 40105, scale: 1 },
  { name: "水箱液位", unit: "%", read_only: true, role: "telemetry", address: 40106, scale: 0.1 },
  { name: "瞬时流量", unit: "m³/h", read_only: true, role: "telemetry", address: 40107, scale: 0.1 },
  { name: "运行频率", unit: "Hz", read_only: true, role: "telemetry", address: 40108, scale: 0.1 },
  { name: "总功率", unit: "kW", read_only: true, role: "telemetry", address: 40109, scale: 0.1 },
  { name: "今日电量", unit: "kWh", read_only: true, role: "energy", address: 40110, scale: 0.1 },
  { name: "泵启停", read_only: false, role: "setpoint", address: 40111, scale: 1 },
  { name: "采集有效", read_only: true, role: "status", address: 40112, scale: 1 },
]
const tankSpecs = [
  { name: "水箱液位", unit: "%", read_only: true, role: "telemetry", address: 40201, scale: 0.1 },
  { name: "采集有效", read_only: true, role: "status", address: 40202, scale: 1 },
]

function profile(id: string, model: string, specs: ProfilePointSpec[], verified: boolean): Profile {
  const commands = id.startsWith("centrifugal-pump") ? [
    { id: "start", name: "启动", point: "泵启停", value: 1, readback: "运行状态", readback_value: 1, timeout_ms: 5000 },
    { id: "stop", name: "停止", point: "泵启停", value: 0, readback: "运行状态", readback_value: 0, timeout_ms: 5000 },
    { id: "set-pressure", name: "目标压力安全写回读", point: "目标压力", value: 0.45, readback: "目标压力", readback_value: 0.45, timeout_ms: 5000 },
  ] : []
  return {
    id, vendor: "教学设备", model, protocol: "modbus", points: specs.length, commands: commands.length,
    verified, verification: verified ? "教学泵房真机核对记录：地址、缩放、读写方向与回读一致。" : "新版 Profile 待现场验证。",
    notes: ["点位、命令与读写方向随 Profile 版本固定。", "L3 只开放显式声明的固定安全值。"],
    point_specs: clone(specs), command_specs: commands,
  }
}

const profiles: Profile[] = [
  profile(PUMP_PROFILE, "离心泵 v1.3", pumpSpecs, true),
  profile("centrifugal-pump-v1.4", "离心泵 v1.4（测试）", [...pumpSpecs,
    { name: "振动", unit: "mm/s", read_only: true, role: "telemetry", address: 40113, scale: 0.01 },
    { name: "轴承温度", unit: "°C", read_only: true, role: "telemetry", address: 40114, scale: 0.1 },
  ], false),
  profile("water-tank-v1", "水箱液位 v1.0", tankSpecs, true),
]

const pcsSpecs = [
  { name: '有功功率', unit: 'kW', read_only: false, note: 'L3_SAFE', address: 30101, scale: 0.1 },
  { name: '直流电压', unit: 'V', read_only: true, address: 30102, scale: 0.1 },
  { name: '直流电流', unit: 'A', read_only: true, address: 30103, scale: 0.1 },
  { name: '温度', unit: '°C', read_only: true, address: 30104, scale: 0.1 },
  { name: '运行状态', read_only: true, address: 30105, scale: 1 },
  { name: '告警字', read_only: true, address: 30106, scale: 1 },
]
const bmsSpecs = [
  { name: 'SOC', unit: '%', read_only: true, address: 40101, scale: 0.1 },
  { name: 'SOH', unit: '%', read_only: true, address: 40102, scale: 0.1 },
  { name: '单体电压', unit: 'V', read_only: true, address: 40103, scale: 0.001 },
  { name: '温度', unit: '°C', read_only: true, address: 40104, scale: 0.1 },
  { name: '告警字', read_only: true, address: 40105, scale: 1 },
  { name: '绝缘电阻', unit: 'kΩ', read_only: true, address: 40106, scale: 1 },
]
profiles.push(profile('pcs21', '储能变流器 PCS-100 v2.1', pcsSpecs, true), profile('pcs20', '储能变流器 PCS-100 v2.0', pcsSpecs, true), profile('bms215', '电池管理 BMS-215 v2.1', bmsSpecs, true), profile('bms210', '电池管理 BMS-215 v2.0', bmsSpecs, true))

function profileSpecs(profileID: string): JsonObject[] {
  return (profiles.find(item => item.id === profileID)?.point_specs || []) as JsonObject[]
}

function physicalPoints(channelID: string, businessKey: string, serverID: number, specs: JsonObject[]): UnitPoint[] {
  return specs.map(spec => ({
    id: `io-${businessKey}-${pointKeys[spec.name] || spec.name}`,
    name: spec.name,
    server_id: serverID,
    address: spec.address,
    io_type: spec.read_only === false ? "modbusHoldingRegister" : "modbusInputRegister",
    data_format: spec.name === "出口压力" || spec.name === "目标压力" ? "float32" : "uint16",
    read_only: spec.read_only !== false,
    unit: spec.unit || "",
    scale: spec.scale ?? 1,
    offset: 0,
  }))
}

function physicalNode(channelID: string, item: UnitPoint): Node {
  return makeNode(item.id, "modbusIo", channelID, item.name, [
    point("businessId", item.id), point("modbusIoType", item.io_type), point("readOnly", undefined, item.read_only ? 1 : 0),
    point("units", item.unit || ""), point("scale", undefined, item.scale), point("address", undefined, item.address),
  ])
}

function pointMap(specs: JsonObject[], points: UnitPoint[]) {
  return Object.fromEntries(specs.map(spec => [spec.name, points.find(item => item.name === spec.name)?.id || ""]).filter(([, id]) => !!id))
}

function makeUnit(channelID: string, businessKey: string, serverID: number, profileID: string, bound: boolean): PreviewUnit {
  const specs = profileSpecs(profileID)
  const points = physicalPoints(channelID, businessKey, serverID, specs)
  return {
    channel_id: channelID, server_id: serverID, points,
    model: bound ? {
      name: businessKey === "tank" ? "水箱" : `${businessKey.replace("pump-", "")}号泵`,
      profile_id: profileID,
      business_node_id: businessKey,
      point_map: pointMap(specs, points),
      verified: serverID !== 2,
      verifier: serverID !== 2 ? "张工" : "",
      verified_at: serverID !== 2 ? "2026-11-22" : "",
      device_serial: serverID !== 2 ? `TEACH-${serverID.toString().padStart(3, "0")}` : "",
      verification: serverID !== 2 ? "已完成地址、缩放、读写方向与真机回读核对。" : "",
    } : undefined,
    records: bound ? [{ at: NOW, actor: "张工", level: "L2", passed: true, reasons: [] }] : [],
  }
}

function semanticNode(id: string, parent: string, name: string, businessID: string, value: number, unit = "", text?: string) {
  return makeNode(id, "variable", parent, name, [
    point("businessId", businessID), point("units", unit), point("value", text, value),
  ])
}

function initialRuleDocument(): JsonObject {
  const bindings = {
    points: {
      valid: { node_id: "valid-p1", point_type: "value" }, pressure: { node_id: "pressure-p1", point_type: "value" },
      level: { node_id: "level-tank", point_type: "value" },
    },
    actions: { pump_1: { node_id: "command-p1", point_type: "valueSet" }, target_pressure: { node_id: "target-pressure-p1", point_type: "valueSet" } },
  }
  return {
    table: { rows: [
      { row_id: "low-level", conditions: [{ point_id: "valid", operator: "eq", kind: "bool", value: 1 }, { point_id: "level", operator: "lt", value: 10 }], match: "all", actions: [{ action_id: "pump_1", value: 0, delay_ms: 0, readback: { timeout_ms: 5000, tolerance: 0 } }] },
      { row_id: "low-pressure", conditions: [{ point_id: "valid", operator: "eq", kind: "bool", value: 1 }, { point_id: "pressure", operator: "lt", value: 0.35 }], match: "all", hold_ms: 30000, actions: [{ action_id: "pump_1", value: 1, delay_ms: 0, readback: { timeout_ms: 5000, tolerance: 0 } }] },
      { row_id: "high-pressure", conditions: [{ point_id: "valid", operator: "eq", kind: "bool", value: 1 }, { point_id: "pressure", operator: "gt", value: 0.55 }], match: "all", hold_ms: 30000, actions: [{ action_id: "target_pressure", value: 0.35, delay_ms: 0, readback: { timeout_ms: 5000, tolerance: 0.01 } }] },
      { row_id: "day-notify", conditions: [{ point_id: "valid", operator: "eq", kind: "bool", value: 1 }, { point_id: "level", operator: "gt", value: 50 }, { point_id: "", operator: "", value: 0, kind: "time", start: "14:00", end: "16:00", weekday_mask: 127 }], match: "all", actions: [] },
    ] }, bindings,
  }
}

function initialMachine(): StateMachine {
  const definition = {
    initial_state: "停止", scope: "subtree", states: ["停止", "启动中", "运行", "停止中", "故障"],
    transitions: [
      { transition_id: "start", from: "停止", event: "启动", to: "启动中", match: "all", conditions: [{ point_id: "permit", operator: "eq", kind: "bool", value: 1 }, { point_id: "fault", operator: "eq", kind: "bool", value: 0 }], actions: [{ action_id: "pump_1", value: 1, readback: { timeout_ms: 5000, tolerance: 0 } }] },
      { transition_id: "running", from: "启动中", event: "回读运行", to: "运行", match: "all", conditions: [{ point_id: "current", operator: "gt", value: 0 }], actions: [] },
      { transition_id: "stop", from: "运行", event: "停止", to: "停止中", match: "all", conditions: [], actions: [{ action_id: "pump_1", value: 0, readback: { timeout_ms: 5000, tolerance: 0 } }] },
      { transition_id: "stopped", from: "停止中", event: "回读停止", to: "停止", match: "all", conditions: [], actions: [] },
      { transition_id: "fault", from: "*", event: "故障", to: "故障", match: "all", conditions: [], actions: [{ action_id: "pump_1", value: 0, readback: { timeout_ms: 5000, tolerance: 0 } }] },
    ],
    bindings: {
      points: { permit: { node_id: "permit-p1", point_type: "value" }, fault: { node_id: "fault-p1", point_type: "value" }, current: { node_id: "current-p1", point_type: "value" } },
      actions: { pump_1: { node_id: "command-p1", point_type: "valueSet" } },
    },
  }
  return { id: "machine-pump", parent: ROOT, name: "泵组过程状态机", revision: 1, version: 1, draft: clone(definition), applied: clone(definition), current_state: "停止" }
}

const siteTemplates: SiteTemplate[] = [
  { id: "blank", name: "空白项目", description: "从零开始搭", nodes: [] },
  { id: "pump-house", name: "供水泵房", description: "水箱、两台离心泵、出水压力", nodes: [
    { name: "泵房", type: "area", children: [{ name: "1号泵", type: "device" }, { name: "2号泵", type: "device" }, { name: "水箱", type: "device" }] },
    { name: "供水单元", type: "unit", children: [] },
  ] },
  { id: "storage", name: "储能站", description: "光伏区、储能区、并网点", nodes: [
    { name: "光伏区", type: "area", children: [{ name: "光伏1", type: "device" }, { name: "光伏2", type: "device" }] },
    { name: "储能区", type: "area", children: [
      { name: "储能1", type: "energyUnit", children: [{ name: "PCS1", type: "device" }, { name: "BMS1", type: "device" }] },
      { name: "储能2", type: "energyUnit", children: [{ name: "PCS2", type: "device" }, { name: "BMS2", type: "device" }] },
    ] },
  ] },
]

function initialScreen(): JsonObject {
  return {
    layer: ROOT, version: 1, title: "教学泵房", columns: 2,
    point_ids: ["pressure-p1", "power-p1", "level-tank"],
    slots: { top: ["status", "kpi"], main: ["flow", "devices", "recent"], side: ["alarm", "today", "trend", "summary"] },
    blocks: [
      { type: "status", binding: {}, span: 2 },
      { type: "kpi", binding: { metrics: [
        { point_id: "pressure-p1", label: "出口压力", target: "0.45" }, { point_id: "power-p1", label: "总功率", target: "" },
        { point_id: "level-tank", label: "水箱液位", target: "≥ 20" }, { metric: "today_energy", label: "今日电量", target: "" },
        { metric: "active_alarms", label: "活动告警", target: "" }, { metric: "communication_success_rate", label: "通信成功率", target: "" },
      ] }, span: 2 },
      { type: "flow", binding: { detail: true }, span: 2 },
      { type: "devices", binding: { columns: ["type", "state", "value", "communication", "updated"] }, span: 2 },
      { type: "recent", binding: { point_ids: ["pressure-p1", "target-pressure-p1", "current-p1", "level-tank"], columns: ["value", "quality", "time"], rows: 5 }, span: 2 },
      { type: "alarm", binding: {}, span: 2 }, { type: "today", binding: {}, span: 2 },
      { type: "trend", binding: { point_id: "pressure-p1" }, span: 2 }, { type: "summary", binding: {}, span: 2 },
    ],
  }
}

function createState(unboundPump = false): DesignState {
  const nodes: Node[] = [
    makeNode(ROOT, "site", "root", "教学泵房"),
    makeNode("area-pump", "area", ROOT, "泵房"),
    makeNode("pump-1", "device", "area-pump", "1号泵"),
    makeNode("pump-2", "device", "area-pump", "2号泵"),
    makeNode("tank", "device", "area-pump", "水箱"),
    makeNode("unit-supply", "unit", ROOT, "供水单元", [point("easyemsUnitMembers", JSON.stringify([
      { role: "驱动泵", node_id: "pump-1", required: true }, { role: "出口压力", node_id: "pressure-p1", required: true }, { role: "液位反馈", node_id: "level-tank", required: true },
    ]))]),
    semanticNode("pressure-p1", "pump-1", "出口压力", "pressure", 0.42, "MPa"),
    semanticNode("target-pressure-p1", "pump-1", "目标压力", "target_pressure", 0.45, "MPa"),
    semanticNode("current-p1", "pump-1", "电流", "current", 12.4, "A"),
    semanticNode("power-p1", "pump-1", "总功率", "power", 12.4, "kW"),
    semanticNode("running-p1", "pump-1", "运行状态", "running", 1, "", "运行"),
    semanticNode("fault-p1", "pump-1", "故障", "fault", 0, "", "无"),
    semanticNode("permit-p1", "pump-1", "运行许可", "permit", 1, "", "允许"),
    semanticNode("valid-p1", "pump-1", "采集有效", "valid", 1, "", "有效"),
    semanticNode("command-p1", "pump-1", "泵启停", "pump_1", 1, "", "运行"),
    semanticNode("pressure-p2", "pump-2", "出口压力", "pressure_p2", 0.38, "MPa"),
    semanticNode("current-p2", "pump-2", "电流", "current_p2", 0, "A"),
    semanticNode("power-p2", "pump-2", "总功率", "power_p2", 0, "kW"),
    semanticNode("running-p2", "pump-2", "运行状态", "running_p2", 0, "", "停止"),
    semanticNode("level-tank", "tank", "水箱液位", "level", 68, "%"),
    makeNode("rule-pump", "decisionTable", ROOT, "供水安全与压力策略", [
      point("draft", JSON.stringify(initialRuleDocument())), point("applied", JSON.stringify(initialRuleDocument())), point("enabled", undefined, 1), point("lastMatch", "day-notify"),
    ]),
    makeNode(CHANNEL, "modbus", ROOT, "Modbus TCP · 教学泵房", [
      point("protocol", "TCP"), point("uri", "192.168.1.10:502"), point("pollPeriod", undefined, 1000), point("timeout", undefined, 1000),
    ]),
  ]
  const units: Record<string, PreviewUnit> = {
    [`${CHANNEL}/1`]: makeUnit(CHANNEL, "pump-1", 1, PUMP_PROFILE, true),
    [`${CHANNEL}/2`]: makeUnit(CHANNEL, "pump-2", 2, PUMP_PROFILE, !unboundPump),
    [`${CHANNEL}/3`]: makeUnit(CHANNEL, "tank", 3, "water-tank-v1", true),
  }
  if (!unboundPump && units[`${CHANNEL}/2`].model) units[`${CHANNEL}/2`].model!.verified = true
  for (const unit of Object.values(units)) {
    for (const item of unit.points) nodes.push(physicalNode(CHANNEL, item))
  }
  const state: DesignState = {
    counter: 0, nodes, values: [], alarms: [
      { id: "alarm-pressure", key: "pressure-low", revision: 1, severity: "warning", title: "2号泵 出口压力低", detail: "出口压力低于 0.35 MPa，检查泵组与管网。", device_id: "pump-2", point_id: "pressure-p2", active: true, state: "active", created_at: NOW, updated_at: NOW, permissions: { ack: true, maintain: true } },
      { id: "alarm-level", key: "level-low", revision: 1, severity: "info", title: "水箱 液位偏低", detail: "夜间注意液位变化，低于 20% 时需要补水。", device_id: "tank", point_id: "level-tank", active: true, state: "active", created_at: NOW, updated_at: NOW, permissions: { ack: true, maintain: true } },
    ], units,
    screens: { [ROOT]: initialScreen() }, machines: [initialMachine()],
    aggregates: { [ROOT]: { group: ROOT, op: "sum", members: ["power-p1", "power-p2"], units: "kW", window: "", quality: "degrade" } },
    versions: [{ id: "v2026.11.19-1", site_id: ROOT, site_name: "教学泵房", created_at: "2026-11-19T08:00:00.000Z", operator: "张工", note: "教学泵房初始工程", preflight_status: "warn", nodes: nodes.length }],
    latestPreflight: null,
    handovers: { [ROOT]: { layer_id: ROOT, note: "上一班 张工：2号泵检修后已恢复；夜间注意液位。", actor: "张工", updated_at: NOW } },
    emsInstalled: true,
  }
  const semanticValues: Record<string, number> = {
    "pressure-p1": 0.42, "target-pressure-p1": 0.45, "current-p1": 12.4, "power-p1": 12.4,
    "running-p1": 1, "fault-p1": 0, "permit-p1": 1, "valid-p1": 1, "command-p1": 1,
    "pressure-p2": 0.38, "current-p2": 0, "power-p2": 0, "running-p2": 0, "level-tank": 68,
  }
  for (const [id, value] of Object.entries(semanticValues)) {
    const node = nodes.find(item => item.id === id)
    const offline = unboundPump && id.endsWith("-p2")
    state.values.push({ device_id: node?.parent || "", point_id: id, value: { kind: "number", number: value }, quality: offline ? "offline" : "good", source_time: NOW, received_time: NOW, ...(offline ? { reason: "设备尚未绑定" } : {}) })
  }
  for (const unit of Object.values(units)) {
    for (const item of unit.points) {
      const value = item.name === "出口压力" ? (unit.server_id === 2 ? 0.38 : 0.42)
        : item.name === "目标压力" ? 0.45 : item.name === "电流" ? (unit.server_id === 2 ? 0 : 12.4)
          : item.name === "总功率" ? (unit.server_id === 2 ? 0 : 12.4) : item.name === "水箱液位" ? 68
            : item.name === "运行状态" || item.name === "泵启停" || item.name === "采集有效" ? (unit.server_id === 2 ? 0 : 1)
              : item.name === "瞬时流量" ? 36 : item.name === "运行频率" ? 50 : item.name === "今日电量" ? 124 : 0
      state.values.push({ device_id: `${unit.channel_id}/${unit.server_id}`, point_id: item.id, value: { kind: "number", number: value }, quality: unit.model ? "good" : "offline", source_time: NOW, received_time: NOW, ...(unit.model ? {} : { reason: "设备尚未绑定" }) })
    }
  }
  syncAllBindings(state)
  return state
}

let state = createState()
const projectStates = new Map<string, DesignState>([[ROOT, state]])

export function selectDesignPreviewProject(id: string, name: string) {
  if (id === ROOT) return
  projectStates.set(ROOT, state)
  ROOT = id
  const existing = projectStates.get(id)
  if (existing) { state = existing; return }
  const next: DesignState = { counter: 0, nodes: [makeNode(id, 'site', 'root', name)], values: [], alarms: [], units: {}, screens: {}, machines: [], aggregates: {}, versions: [], latestPreflight: null, handovers: {}, emsInstalled: state.emsInstalled }
  if (id === 'site-energy' || id === 'site-farm') {
    next.nodes.push(makeNode(CHANNEL, 'modbus', id, 'Modbus TCP · 192.168.1.10:502', [point('protocol', 'TCP'), point('uri', '192.168.1.10:502')]))
    const addDevice = (deviceId: string, parent: string, label: string, profileID: string, station: number, bound: boolean) => {
      next.nodes.push(makeNode(deviceId, 'device', parent, label))
      const unit = makeUnit(CHANNEL, deviceId, station, profileID, bound)
      if (unit.model) unit.model.name = label
      next.units[`${CHANNEL}/${station}`] = unit
      unit.points.forEach(item => {
        next.nodes.push(physicalNode(CHANNEL, item))
        const value = item.name === 'SOC' ? 62 : item.name === 'SOH' ? 98 : item.name === '有功功率' ? 30 : item.name === '直流电压' ? 750 : item.name === '温度' ? 28 : 0
        next.values.push({ point_id: item.id, device_id: `${CHANNEL}/${station}`, value: { kind: 'number', number: value }, quality: bound ? 'good' : 'offline', source_time: NOW, received_time: NOW })
      })
    }
    const area = id === 'site-farm' ? 'area-farm' : 'area-ess'
    next.nodes.push(makeNode(area, 'area', id, '储能区'))
    if (id === 'site-energy') {
      next.nodes.push(makeNode('area-pv', 'area', id, '光伏区'))
      addDevice('pv1', 'area-pv', '光伏1', 'pcs21', 21, true)
      addDevice('pv2', 'area-pv', '光伏2', 'pcs21', 22, false)
    }
    for (let n = 1; n <= (id === 'site-farm' ? 12 : 2); n += 1) {
      const unitId = `ess${n}`
      const bound = id === 'site-farm' ? n <= 6 : n === 1
      next.nodes.push(makeNode(unitId, 'energyUnit', area, `储能${n}`, [point('easyemsUnitMembers', JSON.stringify([{ role: 'PCS', node_id: `pcs${n}`, required: true }, { role: 'BMS', node_id: `bms${n}`, required: true }]))]))
      addDevice(`pcs${n}`, unitId, `PCS${n}`, 'pcs21', id === 'site-farm' ? 10 + n : 9 + n * 2, bound)
      addDevice(`bms${n}`, unitId, `BMS${n}`, 'bms215', id === 'site-farm' ? 40 + n : 10 + n * 2, bound)
    }
    syncAllBindings(next)
  }
  state = next
  projectStates.set(id, next)
}

export function resetDesignPreview(unboundPump = false) {
  ROOT = 'site-pump'
  state = createState(unboundPump)
  projectStates.clear()
  projectStates.set(ROOT, state)
}

function snapshot(): Snapshot {
  return clone({ user: { user_id: "preview-admin", email: "preview@example.invalid", policy: { admin: true, role: "admin", actions: ["configure", "export", "command", "rule_publish"] }, actions: ["configure", "export", "command", "rule_publish", "alarm_ack", "alarm_maintain"] }, roots: [ROOT], nodes: state.nodes })
}

function syncAllBindings(current: DesignState) {
  for (const unit of Object.values(current.units)) syncBinding(current, unit)
}

function syncBinding(current: DesignState, unit: PreviewUnit) {
  const channel = current.nodes.find(node => node.id === unit.channel_id)
  if (!channel) return
  channel.points = channel.points.filter(item => !(item.type === "easyemsUnitModel" && item.key === String(unit.server_id)))
  if (unit.model?.profile_id && unit.model.business_node_id) channel.points.push(point("easyemsUnitModel", JSON.stringify(unit.model), undefined, String(unit.server_id)))
}

function unitFor(channelID: string, serverID: number) {
  return state.units[`${channelID}/${serverID}`]
}

function ensureUnit(channelID: string, serverID: number): PreviewUnit {
  const key = `${channelID}/${serverID}`
  if (!state.units[key]) state.units[key] = { channel_id: channelID, server_id: serverID, points: [], records: [] }
  return state.units[key]
}

function modelView(unit: PreviewUnit): JsonObject {
  const specs = profileSpecs(unit.model?.profile_id || PUMP_PROFILE)
  const required = new Set((profiles.find(item => item.id === unit.model?.profile_id)?.command_specs || []).map(command => command.point).filter(Boolean))
  const missing = [...required].filter(name => !unit.model?.point_map?.[name as string])
  return {
    channel_id: unit.channel_id, server_id: unit.server_id, ...(unit.model ? { model: clone(unit.model) } : {}),
    profiles: clone(profiles), points: clone(unit.points), missing,
    commands: (profiles.find(item => item.id === unit.model?.profile_id)?.command_specs || []).map(command => ({ id: command.id, name: command.name, available: !missing.includes(command.point), ...(missing.includes(command.point) ? { reason: "命令必需点未映射" } : {}) })),
    profile_errors: specs.length ? [] : ["当前 Profile 没有点表"],
  }
}

function deviceLedger(): JsonObject[] {
  return Object.values(state.units).map(unit => {
    const model = unit.model
    const profile = profiles.find(item => item.id === model?.profile_id)
    const business = state.nodes.find(node => node.id === model?.business_node_id)
    return {
      channel_id: unit.channel_id, channel_name: nodeName(state.nodes.find(node => node.id === unit.channel_id)), server_id: unit.server_id,
      name: model?.name || nodeName(business) || `站号 ${unit.server_id}`,
      ...(model?.business_node_id ? { business_node_id: model.business_node_id, business_node_name: nodeName(business) } : {}),
      profile_id: model?.profile_id || "", vendor: profile?.vendor || "", model: profile?.model || "未选择 Profile",
      protocol: activePoint(state.nodes.find(node => node.id === unit.channel_id), "protocol")?.text || "TCP",
      address: activePoint(state.nodes.find(node => node.id === unit.channel_id), "uri")?.text || "",
      point_count: unit.points.length, command_count: profile?.command_specs?.length || 0,
      health: model?.profile_id ? "healthy" : "offline", success_rate: model?.profile_id ? 99.2 : 0, avg_latency_ms: model?.profile_id ? 2 : 0,
      verified: !!model?.verified, verifier: model?.verifier || "", verified_at: model?.verified_at || "", device_serial: model?.device_serial || "",
    }
  })
}

function quality(): JsonObject {
  const channels = [...new Set(Object.values(state.units).map(unit => unit.channel_id))].map(id => ({ channel_id: id, node_id: id, health: "healthy", success_rate: 99.2, avg_latency_ms: 2 }))
  const devices = deviceLedger().map(device => ({ device_id: device.business_node_id || `${device.channel_id}/${device.server_id}`, channel_id: device.channel_id, server_id: device.server_id, health: device.health, success_rate: device.success_rate, avg_latency_ms: device.avg_latency_ms, window_5m: { polls: device.profile_id ? 300 : 0, failures: device.profile_id ? 2 : 0, success_rate: device.success_rate } }))
  return { channels, devices }
}

function descendants(id: string): Node[] {
  const result: Node[] = []
  const walk = (parent: string) => {
    for (const child of state.nodes.filter(node => node.parent === parent)) {
      result.push(child)
      walk(child.id)
    }
  }
  walk(id)
  return result
}

function addTemplateNodes(parent: string, template: TemplateNode[], created: Node[]) {
  for (const item of template) {
    const node = makeNode(nextID(state, item.type), item.type, parent, item.name)
    state.nodes.push(node)
    created.push(node)
    addTemplateNodes(node.id, item.children || [], created)
  }
}

function createNode(body: JsonObject): DesignPreviewResult {
  const parent = String(body.parent || "")
  if (!state.nodes.some(node => node.id === parent)) return fail(404, "parent_not_found")
  const description = String(body.description || "").trim()
  if (!description) return fail(400, "description_required")
  const template = siteTemplates.find(item => item.id === body.site_template)
  if (body.site_template && !template) return fail(400, "template_not_found")
  const root = makeNode(nextID(state, template ? "site" : String(body.type || "device")), template ? "site" : String(body.type || "device"), parent, description)
  state.nodes.push(root)
  const created = [root]
  if (template) addTemplateNodes(root.id, template.nodes, created)
  if (template && template.id !== "blank") {
    const rule = makeNode(nextID(state, "rule"), "decisionTable", root.id, "供水安全与压力策略", [point("draft", JSON.stringify(initialRuleDocument()))])
    state.nodes.push(rule)
    created.push(rule)
  }
  return ok({ id: root.id, created: created.length })
}

function copySubtree(id: string, body: JsonObject): DesignPreviewResult {
  const source = state.nodes.find(node => node.id === id)
  if (!source) return fail(404, "not_found")
  const parent = String(body.parent || source.parent)
  if (!state.nodes.some(node => node.id === parent)) return fail(404, "parent_not_found")
  const excluded = new Set(["modbus", "decisionTable", "stateMachine", "user", "alarm"])
  const all = [source, ...descendants(id)]
  const skipped = all.filter(node => excluded.has(node.type)).length
  const included = all.filter(node => !excluded.has(node.type))
  const ids = new Map<string, string>()
  for (const node of included) ids.set(node.id, nextID(state, node.type))
  const copies = included.map(node => {
    const copy = clone(node)
    copy.id = ids.get(node.id)!
    copy.parent = node.id === source.id ? parent : ids.get(node.parent) || node.parent
    if (node.id === source.id) upsertPoint(copy, point("description", String(body.description || `${nodeName(source)} 副本`)))
    const members = activePoint(copy, "easyemsUnitMembers")
    if (members?.text) {
      const roles = JSON.parse(members.text) as JsonObject[]
      members.text = JSON.stringify(roles.map(role => ids.has(role.node_id) ? { ...role, node_id: ids.get(role.node_id) } : role))
    }
    return copy
  })
  state.nodes.push(...copies)
  const internal = new Set(ids.keys())
  const confirm = [...new Set(included.flatMap(node => {
    const text = activePoint(node, "easyemsUnitMembers")?.text
    if (!text) return []
    return (JSON.parse(text) as JsonObject[]).filter(role => role.node_id && !internal.has(role.node_id)).map(role => String(role.node_id))
  }))]
  return ok({ id: copies[0].id, created: copies.length, skipped, confirm })
}

function compare(value: number, operator: string, expected: number) {
  if (operator === "gt") return value > expected
  if (operator === "ge") return value >= expected
  if (operator === "eq") return value === expected
  if (operator === "ne") return value !== expected
  if (operator === "le") return value <= expected
  if (operator === "lt") return value < expected
  return false
}

function timeMatches(condition: JsonObject, at?: string) {
  if (!at) return true
  const date = new Date(at)
  if (Number.isNaN(date.getTime())) return false
  const mask = condition.weekday_mask || 0
  if (mask && !(mask & (1 << date.getDay()))) return false
  if (!condition.start || !condition.end) return true
  const minutes = date.getHours() * 60 + date.getMinutes()
  const [sh, sm] = String(condition.start).split(":").map(Number)
  const [eh, em] = String(condition.end).split(":").map(Number)
  const start = sh * 60 + sm, end = eh * 60 + em
  return start <= end ? minutes >= start && minutes <= end : minutes >= start || minutes <= end
}

function conditionMatches(condition: JsonObject, inputs: JsonObject, textInputs: JsonObject, at?: string) {
  if (condition.kind === "time") return timeMatches(condition, at)
  if (condition.kind === "enum") {
    const actual = String(textInputs[condition.point_id] ?? "")
    return condition.operator === "ne" ? actual !== String(condition.text || "") : actual === String(condition.text || "")
  }
  if (!Object.prototype.hasOwnProperty.call(inputs, condition.point_id)) return false
  return compare(Number(inputs[condition.point_id]), condition.operator, Number(condition.value || 0))
}

function evaluateRule(body: JsonObject): JsonObject {
  const document = asBody(body.document)
  const inputs = asBody(body.inputs), textInputs = asBody(body.text_inputs)
  const held = Number(document.simulation?.held_ms || 0)
  const traces: JsonObject[] = []
  const timeChecks: JsonObject[] = []
  for (const row of document.table?.rows || []) {
    const conditions = row.conditions || []
    const ordinary = conditions.filter((condition: JsonObject) => condition.kind !== "time")
    const ordinaryResults = ordinary.map((condition: JsonObject) => conditionMatches(condition, inputs, textInputs, body.at))
    const ordinaryPassed = row.match === "any" ? (ordinaryResults.length === 0 || ordinaryResults.some(Boolean)) : ordinaryResults.every(Boolean)
    const times = conditions.filter((condition: JsonObject) => condition.kind === "time")
    const checks: JsonObject[] = times.map((condition: JsonObject) => ({ start: condition.start || "", end: condition.end || "", weekday_mask: condition.weekday_mask || 0, satisfied: timeMatches(condition, body.at) }))
    timeChecks.push(...checks)
    const passed = ordinaryPassed && checks.every(check => check.satisfied)
    const reasons: string[] = []
    if (!ordinaryPassed) reasons.push("点位条件未满足")
    if (checks.some(check => !check.satisfied)) reasons.push("不在时间窗")
    if (passed && held < Number(row.hold_ms || 0)) reasons.push(`已持续 ${held / 1000}s，未达到 ${Number(row.hold_ms || 0) / 1000}s`)
    traces.push({ row_id: row.row_id, state: !passed ? "not-matched" : held < Number(row.hold_ms || 0) ? "waiting-hold" : "fired", reasons, held_ms: held, required_ms: Number(row.hold_ms || 0) })
    if (!passed) continue
    if (held < Number(row.hold_ms || 0)) return { row_id: row.row_id, state: "waiting-hold", actions: [], rows: traces, time_checks: timeChecks, blocked: [] }
    return { row_id: row.row_id, state: "fired", actions: clone(row.actions || []), rows: traces, time_checks: timeChecks, blocked: [] }
  }
  return { state: "no-match", actions: [], rows: traces, time_checks: timeChecks, blocked: [] }
}

function ruleByID(id: string) {
  return state.nodes.find(node => node.id === id && node.type === "decisionTable")
}

function machineResponse(machine: StateMachine): JsonObject {
  return { ...clone(machine), permissions: { edit: true, publish: true, execute: true } }
}

function simulateMachine(machine: StateMachine, definition: JsonObject, event: string, inputs: JsonObject, textInputs: JsonObject, at?: string): JsonObject {
  const transition = (definition.transitions || []).find((item: JsonObject) => (item.from === machine.current_state || item.from === "*") && item.event === event)
  if (!transition) return { status: "held", from: machine.current_state, to: machine.current_state, reason: "没有匹配的事件转移" }
  const conditions = transition.conditions || []
  const results = conditions.map((condition: JsonObject) => conditionMatches(condition, inputs, textInputs, at))
  const matched = transition.match === "any" ? (results.length === 0 || results.some(Boolean)) : results.every(Boolean)
  return matched
    ? { status: "matched", transition_id: transition.transition_id, from: machine.current_state, to: transition.to, reason: "守卫条件满足；模拟未写设备", actions: clone(transition.actions || []) }
    : { status: "held", transition_id: transition.transition_id, from: machine.current_state, to: machine.current_state, reason: "守卫条件不满足", actions: [] }
}

function liveMachineInputs(): JsonObject {
  const values: JsonObject = {}
  for (const sample of state.values) {
    const node = state.nodes.find(item => item.id === sample.point_id)
    const id = activePoint(node, "businessId")?.text
    if (id && sample.value.number !== undefined && sample.quality === "good") values[id] = sample.value.number
  }
  return values
}

function aggregateResult(config: JsonObject): JsonObject {
  const members = (config.members || []).map((id: string) => {
    const sample = state.values.find(item => item.point_id === id)
    return { node_id: id, name: nodeName(state.nodes.find(node => node.id === id)), value: sample?.value.number ?? null, quality: sample?.quality || "uninitialized", time: sample?.source_time || NOW }
  })
  const validMembers = members.filter((member: JsonObject) => member.quality === "good" && typeof member.value === "number")
  const numbers: number[] = validMembers.map((member: JsonObject) => member.value as number)
  let value: number | null = null
  if (config.op === "count") value = members.filter((member: JsonObject) => member.quality === "good" || config.quality === "count").length
  else if (numbers.length) {
    if (config.op === "avg") value = numbers.reduce((sum, item) => sum + item, 0) / numbers.length
    else if (config.op === "min") value = Math.min(...numbers)
    else if (config.op === "max") value = Math.max(...numbers)
    else value = numbers.reduce((sum, item) => sum + item, 0)
  }
  if (value !== null) value = Math.round(value * 10000) / 10000
  const partial = validMembers.length !== members.length
  return {
    configured: true, config: clone(config), value,
    quality: value === null ? "invalid" : partial && config.quality === "degrade" ? "degraded" : "good",
    partial, valid: validMembers.length, total: members.length, time: NOW, members,
    ...(config.window ? { window: { start: "2026-11-23T13:29:00.000Z", end: NOW, members: validMembers.length } } : {}),
  }
}

function screenKind(id: string) {
  const type = state.nodes.find(node => node.id === id)?.type || ""
  if (["unit", "energyUnit", "energy_unit"].includes(type)) return "unit"
  if (["device", "modbus_device"].includes(type)) return "device"
  if (["area", "group", "plant"].includes(type)) return "area"
  return "site"
}

function screenFor(id: string): JsonObject | null {
  if (state.screens[id]) return clone(state.screens[id])
  let current = state.nodes.find(node => node.id === id)
  const seen = new Set<string>()
  while (current && !seen.has(current.id)) {
    seen.add(current.id)
    if (state.screens[current.id]) return { ...clone(state.screens[current.id]), layer: id, inherited_from: current.id }
    current = state.nodes.find(node => node.id === current?.parent)
  }
  return null
}

function saveScreen(id: string, body: JsonObject): DesignPreviewResult {
  if (!state.nodes.some(node => node.id === id)) return fail(404, "not_found")
  const currentVersion = Number(state.screens[id]?.version || 0)
  if (Number(body.version || 0) !== currentVersion) return fail(409, "version_conflict")
  const targets: string[] = body.apply_to_same
    ? [ROOT, ...descendants(ROOT).map(node => node.id)].filter(target => target === id || screenKind(target) === screenKind(id) && screenKind(target) !== "site")
    : [id]
  for (const target of [...new Set(targets)]) {
    const saved = clone(body)
    delete saved.apply_to_same
    delete saved.inherited_from
    saved.layer = target
    saved.version = Number(state.screens[target]?.version || 0) + 1
    state.screens[target] = saved
  }
  return ok({ saved: true, version: state.screens[id].version, applied: new Set(targets).size })
}

function siteNodes(site: string) {
  const root = state.nodes.find(node => node.id === site && node.type === "site")
  return root ? [root, ...descendants(site)] : []
}

function projectPackage(site: string): DesignPreviewResult {
  const nodes = siteNodes(site)
  if (!nodes.length) return fail(404, "site_not_found")
  const ids = new Set(nodes.map(node => node.id))
  const packageNodes = nodes.map(node => ({
    ref: node.id, type: node.type, parent: ids.has(node.parent) ? node.parent : "",
    points: node.points.filter(item => !["uri", "port", "value", "enabled", "lastMatch", "error"].includes(item.type)),
  }))
  const usedProfiles = [...new Set(Object.values(state.units).filter(unit => ids.has(unit.channel_id) && unit.model?.profile_id).map(unit => unit.model!.profile_id))]
  return ok({
    format: "fastiot.project-package", format_version: 2, id: `package-${site}`, name: nodeName(nodes[0]), version: state.versions.filter(item => item.site_id === site).length + 1,
    source_version: state.versions.find(item => item.site_id === site)?.id || "", created_at: NOW, operator: "preview-admin",
    dependencies: [{ kind: "industry", id: "ems", version: "1.0.0" }, ...usedProfiles.map(id => ({ kind: "profile", id, version: id.endsWith("v1.4") ? "1.4.0" : id === "water-tank-v1" ? "1.0.0" : "1.3.0" }))],
    nodes: packageNodes, point_count: nodes.filter(node => ["variable", "modbusIo"].includes(node.type)).length,
    strategy_count: nodes.filter(node => node.type === "decisionTable").length,
    screens: Object.values(state.screens).filter(screen => ids.has(screen.layer)).map(screen => ({ node: screen.layer, source: "structured-screen", bindings: Object.fromEntries((screen.point_ids || []).map((id: string) => [id, `point:${id}`])), artifact: "json", build: { version: screen.version } })),
    stripped: [{ node: `${CHANNEL}`, point: "uri", reason: "连接地址" }], warnings: deviceLedger().some(device => !device.profile_id) ? ["仍有设备未绑定；项目包不携带现场地址与运行数据。"] : [],
  })
}

function preflight(site: string, persisted = false): JsonObject {
  const ledger = deviceLedger()
  const unbound = ledger.filter(device => !device.profile_id).length
  const unverified = ledger.filter(device => device.profile_id && !device.verified).length
  const rules = siteNodes(site).filter(node => node.type === "decisionTable")
  const gates = [
    { key: "ledger", title: "设备接入完整", status: unbound ? "fail" : "pass", detail: unbound ? `${unbound} 台设备未绑定通道与 Profile` : "全部设备已绑定", next_step: unbound ? "回第 3 步完成批量或逐台接入" : "" },
    { key: "verification", title: "现场验证", status: unverified ? "warn" : "pass", detail: unverified ? `${unverified} 台设备尚未完成人工验证` : "已绑定设备均有验证记录", next_step: unverified ? "逐台完成 L1/L2 与人工签收" : "" },
    { key: "rules", title: "策略已发布", status: rules.some(rule => !!activePoint(rule, "applied")?.text) ? "pass" : "warn", detail: `${rules.length} 张决策表；发布与启用分开记录` },
    { key: "screens", title: "客户画面", status: state.screens[site] ? "pass" : "warn", detail: state.screens[site] ? `本层画面 v${state.screens[site].version}，其他层可继承` : "当前使用默认块" },
    { key: "package", title: "项目包依赖", status: "pass", detail: "ems@1.0.0 与 Profile 版本已固定", informational: true },
  ]
  const summary = { pass: gates.filter(gate => gate.status === "pass").length, warn: gates.filter(gate => gate.status === "warn").length, fail: gates.filter(gate => gate.status === "fail").length }
  return { ...(persisted ? { id: nextID(state, "preflight") } : {}), site, generated_at: interactionTime(state), operator: "preview-admin", status: summary.fail ? "fail" : summary.warn ? "warn" : "pass", summary, gates }
}

function publishProject(body: JsonObject): DesignPreviewResult {
  const site = String(body.site || "")
  if (!siteNodes(site).length) return fail(404, "site_not_found")
  const report = preflight(site)
  if (report.status === "fail" && !body.confirm) return fail(409, "preflight_failed")
  const version = { id: nextID(state, "v2026.11.23"), site_id: site, site_name: nodeName(state.nodes.find(node => node.id === site)), created_at: interactionTime(state), operator: "preview-admin", note: String(body.note || ""), preflight_status: report.status, nodes: siteNodes(site).length }
  state.versions.unshift(version)
  return ok(version)
}

function applyProfile(unit: PreviewUnit, profileID: string, businessNodeID?: string) {
  const specs = profileSpecs(profileID)
  if (!specs.length) return
  if (!unit.points.length || unit.points.length < specs.length) {
    const businessKey = businessNodeID || unit.model?.business_node_id || `station-${unit.server_id}`
    unit.points = physicalPoints(unit.channel_id, businessKey, unit.server_id, specs)
    const existing = new Set(state.nodes.map(node => node.id))
    for (const item of unit.points) if (!existing.has(item.id)) state.nodes.push(physicalNode(unit.channel_id, item))
  }
  unit.model = {
    ...(unit.model || {}), name: unit.model?.name || nodeName(state.nodes.find(node => node.id === businessNodeID)) || `站号 ${unit.server_id}`,
    profile_id: profileID, ...(businessNodeID || unit.model?.business_node_id ? { business_node_id: businessNodeID || unit.model?.business_node_id } : {}),
    point_map: pointMap(specs, unit.points), verified: false, verifier: "", verified_at: "", device_serial: "", verification: "",
  }
  syncBinding(state, unit)
  for (const item of unit.points) {
    const sample = state.values.find(value => value.point_id === item.id)
    if (sample) { sample.quality = "good"; delete sample.reason; sample.source_time = NOW }
    else state.values.push({ device_id: `${unit.channel_id}/${unit.server_id}`, point_id: item.id, value: { kind: "number", number: 0 }, quality: "good", source_time: NOW, received_time: NOW })
  }
  if (businessNodeID) {
    for (const sample of state.values.filter(value => value.device_id === businessNodeID)) { sample.quality = "good"; delete sample.reason }
  }
}

function profileApply(channelID: string, body: JsonObject): DesignPreviewResult {
  const profileID = String(body.profile_id || "")
  const specs = profileSpecs(profileID)
  if (!specs.length) return fail(404, "profile_not_found")
  const servers = Array.isArray(body.servers) ? body.servers.map(Number).filter(Number.isInteger) : []
  if (!servers.length) return fail(400, "servers_required")
  const results = servers.map(server => {
    const unit = ensureUnit(channelID, server)
    const points = specs.map(spec => {
      const match = unit.points.find(item => item.name === spec.name && item.server_id === server)
      return { name: spec.name, ...(match ? { point_id: match.id } : {}), verdict: match ? "exact" : "missing", ...(match ? {} : { reason: "当前站号没有同名物理点" }) }
    })
    return { server, profile_id: profileID, mapped: points.filter(item => item.point_id).length, exact: points.filter(item => item.verdict === "exact").length, scaled: 0, mismatched: 0, missing: points.filter(item => !item.point_id).map(item => item.name), blocked: [], points }
  })
  if (!body.dry_run) {
    for (const server of servers) applyProfile(ensureUnit(channelID, server), profileID, unitFor(channelID, server)?.model?.business_node_id || ({ 1: "pump-1", 2: "pump-2", 3: "tank" } as Record<number, string>)[server])
  }
  return ok({ profile_id: profileID, dry_run: !!body.dry_run, applied: body.dry_run ? 0 : servers.length, results })
}

function parseCSV(text: string): string[][] {
  const rows: string[][] = []
  let row: string[] = [], cell = "", quoted = false
  for (let index = 0; index < text.length; index++) {
    const char = text[index]
    if (char === '"') {
      if (quoted && text[index + 1] === '"') { cell += '"'; index++ } else quoted = !quoted
    } else if (char === "," && !quoted) { row.push(cell); cell = "" }
    else if ((char === "\n" || char === "\r") && !quoted) {
      if (char === "\r" && text[index + 1] === "\n") index++
      row.push(cell); cell = ""
      if (row.some(value => value.trim())) rows.push(row)
      row = []
    } else cell += char
  }
  row.push(cell)
  if (row.some(value => value.trim())) rows.push(row)
  return rows
}

function batchAccess(body: JsonObject, reviewed = false): DesignPreviewResult {
  // Preview-only compatibility. Real acceptance uses the Go reviewed endpoint.
  const previewVersion = JSON.stringify([body.csv, state.units, state.nodes])
  if (reviewed && !body.dry_run && body.preview_version !== previewVersion) return fail(409, "batch_preview_changed")
  const rows = parseCSV(String(body.csv || "")).slice(1).map((cells, index) => {
    const [device = "", profileID = "", channelID = "", stationText = ""] = cells.map(cell => cell.trim())
    const station = Number(stationText)
    const business = state.nodes.find(node => node.id === device && node.type === "device")
    const errors: string[] = []
    if (!business) errors.push("设备占位不存在")
    if (!profileSpecs(profileID).length) errors.push("Profile 不存在或没有点表")
    if (!state.nodes.some(node => node.id === channelID && node.type === "modbus")) errors.push("通道不存在")
    if (!Number.isInteger(station) || station < 1 || station > 247) errors.push("站号必须是 1–247 的整数")
    return { line: index + 2, device, model: profileID, channel: channelID, station, device_id: device, profile_id: profileID, channel_id: channelID, valid: errors.length === 0, status: errors.length ? "blocked" : body.dry_run ? "ready" : "mapped", errors, created_points: profileSpecs(profileID).length, mapped: 0, missing: [] }
  })
  if (!body.dry_run) {
    for (const row of rows.filter(item => item.valid)) {
      const unit = ensureUnit(row.channel_id, row.station)
      applyProfile(unit, row.profile_id, row.device_id)
      row.mapped = Object.keys(unit.model?.point_map || {}).length
      row.created_points = 0
    }
  }
  return ok({ dry_run: !!body.dry_run, applied: body.dry_run ? 0 : rows.filter(row => row.status === "mapped").length, rows, ...(reviewed ? { preview_version: previewVersion } : {}) })
}

function verification(unit: PreviewUnit, body: JsonObject): JsonObject {
  const level = String(body.level || "L1")
  const result: JsonObject = { level, passed: true, reasons: [], recorded: true }
  if (level === "L1") result.line = { connected: true, stations: [{ server_id: unit.server_id, responded: true, error: "" }] }
  if (level === "L2") result.points = Object.entries(unit.model?.point_map || {}).map(([name, id]) => ({ name, point_id: id, passed: true, quality: "good", source_time: NOW, reason: "" }))
  if (level === "L3") result.command = { state: "confirmed", reason: "固定安全值已写入并回读一致" }
  unit.records.push({ at: interactionTime(state), actor: "preview-admin", level, passed: true, reasons: [] })
  return result
}

export function resolveDesignPreview(path: string, method: string, body?: unknown): DesignPreviewResult {
  const verb = method.toUpperCase()
  const url = new URL(path, "http://design.preview")
  const pathname = url.pathname
  const input = asBody(body)

  if (pathname === "/snapshot" && verb === "GET") return ok(snapshot())
  if (pathname === "/templates" && verb === "GET") return ok({ site_templates: clone(siteTemplates) })
  if (pathname === "/ems/state" && verb === "GET") return ok({ values: clone(state.values), evaluated_at: NOW })
  if (pathname === "/ems/quality" && verb === "GET") return ok(quality())
  if (pathname === "/ems/alarms" && verb === "GET") return ok({ alarms: clone(state.alarms) })
  if (pathname === "/device-ledger" && verb === "GET") return ok({ devices: deviceLedger() })
  if (pathname === "/ems/profiles" && verb === "GET") return ok({ profiles: clone(profiles) })
  if (pathname === "/calculations" && verb === "GET") return ok({ calculations: [{ id: "calc-pump-power", name: "泵组总有功功率", state: { value: aggregateResult(state.aggregates[ROOT]).value, quality: "good", unit: "kW" } }], sources: [], can_create: true, evaluated_at: NOW, batch_id: "preview-batch-1" })
  if (pathname === "/virtual-inputs" && verb === "GET") return ok({ inputs: [{ id: "input-permit", name: "运行许可", source: "configuration", state: { value: 1, quality: "good", unit: "" } }] })
  if (pathname === "/state-machines" && verb === "GET") return ok({ state_machines: state.machines.map(machineResponse), can_create: true })

  if (pathname === "/industry/packages" && verb === "GET") return ok({ packages: [{ id: "ems", version: "1.0.0", installed: state.emsInstalled }] })
  if (pathname === "/industry/packages" && verb === "POST") { state.emsInstalled = true; return ok({ installed: true }) }
  if (pathname === "/industry/packages/ems" && verb === "DELETE") { state.emsInstalled = false; return ok({ installed: false }) }

  if (pathname === "/nodes" && verb === "POST") return createNode(input)
  let match = pathname.match(/^\/nodes\/([^/]+)\/copy$/)
  if (match && verb === "POST") return copySubtree(decodeURIComponent(match[1]), input)
  match = pathname.match(/^\/nodes\/([^/]+)$/)
  if (match && verb === "PATCH") {
    const id = decodeURIComponent(match[1])
    const node = state.nodes.find(item => item.id === id)
    if (!node) return fail(404, "not_found")
    if (input.description) upsertPoint(node, point("description", String(input.description)))
    if (Array.isArray(input.members)) upsertPoint(node, point("easyemsUnitMembers", JSON.stringify(input.members)))
    const missing = (input.members || []).filter((role: JsonObject) => role.required && !role.node_id).map((role: JsonObject) => role.role)
    return ok({ saved: true, missing_required: missing })
  }

  if (pathname === "/ems/channels" && verb === "GET") {
    const channels = [...new Set(state.nodes.filter(node => node.type === "modbus").map(node => node.id))].map(id => ({
      id, node_id: id, protocol: (activePoint(state.nodes.find(node => node.id === id), "protocol")?.text || "TCP").toLowerCase(),
      address: activePoint(state.nodes.find(node => node.id === id), "uri")?.text || activePoint(state.nodes.find(node => node.id === id), "port")?.text || "",
      period_ms: activePoint(state.nodes.find(node => node.id === id), "pollPeriod")?.value || 1000, persisted: true,
      devices: Object.values(state.units).filter(unit => unit.channel_id === id).map(unit => ({ id: `${id}/${unit.server_id}`, device_id: unit.model?.business_node_id || `${id}/${unit.server_id}`, server_id: unit.server_id, health: unit.model?.profile_id ? "healthy" : "offline", points: unit.points.map(item => ({ id: item.id, name: item.name, address: item.address, read_only: item.read_only })) })),
    }))
    return ok({ channels })
  }
  if (pathname === "/modbus" && verb === "POST") {
    const id = nextID(state, "channel")
    const node = makeNode(id, "modbus", String(input.parent || ROOT), String(input.description || "新 Modbus 通道"), [point("protocol", String(input.protocol || "TCP")), point("uri", String(input.uri || "")), point("port", String(input.port || "")), point("baud", String(input.baud || "9600")), point("pollPeriod", undefined, Number(input.poll_period || 1000)), point("timeout", undefined, Number(input.timeout || 1000))])
    state.nodes.push(node)
    return ok({ id, node_id: id })
  }
  match = pathname.match(/^\/modbus\/([^/]+)$/)
  if (match && verb === "PUT") {
    const id = decodeURIComponent(match[1])
    const node = state.nodes.find(item => item.id === id && item.type === "modbus")
    if (!node) return fail(404, "not_found")
    for (const [field, type] of [["description", "description"], ["protocol", "protocol"], ["uri", "uri"], ["port", "port"], ["baud", "baud"]] as const) if (input[field] !== undefined) upsertPoint(node, point(type, String(input[field])))
    for (const [field, type] of [["poll_period", "pollPeriod"], ["timeout", "timeout"]] as const) if (input[field] !== undefined) upsertPoint(node, point(type, undefined, Number(input[field])))
    return ok({ id, node_id: id })
  }
  match = pathname.match(/^\/modbus\/([^/]+)\/io$/)
  if (match && verb === "POST") {
    const channelID = decodeURIComponent(match[1])
    if (!state.nodes.some(node => node.id === channelID && node.type === "modbus")) return fail(404, "not_found")
    const serverID = Number(input.server_id || 1)
    const unit = ensureUnit(channelID, serverID)
    const item: UnitPoint = { id: nextID(state, "io"), name: String(input.description || "新物理点"), server_id: serverID, address: Number(input.address || 0), io_type: String(input.io_type || "modbusHoldingRegister"), data_format: String(input.data_format || "uint16"), read_only: input.read_only !== false, unit: String(input.units || ""), scale: Number(input.scale || 1), offset: Number(input.offset || 0) }
    unit.points.push(item)
    state.nodes.push(physicalNode(channelID, item))
    state.values.push({ device_id: `${channelID}/${serverID}`, point_id: item.id, value: { kind: "number", number: 0 }, quality: "good", source_time: NOW, received_time: NOW })
    return ok({ id: item.id })
  }
  match = pathname.match(/^\/modbus\/([^/]+)\/units\/apply$/)
  if (match && verb === "POST") return profileApply(decodeURIComponent(match[1]), input)
  match = pathname.match(/^\/modbus\/([^/]+)\/units\/(\d+)\/model$/)
  if (match && verb === "GET") {
    const unit = unitFor(decodeURIComponent(match[1]), Number(match[2]))
    return unit ? ok(modelView(unit)) : ok({ channel_id: decodeURIComponent(match[1]), server_id: Number(match[2]), profiles: clone(profiles), points: [], commands: [], missing: [] })
  }
  if (match && verb === "PUT") {
    const unit = ensureUnit(decodeURIComponent(match[1]), Number(match[2]))
    unit.model = { name: `站号 ${unit.server_id}`, profile_id: "", verified: false, ...(unit.model || {}), ...clone(input), point_map: clone(input.point_map || {}) }
    syncBinding(state, unit)
    return ok(modelView(unit))
  }
  match = pathname.match(/^\/modbus\/([^/]+)\/units\/(\d+)\/verify$/)
  if (match && verb === "GET") return ok({ records: clone(unitFor(decodeURIComponent(match[1]), Number(match[2]))?.records || []) })
  if (match && verb === "POST") return ok(verification(ensureUnit(decodeURIComponent(match[1]), Number(match[2])), input))
  match = pathname.match(/^\/modbus\/([^/]+)\/line-test$/)
  if (match && (verb === "GET" || verb === "POST")) {
    const channelID = decodeURIComponent(match[1])
    return ok({ connected: true, latency_ms: 2, probed: Object.values(state.units).filter(unit => unit.channel_id === channelID).length, stations: Object.values(state.units).filter(unit => unit.channel_id === channelID).map(unit => ({ server_id: unit.server_id, responded: true, latency_ms: 2, probe: "holdingRegister:0", error: "" })) })
  }
  if (pathname === "/access/batch" && verb === "POST") return batchAccess(input)
  if (pathname === "/access/batch/reviewed" && verb === "POST") return batchAccess(input, true)

  match = pathname.match(/^\/rules\/([^/]+)\/test$/)
  if (match && verb === "POST") return ruleByID(decodeURIComponent(match[1])) ? ok(evaluateRule(input)) : fail(404, "not_found")
  match = pathname.match(/^\/rules\/([^/]+)\/(draft|publish|enable)$/)
  if (match) {
    const rule = ruleByID(decodeURIComponent(match[1]))
    if (!rule) return fail(404, "not_found")
    if (match[2] === "draft" && verb === "PUT") { upsertPoint(rule, point("draft", JSON.stringify(input))); return ok({ saved: true }) }
    if (match[2] === "publish" && verb === "POST") { upsertPoint(rule, point("draft", JSON.stringify(input))); upsertPoint(rule, point("applied", JSON.stringify(input))); return ok({ published: true }) }
    if (match[2] === "enable" && verb === "POST") { if (input.enabled && !activePoint(rule, "applied")?.text) return fail(409, "not_published"); upsertPoint(rule, point("enabled", undefined, input.enabled ? 1 : 0)); return ok({ enabled: !!input.enabled }) }
  }

  if (pathname === "/state-machines" && verb === "POST") {
    const definition = clone(input.definition || {})
    const machine: StateMachine = { id: nextID(state, "machine"), parent: String(input.parent || ROOT), name: String(input.name || "设备过程状态机"), revision: 1, version: 0, draft: definition, current_state: definition.initial_state || "stopped" }
    state.machines.push(machine)
    return ok(machineResponse(machine))
  }
  match = pathname.match(/^\/state-machines\/([^/]+)\/(draft|publish|simulate|execute)$/)
  if (match && verb === "POST") {
    const id = decodeURIComponent(match[1])
    const machine = state.machines.find(item => item.id === id)
    if (!machine) return fail(404, "not_found")
    if (match[2] === "draft") { machine.draft = clone(input.definition || {}); machine.revision++; return ok(machineResponse(machine)) }
    if (match[2] === "publish") { machine.applied = clone(machine.draft); machine.version++; machine.revision++; return ok(machineResponse(machine)) }
    if (match[2] === "simulate") {
      machine.simulation = simulateMachine(machine, input.definition || machine.draft, String(input.event || ""), asBody(input.inputs), asBody(input.text_inputs), input.at)
      return ok(machineResponse(machine))
    }
    if (!input.confirm) return fail(400, "confirmation_required")
    const simulation = simulateMachine(machine, machine.applied || machine.draft, String(input.event || ""), liveMachineInputs(), {}, input.at)
    const execution = { at: interactionTime(state), event: String(input.event || ""), status: simulation.status === "matched" ? "confirmed" : "held", transition_id: simulation.transition_id, from: simulation.from, to: simulation.to, reason: simulation.status === "matched" ? "全部动作已回读确认" : simulation.reason, actions: (simulation.actions || []).map((action: JsonObject) => ({ output: action.action_id, requested: action.value, applied: action.value, status: "confirmed", reason: "预览回读一致" })) }
    if (simulation.status === "matched") machine.current_state = simulation.to
    machine.last_execution = execution
    machine.execution = execution
    return ok(machineResponse(machine))
  }

  if (pathname === "/aggregate" && verb === "GET") {
    const group = url.searchParams.get("group") || ""
    const config = state.aggregates[group]
    return config ? ok(aggregateResult(config)) : ok({ configured: false, config: { group, op: "sum", members: [] }, quality: "unconfigured", members: [] })
  }
  if (pathname === "/aggregate" && verb === "PUT") {
    if (!input.group || !Array.isArray(input.members) || !input.members.length) return fail(400, "invalid_aggregate")
    state.aggregates[String(input.group)] = clone(input)
    return ok(aggregateResult(state.aggregates[String(input.group)]))
  }

  match = pathname.match(/^\/ems\/screen\/([^/]+)$/)
  if (match && verb === "GET") {
    const id = decodeURIComponent(match[1])
    const screen = screenFor(id)
    return screen ? ok(screen) : fail(404, "screen_not_found")
  }
  if (match && verb === "PUT") return saveScreen(decodeURIComponent(match[1]), input)

  match = pathname.match(/^\/ems\/alarms\/([^/]+)\/(ack|close)$/)
  if (match && verb === "POST") {
    const id = decodeURIComponent(match[1])
    const alarm = state.alarms.find(item => item.id === id)
    if (!alarm) return fail(404, "not_found")
    if (match[2] === "ack") { alarm.state = "acknowledged"; alarm.acked_by = "preview-admin"; alarm.acked_at = interactionTime(state) }
    else { alarm.active = false; alarm.state = "closed"; alarm.closed_at = interactionTime(state) }
    alarm.updated_at = interactionTime(state)
    return ok({ alarm: clone(alarm) })
  }

  if (pathname === "/projects/export" && verb === "GET") return projectPackage(url.searchParams.get("site") || "")
  if (pathname === "/projects/versions" && verb === "GET") return ok({ versions: clone(state.versions.filter(version => !url.searchParams.get("site") || version.site_id === url.searchParams.get("site"))) })
  if (pathname === "/projects/publish" && verb === "POST") return publishProject(input)
  if (pathname === "/delivery/gates" && verb === "GET") return ok(preflight(url.searchParams.get("site") || ROOT))
  if (pathname === "/delivery/preflight" && verb === "POST") { state.latestPreflight = preflight(url.searchParams.get("site") || ROOT, true); return ok(clone(state.latestPreflight)) }
  if (pathname === "/delivery/preflight" && verb === "GET") return ok({ latest: clone(state.latestPreflight) })

  if (pathname === "/overview/handover" && verb === "GET") return ok({ handover: clone(state.handovers[url.searchParams.get("layer") || ""] || null) })
  if (pathname === "/overview/handover" && verb === "PUT") {
    const layer = url.searchParams.get("layer") || ""
    const note = String(input.note || "").trim()
    if (!layer || !note) return fail(400, "invalid_handover_note")
    state.handovers[layer] = { layer_id: layer, note, actor: "preview-admin", updated_at: interactionTime(state) }
    return ok({ handover: clone(state.handovers[layer]) })
  }

  return unhandled
}
