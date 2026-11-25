import test, { beforeEach } from "node:test"
import assert from "node:assert/strict"
import { resetDesignPreview, resolveDesignPreview, selectDesignPreviewProject } from "../../api/preview/design.ts"

beforeEach(() => resetDesignPreview(true))

test('default pump-house matches the final prototype with all three devices bound', () => {
  resetDesignPreview()
  assert.ok(modelOnSnapshot(snapshot(), 'pump-2'))
  assert.equal(call('/delivery/gates?site=site-pump').status, 'pass')
})

function call(path, method = "GET", body) {
  const result = resolveDesignPreview(path, method, body)
  assert.equal(result.handled, true, `${method} ${path} must be handled`)
  assert((result.status || 200) < 400, `${method} ${path} failed with ${result.status}: ${JSON.stringify(result.data)}`)
  return result.data
}

function snapshot() {
  return call("/snapshot")
}

function description(node) {
  return node.points.find(point => point.type === "description")?.text
}

function modelOnSnapshot(data, businessID) {
  return data.nodes.filter(node => node.type === "modbus").flatMap(node => node.points).find(point => {
    if (point.type !== "easyemsUnitModel" || !point.text) return false
    return JSON.parse(point.text).business_node_id === businessID
  })
}

test('project switching keeps edits scoped and supplies energy and batch ledgers', () => {
  const created = call('/nodes', 'POST', { parent: 'site-pump', type: 'area', description: '新增区域' })
  selectDesignPreviewProject('site-energy', '储能站 A')
  assert.deepEqual(snapshot().roots, ['site-energy'])
  assert.equal(snapshot().nodes.filter(node => node.type === 'device').length, 6)
  assert.equal(snapshot().nodes.some(node => node.id === created.id), false)
  assert.ok(call('/ems/profiles').profiles.some(p => p.id === 'pcs21'))
  selectDesignPreviewProject('site-farm', '批量接入示例')
  assert.equal(snapshot().nodes.filter(node => node.type === 'energyUnit').length, 12)
  assert.equal(snapshot().nodes.filter(node => node.type === 'device').length, 24)
  selectDesignPreviewProject('site-pump', '教学泵房')
  assert.equal(snapshot().nodes.some(node => node.id === created.id), true)
  selectDesignPreviewProject('site-new-3', '未命名项目')
  assert.equal(snapshot().nodes.length, 1)
})

test("resolver covers every read contract used by the six design steps", () => {
  const requests = [
    ["/snapshot", "user"], ["/templates", "site_templates"], ["/ems/state", "values"], ["/ems/quality", "devices"],
    ["/ems/alarms", "alarms"], ["/device-ledger", "devices"], ["/ems/channels", "channels"], ["/ems/profiles", "profiles"],
    ["/modbus/channel-pump/units/1/model", "points"], ["/modbus/channel-pump/units/1/verify", "records"],
    ["/state-machines", "state_machines"], ["/aggregate?group=site-pump", "config"], ["/calculations", "calculations"],
    ["/virtual-inputs", "inputs"], ["/ems/screen/site-pump", "blocks"], ["/projects/export?site=site-pump", "nodes"],
    ["/projects/versions?site=site-pump", "versions"], ["/delivery/gates?site=site-pump", "gates"], ["/overview/handover?layer=site-pump", "handover"],
    ["/industry/packages", "packages"],
  ]
  for (const [path, key] of requests) assert(Object.prototype.hasOwnProperty.call(call(path), key), `${path} returns ${key}`)
  const data = snapshot()
  assert.deepEqual(data.nodes.filter(node => node.type === "device").map(description), ["1号泵", "2号泵", "水箱"])
  assert.equal(modelOnSnapshot(data, "pump-2"), undefined, "pump 2 starts unbound for the batch-access walkthrough")
  assert.ok(modelOnSnapshot(data, "pump-1"), "pump 1 has a persisted binding")
  assert.equal(call("/ems/screen/area-pump").inherited_from, "site-pump")
  assert.deepEqual(resolveDesignPreview("/not-a-design-api", "GET"), { handled: false })
})

