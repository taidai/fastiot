import { reactive } from "vue"

export type DeliveryPreviewImport = { site_id: string; name: string; created: number; channels: number; rules: number; node_ids?: string[]; version_id?: string; warnings?: string[] }
const freshDeliveryState = () => ({
  current: "package", selectedSite: "", publishNote: "", publishedSites: {} as Record<string, boolean>,
  preflightFail: false, dependencyMismatch: false, permissionDemo: false,
  importStep: "idle" as "idle" | "mismatch" | "imported" | "rebound" | "running",
  importResult: null as DeliveryPreviewImport | null,
  importStatus: "未导入",
})
export const deliveryPreviewState = reactive(freshDeliveryState())
let deliveryProject = 'site-pump'
const presentationStates = new Map<string, ReturnType<typeof freshDeliveryState>>()
export function selectDeliveryPreviewProject(id: string) {
  if (deliveryProject === id) return
  presentationStates.set(deliveryProject, JSON.parse(JSON.stringify(deliveryPreviewState)))
  deliveryProject = id
  Object.assign(deliveryPreviewState, presentationStates.get(id) || freshDeliveryState())
}

export type DeliveryPreviewResult = { handled: boolean; status?: number; data?: unknown }

type PreviewPoint = { type: string; key: string; time: string; text?: string; value?: number }
type PreviewNode = { id: string; type: string; parent: string; points: PreviewPoint[] }
type PreviewVersion = {
  id: string; site_id: string; site_name: string; created_at: string; operator: string
  note?: string; preflight_status?: string; nodes: number
}
type PreviewUser = {
  id: string; email: string
  policy: { admin: boolean; role: string; root_node_ids: string[]; actions: string[] }
}
type AuditRecord = { time: string; actor: string; action: string; object: string; detail: string; result: string }
type PackageInput = {
  format?: string; format_version?: number; name?: string
  dependencies?: { kind: string; id: string; version: string }[]
}

export const DELIVERY_PREVIEW_DEPLOYMENTS = [
  { target: "教学泵房（本站）", version: "v2026.11.19-1", time: "11-19 09:58", operator: "张工", status: "运行中" },
  { target: "储能站 A", version: "—", time: "—", operator: "—", status: "待部署" },
]

const description = (text: string): PreviewPoint => ({ type: "description", key: "", time: "", text })
const textPoint = (type: string, text: string): PreviewPoint => ({ type, key: "", time: "", text })
const pointText = (node: PreviewNode | undefined, type: string) => node?.points.find(point => point.type === type)?.text || ""
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T
const ok = (data: unknown): DeliveryPreviewResult => ({ handled: true, data })
const fail = (status: number, error: string): DeliveryPreviewResult => ({ handled: true, status, data: { error } })

const nodes: PreviewNode[] = [
  { id: "root-project", type: "group", parent: "", points: [description("教学泵房")] },
  { id: "site-pump", type: "site", parent: "root-project", points: [description("教学泵房（本站）")] },
  { id: "site-energy", type: "site", parent: "root-project", points: [description("储能站 A")] },
  { id: "area-pump", type: "group", parent: "site-pump", points: [description("泵房")] },
  { id: "pump-1", type: "device", parent: "area-pump", points: [description("1号泵")] },
  { id: "pump-2", type: "device", parent: "area-pump", points: [description("2号泵")] },
  { id: "tank-1", type: "device", parent: "area-pump", points: [description("水箱")] },
  { id: "channel-pump", type: "modbus", parent: "site-pump", points: [description("泵房 Modbus 总线"), textPoint("protocol", "TCP"), textPoint("uri", "127.0.0.1:1502")] },
  { id: "point-pressure", type: "modbusIo", parent: "channel-pump", points: [description("出口压力"), textPoint("units", "MPa")] },
  { id: "point-level", type: "modbusIo", parent: "channel-pump", points: [description("水箱液位"), textPoint("units", "%")] },
  { id: "rule-pump", type: "decisionTable", parent: "site-pump", points: [description("泵组压力调节")] },
  { id: "pcs-1", type: "device", parent: "site-energy", points: [description("PCS1")] },
  { id: "bms-1", type: "device", parent: "site-energy", points: [description("BMS1")] },
]

let versions: PreviewVersion[] = [
  { id: "v2026.11.21-1", site_id: "site-pump", site_name: "教学泵房", created_at: "2026-11-21T10:18:00+08:00", operator: "张工", note: "交付基线", preflight_status: "pass", nodes: 14 },
  { id: "v2026.11.19-1", site_id: "site-pump", site_name: "教学泵房", created_at: "2026-11-19T09:58:00+08:00", operator: "张工", note: "泵组联调完成", preflight_status: "pass", nodes: 13 },
  { id: "v2026.11.17-1", site_id: "site-pump", site_name: "教学泵房", created_at: "2026-11-17T16:26:00+08:00", operator: "李工", note: "首次工程冻结", preflight_status: "pass", nodes: 12 },
]

let industryInstalled = true
export function setDeliveryPreviewEms(enabled: boolean) { industryInstalled = enabled }

export function setDeliveryPreviewScenarios(scenarios: { preflightFail?: boolean; dependencyMismatch?: boolean }) {
  if (scenarios.preflightFail !== undefined) deliveryPreviewState.preflightFail = scenarios.preflightFail
  if (scenarios.dependencyMismatch !== undefined) deliveryPreviewState.dependencyMismatch = scenarios.dependencyMismatch
}
let publishSequence = 1
let preflightSequence = 1
let imported = false
const latestPreflight: Record<string, unknown> = {
  "site-pump": {
    id: "pf-20261121-1", site: "site-pump", generated_at: "2026-11-21T10:12:00+08:00", operator: "张工",
    status: "pass", summary: { pass: 10, warn: 0, fail: 0 }, gates: [],
  },
}

const users: PreviewUser[] = [
  { id: "u-zhang", email: "张工", policy: { admin: false, role: "implementer", root_node_ids: ["root-project"], actions: [] } },
  { id: "u-li", email: "李工", policy: { admin: false, role: "operator", root_node_ids: ["site-pump"], actions: [] } },
  { id: "u-wang", email: "王管理员", policy: { admin: true, role: "admin", root_node_ids: ["root-project"], actions: [] } },
]

const auditRecords: AuditRecord[] = [
  { time: "2026-11-21T10:42:00+08:00", actor: "张工", action: "启动设备", object: "2号泵", detail: "命令回读一致", result: "success" },
  { time: "2026-11-21T10:40:00+08:00", actor: "李工", action: "确认报警", object: "2号泵 出口压力低", detail: "已接手", result: "success" },
  { time: "2026-11-19T09:58:00+08:00", actor: "张工", action: "项目发布", object: "v2026.11.19-1", detail: "13 个节点 · 泵组联调完成", result: "success" },
  { time: "2026-11-19T09:12:00+08:00", actor: "张工", action: "修改点位", object: "1号泵", detail: "绑定点位已应用", result: "success" },
]

const projectPackage = {
  format: "fastiot.project-package", format_version: 2, id: "pp-pump-20261121", name: "教学泵房", version: 2,
  source_version: "v2026.11.21-1", created_at: "2026-11-21T10:20:00+08:00", operator: "张工",
  point_count: 36, strategy_count: 3,
  dependencies: [
    { kind: "industry", id: "ems", version: "1.0.0" },
    { kind: "profile", id: "pump", version: "v1.3" },
  ],
  nodes: [
    { ref: "site", type: "site" }, { ref: "area", type: "group", parent: "site" },
    { ref: "pump-1", type: "device", parent: "area" }, { ref: "pump-2", type: "device", parent: "area" },
    { ref: "tank", type: "device", parent: "area" }, { ref: "channel", type: "modbus", parent: "site" },
    { ref: "pressure", type: "modbusIo", parent: "channel" }, { ref: "level", type: "modbusIo", parent: "channel" },
    { ref: "current", type: "modbusIo", parent: "channel" }, { ref: "run", type: "modbusIo", parent: "channel" },
    { ref: "target", type: "modbusIo", parent: "channel" }, { ref: "rule", type: "decisionTable", parent: "site" },
  ],
  screens: [{ node: "site", source: "screen:site" }, { node: "pump-1", source: "screen:pump" }],
  stripped: [
    { node: "channel", point: "uri", reason: "连接地址" }, { node: "channel", point: "credential", reason: "凭据" },
    { node: "rule", point: "enabled", reason: "控制启动" }, { node: "pressure", point: "value", reason: "运行状态" },
  ],
  warnings: [],
}