test("site creation, node creation, subtree copy, and member binding mutate the shared ledger", () => {
  const createdSite = call("/nodes", "POST", { parent: "site-pump", description: "泵房示例", site_template: "pump-house" })
  assert.equal(createdSite.created, 7, "site, five template objects, and a default decision table")
  let data = snapshot()
  assert.equal(description(data.nodes.find(node => node.id === createdSite.id)), "泵房示例")
  assert.equal(data.nodes.filter(node => node.parent === createdSite.id).length, 3, "area, unit, and default rule")

  const createdDevice = call("/nodes", "POST", { parent: "unit-supply", type: "device", description: "备用泵" })
  data = snapshot()
  assert.equal(data.nodes.find(node => node.id === createdDevice.id).parent, "unit-supply")

  const copied = call("/nodes/pump-1/copy", "POST", { parent: "area-pump", description: "1号泵 副本" })
  data = snapshot()
  const copy = data.nodes.find(node => node.id === copied.id)
  assert.equal(description(copy), "1号泵 副本")
  assert.equal(copy.parent, "area-pump")
  assert.ok(copied.created > 1, "semantic points are copied with the device")
  assert.equal(modelOnSnapshot(data, copied.id), undefined, "physical bindings are never duplicated")

  const memberResult = call("/nodes/unit-supply", "PATCH", { description: "供水单元", members: [{ role: "备用泵", node_id: createdDevice.id, required: true }] })
  assert.deepEqual(memberResult.missing_required, [])
  const unit = snapshot().nodes.find(node => node.id === "unit-supply")
  assert.equal(JSON.parse(unit.points.find(point => point.type === "easyemsUnitMembers").text)[0].node_id, createdDevice.id)
})

test("batch dry-run is side-effect free and apply binds pump 2 across snapshot, model, runtime, and quality", () => {
  const csv = "设备,型号,通道,站号\npump-2,centrifugal-pump-v1.3,channel-pump,2"
  const preview = call("/access/batch", "POST", { csv, dry_run: true })
  assert.equal(preview.applied, 0)
  assert.equal(preview.rows[0].status, "ready")
  assert.equal(modelOnSnapshot(snapshot(), "pump-2"), undefined)

  const applied = call("/access/batch", "POST", { csv, dry_run: false })
  assert.equal(applied.applied, 1)
  assert.equal(applied.rows[0].status, "mapped")
  assert.ok(applied.rows[0].mapped >= 12)
  assert.equal(JSON.parse(modelOnSnapshot(snapshot(), "pump-2").text).profile_id, "centrifugal-pump-v1.3")
  assert.equal(call("/modbus/channel-pump/units/2/model").model.business_node_id, "pump-2")
  assert.equal(call("/device-ledger").devices.find(device => device.business_node_id === "pump-2").health, "healthy")
  assert(call("/ems/state").values.filter(value => value.device_id === "pump-2").every(value => value.quality === "good"))
  assert.equal(call("/delivery/gates?site=site-pump").gates.find(gate => gate.key === "ledger").status, "pass")
})