let runtimeValues: unknown[] = [
  { point_id: "point-pressure", device_id: "channel-pump/1", value: { kind: "number", number: 0.42 }, quality: "good", source_time: "2026-11-21T10:42:02+08:00" },
  { point_id: "point-level", device_id: "channel-pump/3", value: { kind: "number", number: 68 }, quality: "good", source_time: "2026-11-21T10:42:02+08:00" },
]
let runtimeChannels: unknown[] = [
  { channel_id: "channel-pump", health: "healthy", polls: 128, failures: 0, last_success_at: "2026-11-21T10:42:02+08:00" },
]

function gates(site: string) {
  return {
    id: `gates-${site}`, site, generated_at: "2026-11-21T10:12:00+08:00", operator: "张工",
    status: deliveryPreviewState.preflightFail ? "fail" : "pass", summary: deliveryPreviewState.preflightFail ? { pass: 9, warn: 0, fail: 1 } : { pass: 10, warn: 0, fail: 0 },
    gates: [
      { key: "dependencies", title: "行业包与 Profile 依赖", status: "pass", detail: "ems@1.0.0 与泵 Profile v1.3 已锁定且匹配" },
      { key: "mappings", title: "点位与画面映射", status: deliveryPreviewState.preflightFail ? "fail" : "pass", detail: deliveryPreviewState.preflightFail ? "PCS7 必需点未映射" : "必需点已映射，画面绑定已 token 化" },
      { key: "channels", title: "通道与设备", status: "pass", detail: "3 台设备已绑定；通道连接配置完整" },
      { key: "permissions", title: "权限边界", status: "pass", detail: "交付不自动授予控制权限，服务端逐请求校验" },
    ],
  }
}

function industryPackages() {
  return [{
    id: "ems", version: "1.0.0", label: "EMS 行业包",
    provides: ["能源单元、设备与能力约束", "能源策略与电站级目标", "功率分配、仲裁与联合调度", "需量窗口与 96 点计划"],
    installed: industryInstalled, installed_at: industryInstalled ? "2026-11-17T09:00:00+08:00" : "",
  }]
}

function importPackage(body: unknown): DeliveryPreviewResult {
  const input = (body || {}) as { package?: PackageInput & { source_version?: string }; parent?: string }
  const pkg = input.package || {}
  if (deliveryPreviewState.dependencyMismatch) {
    deliveryPreviewState.importStep = "mismatch"
    return fail(400, "incompatible_dependency:industry:ems:required@0.9.0:available@1.0.0")
  }
  const source = versions.find(version => version.id === pkg.source_version)?.site_id || "site-pump"
  if (!deliveryPreviewState.publishedSites[source]) return fail(409, "publish_required")
  if (pkg.format && pkg.format !== "fastiot.project-package") return fail(400, "invalid_package_format")
  if (pkg.format_version !== undefined && pkg.format_version !== 2) return fail(400, `unsupported_package_version:${pkg.format_version}`)
  const ems = pkg.dependencies?.find(item => item.kind === "industry" && item.id === "ems")
  if (ems && !industryInstalled) return fail(400, `missing_dependency:industry:ems@${ems.version}:not_installed`)
  if (ems && ems.version !== "1.0.0") return fail(400, `incompatible_dependency:industry:ems:required@${ems.version}:available@1.0.0`)

  const name = pkg.name?.trim() || "教学泵房（新实例）"
  if (!imported) {
    imported = true
    nodes.push(
      { id: "site-imported", type: "site", parent: input.parent || "root-project", points: [description(name)] },
      { id: "channel-imported", type: "modbus", parent: "site-imported", points: [description("导入总线"), textPoint("protocol", "TCP"), textPoint("uri", "")] },
      { id: "point-imported", type: "modbusIo", parent: "channel-imported", points: [description("出口压力"), textPoint("units", "MPa")] },
      { id: "rule-imported", type: "decisionTable", parent: "site-imported", points: [description("泵组压力调节")] },
    )
    versions.unshift({ id: "v2026.11.21-import-1", site_id: "site-imported", site_name: name, created_at: "2026-11-24T10:26:00+08:00", operator: "王管理员", note: "跨实例导入基线", preflight_status: "warn", nodes: 4 })
  } else {
    const site = nodes.find(node => node.id === "site-imported")
    if (site) site.points = [description(name)]
  }
  auditRecords.unshift({ time: "2026-11-24T10:26:00+08:00", actor: "王管理员", action: "项目导入", object: name, detail: "4 个节点 · 地址与凭据待重绑", result: "success" })
  deliveryPreviewState.importStep = "imported"
  runtimeValues = runtimeValues.filter(value => (value as { point_id: string }).point_id !== "point-imported")
  runtimeChannels = runtimeChannels.filter(channel => (channel as { channel_id: string }).channel_id !== "channel-imported")
  const channel = nodes.find(node => node.id === "channel-imported")!
  channel.points = [description("导入总线"), textPoint("protocol", "TCP"), textPoint("uri", "")]
  delete latestPreflight["site-imported"]
  const result = {
    site_id: "site-imported", name, created: 4, model: 0, channels: 1, rules: 1, profiles: ["pump@v1.3"],
    version_id: "v2026.11.21-import-1", node_ids: ["site-imported", "channel-imported", "point-imported", "rule-imported"],
    ignored: [{ node: "channel-imported", point: "uri", reason: "连接地址" }],
    warnings: ["请核对画面 token 与现场点位映射；地址、凭据和控制启动未导入"],
  }
  deliveryPreviewState.importResult = result
  deliveryPreviewState.importStatus = `已导入 ${name}；待现场重绑（原型）`
  return ok(clone(result))
}