test("modbus channel, point, Profile, mapping, and verification writes remain consistent", () => {
  const channel = call("/modbus", "POST", { parent: "site-pump", description: "备用 TCP 通道", protocol: "TCP", uri: "127.0.0.1:1502", poll_period: 500, timeout: 800 })
  assert(call("/ems/channels").channels.some(item => item.id === channel.id && item.address === "127.0.0.1:1502"))
  call(`/modbus/${channel.id}`, "PUT", { description: "备用通道", protocol: "TCP", uri: "127.0.0.1:1503", poll_period: 1000 })
  assert.equal(call("/ems/channels").channels.find(item => item.id === channel.id).address, "127.0.0.1:1503")

  const physical = call(`/modbus/${channel.id}/io`, "POST", { description: "出口压力", server_id: 8, address: 40101, io_type: "modbusInputRegister", data_format: "float32", read_only: true, scale: 0.01, units: "MPa" })
  const preview = call(`/modbus/${channel.id}/units/apply`, "POST", { profile_id: "centrifugal-pump-v1.3", servers: [8], dry_run: true })
  assert.equal(preview.applied, 0)
  assert.equal(preview.results[0].points.find(point => point.name === "出口压力").point_id, physical.id)
  assert.equal(call(`/modbus/${channel.id}/units/8/model`).model, undefined)

  call(`/modbus/${channel.id}/units/apply`, "POST", { profile_id: "centrifugal-pump-v1.3", servers: [8], dry_run: false })
  const saved = call(`/modbus/${channel.id}/units/8/model`, "PUT", { name: "教学备用泵", profile_id: "centrifugal-pump-v1.3", business_node_id: "pump-2", point_map: { "出口压力": physical.id }, verified: true, verifier: "张工", verified_at: "2026-11-23", device_serial: "SN-8", verification: "已核对" })
  assert.equal(saved.model.verified, true)
  assert.equal(saved.model.point_map["出口压力"], physical.id)
  const l1 = call(`/modbus/${channel.id}/units/8/verify`, "POST", { level: "L1" })
  assert.equal(l1.line.connected, true)
  const l2 = call(`/modbus/${channel.id}/units/8/verify`, "POST", { level: "L2" })
  assert.equal(l2.points[0].passed, true)
  const l3 = call(`/modbus/${channel.id}/units/8/verify`, "POST", { level: "L3", command_id: "set-pressure", confirm: true })
  assert.equal(l3.command.state, "confirmed")
  assert.equal(call(`/modbus/${channel.id}/units/8/verify`).records.length, 3)
})

test("rule simulation honors first match and hold, while draft, publish, and enable update the snapshot", () => {
  const document = JSON.parse(snapshot().nodes.find(node => node.id === "rule-pump").points.find(point => point.type === "draft").text)
  const waiting = call("/rules/rule-pump/test", "POST", { document: { ...document, simulation: { held_ms: 1000 } }, inputs: { valid: 1, level: 68, pressure: 0.3 }, text_inputs: {} })
  assert.equal(waiting.row_id, "low-pressure")
  assert.equal(waiting.state, "waiting-hold")
  const fired = call("/rules/rule-pump/test", "POST", { document: { ...document, simulation: { held_ms: 30000 } }, inputs: { valid: 1, level: 68, pressure: 0.3 }, text_inputs: {} })
  assert.equal(fired.state, "fired")
  assert.equal(fired.actions[0].action_id, "pump_1")

  document.table.rows[0].conditions[1].value = 5
  call("/rules/rule-pump/draft", "PUT", document)
  let rule = snapshot().nodes.find(node => node.id === "rule-pump")
  assert.equal(JSON.parse(rule.points.find(point => point.type === "draft").text).table.rows[0].conditions[1].value, 5)
  assert.notEqual(rule.points.find(point => point.type === "applied").text, rule.points.find(point => point.type === "draft").text)
  call("/rules/rule-pump/publish", "POST", document)
  call("/rules/rule-pump/enable", "POST", { enabled: false })
  rule = snapshot().nodes.find(node => node.id === "rule-pump")
  assert.equal(rule.points.find(point => point.type === "applied").text, rule.points.find(point => point.type === "draft").text)
  assert.equal(rule.points.find(point => point.type === "enabled").value, 0)
})

test("state machine create, draft, publish, simulation, and confirmed execution preserve revisions and state", () => {
  const initial = call("/state-machines")
  assert.equal(initial.state_machines[0].current_state, "停止")
  const created = call("/state-machines", "POST", { parent: "area-pump", name: "备用状态机", definition: { initial_state: "idle", scope: "self", states: ["idle"], transitions: [], bindings: { points: {}, actions: {} } } })
  assert.equal(created.version, 0)
  const definition = { ...created.draft, states: ["idle", "running"], transitions: [{ transition_id: "go", from: "idle", event: "start", to: "running", conditions: [], match: "all", actions: [] }] }
  const drafted = call(`/state-machines/${created.id}/draft`, "POST", { definition, revision: created.revision })
  assert.equal(drafted.revision, 2)
  const published = call(`/state-machines/${created.id}/publish`, "POST", { revision: drafted.revision })
  assert.equal(published.version, 1)
  const simulated = call(`/state-machines/${created.id}/simulate`, "POST", { event: "start", definition, inputs: {}, text_inputs: {} })
  assert.equal(simulated.simulation.status, "matched")
  const executed = call(`/state-machines/${created.id}/execute`, "POST", { event: "start", revision: published.revision, confirm: true })
  assert.equal(executed.current_state, "running")
  assert.equal(executed.last_execution.status, "confirmed")
})