function rebind(id: string, body: unknown): DeliveryPreviewResult {
  const channel = nodes.find(node => node.id === id && node.type === "modbus")
  if (!channel) return fail(404, "channel_not_found")
  const input = (body || {}) as { description?: string; protocol?: string; uri?: string }
  channel.points = [
    description(input.description || pointText(channel, "description") || id),
    textPoint("protocol", input.protocol || "TCP"), textPoint("uri", input.uri || ""),
  ]
  if (id === "channel-imported") {
    deliveryPreviewState.importStep = input.uri?.trim() ? "rebound" : "imported"
    deliveryPreviewState.importStatus = input.uri?.trim() ? "现场通道与地址已重绑（原型）；待运行" : "待现场重绑（原型）"
  }
  return ok({ saved: true })
}

export function deliveryPermissionCheck(policy = users.find(user => user.id === "u-li")!.policy) {
  const allows = (action: string) => policy.admin || policy.role === "admin" || (policy.actions.length
    ? policy.actions.includes(action)
    : policy.role === "implementer" || (policy.role === "operator" && action === "command"))
  const inScope = policy.root_node_ids.some(id => ["root-project", "site-pump", "area-pump", "pump-2"].includes(id))
  return {
    start: allows("command") && inScope ? "允许（仅泵房）" : "拒绝",
    editPoints: allows("configure") ? "允许" : "拒绝",
    publish: allows("configure") ? "允许" : "拒绝",
  }
}

function evidence(site: string) {
  const siteNodes = nodes.filter(node => node.id === site || node.parent === site)
  const scoped = new Set(siteNodes.map(node => node.id))
  for (let changed = true; changed;) {
    changed = false
    for (const node of nodes) if (scoped.has(node.parent) && !scoped.has(node.id)) { scoped.add(node.id); changed = true }
  }
  const devices = nodes.filter(node => scoped.has(node.id) && node.type === "device")
  return {
    generatedAt: new Date().toISOString(),
    project: pointText(nodes.find(node => node.id === site), "description") || site,
    version: deliveryPreviewState.publishedSites[site] ? versions.find(version => version.site_id === site)?.id || "未发布" : "未发布",
    preflight: { status: deliveryPreviewState.preflightFail ? "failed" : "passed", reason: deliveryPreviewState.preflightFail ? "PCS7 必需点未映射" : null },
    package: {
      nodes: scoped.size, devices: { total: devices.length, unbound: 0 },
      screens: projectPackage.screens.length,
      rules: nodes.filter(node => scoped.has(node.id) && node.type === "decisionTable").length,
      dependencies: projectPackage.dependencies.filter(item => item.kind !== "industry" || industryInstalled).map(item => `${item.id}@${item.version}`),
    },
    importStep: deliveryPreviewState.importStep,
    mappingDiff: [{ device: "PCS7", required: "有功功率", mapped: !deliveryPreviewState.preflightFail }],
    audit: auditRecords.map(record => ({ at: record.time, actor: record.actor, action: `${record.action} ${record.object}`, result: record.result })),
    permissions: { operator: deliveryPermissionCheck() },
  }
}

export function resolveDeliveryPreview(path: string, method: string, body?: unknown): DeliveryPreviewResult {
  const [route, query = ""] = path.split("?")
  const params = new URLSearchParams(query)
  const verb = method.toUpperCase()
  const site = params.get("site") || "site-pump"

  if (route === "/snapshot" && verb === "GET") return ok({
    user: { user_id: "u-wang", email: "王管理员", policy: { admin: true, role: "admin", actions: [] }, actions: [] },
    roots: ["root-project"], nodes: clone(nodes), session_timeout_seconds: 28800,
  })
  if (route === "/ems/state" && verb === "GET") return ok({ values: clone(runtimeValues), evaluated_at: "2026-11-24T10:32:02+08:00" })
  if (route === "/ems/quality" && verb === "GET") return ok({ channels: clone(runtimeChannels), devices: [] })
  if (route === "/ems/alarms" && verb === "GET") return ok({ alarms: [] })
  if (route === "/ems/channels" && verb === "GET") return ok({ channels: nodes.filter(node => node.type === "modbus").map(node => ({ id: node.id, protocol: pointText(node, "protocol") || "TCP", address: pointText(node, "uri"), devices: [] })) })
  if (route === "/industry/packages" && verb === "GET") return ok({ packages: industryPackages() })
  if (route === "/industry/packages" && verb === "POST") { industryInstalled = true; return ok({ id: "ems", version: "1.0.0", installed: true }) }
  if (route === "/industry/packages/ems" && verb === "DELETE") { industryInstalled = false; return ok({ id: "ems", installed: false }) }
  if (route === "/delivery/gates" && verb === "GET") return ok(gates(site))
  if (route === "/delivery/preflight" && verb === "GET") return ok({ latest: latestPreflight[site] ? clone(latestPreflight[site]) : null })
  if (route === "/delivery/preflight" && verb === "POST") {
    const report = { ...gates(site), id: `pf-preview-${preflightSequence++}`, generated_at: "2026-11-24T10:30:00+08:00", operator: "王管理员" }
    latestPreflight[site] = report
    return ok(clone(report))
  }
  if (route === "/projects/versions" && verb === "GET") return ok({ versions: clone(versions.filter(version => version.site_id === site)) })
  if (route === "/projects/publish" && verb === "POST") {
    if (deliveryPreviewState.preflightFail) return fail(409, "preflight_failed")
    const input = (body || {}) as { site?: string; note?: string; confirm?: boolean }
    const target = input.site || "site-pump"
    const version: PreviewVersion = {
      id: `v2026.11.24-${publishSequence++}`, site_id: target, site_name: target === "site-energy" ? "储能站 A" : "教学泵房",
      created_at: "2026-11-24T10:28:00+08:00", operator: "王管理员", note: input.note?.trim() || "", preflight_status: "pass", nodes: target === "site-pump" ? 14 : 4,
    }
    versions.unshift(version)
    deliveryPreviewState.publishedSites[target] = true
    deliveryPreviewState.importStep = "idle"
    deliveryPreviewState.importResult = null
    deliveryPreviewState.importStatus = "未导入"
    auditRecords.unshift({ time: version.created_at, actor: version.operator, action: "项目发布", object: version.id, detail: `${version.nodes} 个节点${version.note ? ` · ${version.note}` : ""}`, result: "success" })
    return ok(clone(version))
  }
  if (route === "/projects/export" && verb === "GET") return ok(clone({ ...projectPackage, source_version: versions.find(version => version.site_id === site)?.id, name: pointText(nodes.find(node => node.id === site), "description") || site, dependencies: projectPackage.dependencies.filter(item => item.kind !== "industry" || industryInstalled) }))
  if (route === "/projects/import" && verb === "POST") return importPackage(body)
  if (route === "/projects/rollback" && verb === "POST") {
    const input = (body || {}) as { site?: string; version?: string }
      const rollbackVersion = versions.find(version => version.site_id === (input.site || "site-pump") && version.id === input.version)
    if (!rollbackVersion) return fail(404, "version_not_found")
    versions = [rollbackVersion, ...versions.filter(version => version !== rollbackVersion)]
    auditRecords.unshift({ time: "2026-11-24T10:34:00+08:00", actor: "王管理员", action: "项目回退", object: rollbackVersion.id, detail: "只回工程配置；历史与命令保留", result: "success" })
    return ok({ version: rollbackVersion.id, baseline: `pv-preview-baseline-${rollbackVersion.id}`, updated: 11, created: 1, extra: 1, preflight_status: "pass" })
  }
  if (route === "/users" && verb === "GET") return ok({ users: clone(users), actions: ["configure", "write_value", "command", "rule_publish", "alarm_ack", "alarm_maintain", "delete", "export", "account_manage", "audit_view", "site_settings", "virtual_input_apply", "virtual_input_write", "calculation_apply"] })
  if (route === "/users" && verb === "POST") {
    const input = (body || {}) as { parent?: string; email?: string; role?: string; actions?: string[] }
    const user: PreviewUser = { id: `u-preview-${users.length + 1}`, email: input.email?.trim() || "新账号", policy: { admin: input.role === "admin", role: input.role || "viewer", root_node_ids: [input.parent || "root-project"], actions: [...(input.actions || [])] } }
    users.push(user)
    return ok({ id: user.id })
  }
  const userMatch = route.match(/^\/users\/([^/]+)$/)
  if (userMatch && verb === "PUT") {
    const user = users.find(item => item.id === decodeURIComponent(userMatch[1]))
    if (!user) return fail(404, "user_not_found")
    const input = (body || {}) as { role?: string; actions?: string[] }
    user.policy.role = input.role || user.policy.role
    user.policy.admin = user.policy.role === "admin"
    user.policy.actions = [...(input.actions || [])]
    return ok({ saved: true })
  }
  if (route === "/audit" && verb === "GET") {
    const action = params.get("action") || ""
    const result = params.get("result") || ""
    const records = auditRecords.filter(record => (!action || record.action === action) && (!result || record.result === result)).slice(0, 200)
    return ok({ records: clone(records), actions: ["启动设备", "确认报警", "项目发布", "项目导入", "项目回退", "修改点位"] })
  }
  if (route === "/delivery/preview/rebind" && verb === "POST") {
    if (deliveryPreviewState.importStep !== "imported") return fail(409, "import_required")
    return rebind("channel-imported", { protocol: "TCP", uri: "127.0.0.1:2502" })
  }
  if (route === "/delivery/preview/run" && verb === "POST") {
    if (deliveryPreviewState.importStep !== "rebound") return fail(409, "rebind_required")
    if (deliveryPreviewState.preflightFail) return fail(409, "preflight_failed")
    if (deliveryPreviewState.dependencyMismatch) return fail(409, "incompatible_dependency:industry:ems:required@0.9.0:available@1.0.0")
    deliveryPreviewState.importStep = "running"
    deliveryPreviewState.importStatus = "新实例运行中（原型）；采集恢复，未重放旧命令"
    runtimeValues = [...runtimeValues, { point_id: "point-imported", device_id: "channel-imported/1", value: { kind: "number", number: 0.42 }, quality: "good", source_time: new Date().toISOString() }]
    runtimeChannels = [...runtimeChannels, { channel_id: "channel-imported", health: "healthy", polls: 1, failures: 0, last_success_at: new Date().toISOString() }]
    latestPreflight["site-imported"] = { ...gates("site-imported"), id: "pf-imported-run", generated_at: new Date().toISOString(), operator: "王管理员" }
    return ok({ step: "running" })
  }
  if (route === "/delivery/evidence" && verb === "GET") return ok(evidence(site))
  const modbusMatch = route.match(/^\/modbus\/([^/]+)$/)
  if (modbusMatch && verb === "PUT") return rebind(decodeURIComponent(modbusMatch[1]), body)
  return { handled: false }
}