test("aggregate, calculations, and virtual inputs expose derived values and saved configuration", () => {
  const initial = call("/aggregate?group=site-pump")
  assert.equal(initial.configured, true)
  assert.equal(initial.value, 12.4)
  assert.equal(initial.partial, true, "unbound pump 2 does not contribute a zero as valid data")
  const saved = call("/aggregate", "PUT", { group: "site-pump", op: "avg", members: ["pressure-p1", "level-tank"], units: "", window: "5m", quality: "ignore" })
  assert.equal(saved.value, 34.21)
  assert.equal(saved.window.members, 2)
  assert.equal(call("/aggregate?group=site-pump").config.op, "avg")
  assert.equal(call("/calculations").calculations[0].state.unit, "kW")
  assert.equal(call("/virtual-inputs").inputs[0].source, "configuration")
})

test("screen saves increment versions, create child configuration, and apply to same-kind layers", () => {
  const area = call("/ems/screen/area-pump")
  assert.equal(area.inherited_from, "site-pump")
  const draft = { ...area, layer: "area-pump", version: 0 }
  delete draft.inherited_from
  const saved = call("/ems/screen/area-pump", "PUT", { ...draft, apply_to_same: false })
  assert.equal(saved.saved, true)
  assert.equal(saved.version, 1)
  const loaded = call("/ems/screen/area-pump")
  assert.equal(loaded.layer, "area-pump")
  assert.equal(loaded.inherited_from, undefined)

  const same = call("/ems/screen/area-pump", "PUT", { ...loaded, apply_to_same: true })
  assert(same.applied >= 1)
  const conflict = resolveDesignPreview("/ems/screen/area-pump", "PUT", { ...loaded, apply_to_same: false })
  assert.equal(conflict.status, 409)
  assert.equal(conflict.data.error, "version_conflict")
})

test("preflight, publish, package, alarm, industry, and handover interactions are stateful", () => {
  assert.equal(call("/delivery/gates?site=site-pump").status, "fail")
  const blocked = resolveDesignPreview("/projects/publish", "POST", { site: "site-pump", note: "", confirm: false })
  assert.equal(blocked.status, 409)
  assert.equal(blocked.data.error, "preflight_failed")
  const report = call("/delivery/preflight?site=site-pump", "POST")
  assert.equal(report.site, "site-pump")
  assert.equal(call("/delivery/preflight?site=site-pump").latest.id, report.id)
  const version = call("/projects/publish", "POST", { site: "site-pump", note: "验收版", confirm: true })
  assert.equal(version.preflight_status, "fail")
  assert.equal(call("/projects/versions?site=site-pump").versions[0].id, version.id)
  const project = call("/projects/export?site=site-pump")
  assert.equal(project.format, "fastiot.project-package")
  assert(project.nodes.every(node => node.points.every(point => point.type !== "uri" && point.type !== "value")))

  call("/ems/alarms/alarm-pressure/ack", "POST", { note: "已接手" })
  assert.equal(call("/ems/alarms").alarms.find(alarm => alarm.id === "alarm-pressure").state, "acknowledged")
  call("/ems/alarms/alarm-pressure/close", "POST", {})
  assert.equal(call("/ems/alarms").alarms.find(alarm => alarm.id === "alarm-pressure").active, false)
  call("/industry/packages/ems", "DELETE")
  assert.equal(call("/industry/packages").packages[0].installed, false)
  call("/industry/packages", "POST", { id: "ems", version: "1.0.0" })
  assert.equal(call("/industry/packages").packages[0].installed, true)
  call("/overview/handover?layer=area-pump", "PUT", { note: "  夜间注意水箱液位  " })
  assert.equal(call("/overview/handover?layer=area-pump").handover.note, "夜间注意水箱液位")
  assert.equal(call("/overview/handover?layer=site-pump").handover.note.includes("2号泵"), true, "other layers remain isolated")
})
