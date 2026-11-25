import test from "node:test"
import assert from "node:assert/strict"
import {
  boundDevicesForLayer,
  buildHourlyTrend,
  commandStateText,
  descendantNodeIds,
  displaySample,
  formatPercent,
  healthLabel,
  isBusinessLayer,
  isTerminalCommandState,
  metricForPoint,
  qualityLabel,
  screenMetricTarget,
  summarizeSuccessRate,
  valueInLayer,
  alarmInLayer,
  unitPointsForServer,
  writableUnitPoints,
} from "./overview.ts"
import { DEFAULT_APPEARANCE, normalizeAppearance } from "./appearance.ts"

test('rejected commands are terminal and not failed physical operations', () => {
  assert.equal(commandStateText('rejected'), '已拒绝')
  assert.equal(isTerminalCommandState('rejected'), true)
})

const snapshot = {
  roots: ["site"],
  nodes: [
    { id: "site", type: "site", parent: "", points: [{ type: "description", key: "", time: "", text: "一号现场" }] },
    { id: "room", type: "group", parent: "site", points: [{ type: "description", key: "", time: "", text: "泵房" }] },
    { id: "pressure", type: "modbusIo", parent: "room", points: [
      { type: "description", key: "", time: "", text: "出口压力" },
      { type: "units", key: "", time: "", text: "MPa" },
    ] },
    { id: "run", type: "modbusIo", parent: "room", points: [{ type: "description", key: "", time: "", text: "1#泵运行反馈" }] },
    { id: "unnamed", type: "modbusIo", parent: "room", points: [] },
    { id: "table", type: "decisionTable", parent: "room", points: [] },
  ],
}
const values = [
  { device_id: "channel/1", point_id: "pressure", value: { kind: "number", number: 0 }, quality: "good" },
  { device_id: "channel/1", point_id: "run", value: { kind: "bool", bool: true }, quality: "stale" },
]

test("only business containers are ledger layers, not points or decision tables", () => {
  assert.equal(isBusinessLayer(snapshot.nodes[1]), true)
  assert.equal(isBusinessLayer(snapshot.nodes[2]), false)
  assert.equal(isBusinessLayer(snapshot.nodes[5]), false)
})

test("device scope includes descendants and excludes unbound devices", () => {
  const devices = [
    { channel_id: "channel", server_id: 1, device_id: "channel/1", name: "1号泵", business_node_id: "room" },
    { channel_id: "channel", server_id: 2, device_id: "channel/2", name: "2号泵", business_node_id: "other" },
    { channel_id: "channel", server_id: 3, device_id: "channel/3", name: "未绑定", business_node_id: "" },
  ]
  assert.deepEqual(boundDevicesForLayer("site", snapshot.nodes, devices).map(item => item.name), ["1号泵"])
  assert.deepEqual(boundDevicesForLayer("room", snapshot.nodes, devices).map(item => item.name), ["1号泵"])
})

test("metrics use business names, units and real zero instead of raw point ids", () => {
  assert.deepEqual(metricForPoint("pressure", snapshot, values), {
    id: "pressure", label: "出口压力", unit: "MPa", value: "0", quality: "有效", time: "—",
  })
  assert.equal(metricForPoint("unnamed", snapshot, []).label, "未命名点位")
  assert.equal(metricForPoint("missing", snapshot, []).value, "—")
  assert.equal(metricForPoint("missing", snapshot, []).label, "未命名点位")
})

test("layer values exclude other runtime devices but retain aggregate nodes", () => {
  const nodes = descendantNodeIds("room", snapshot.nodes)
  assert.deepEqual([...nodes].sort(), ["pressure", "room", "run", "table", "unnamed"].sort())
  const devices = new Set(["channel/1"])
  const knownDevices = new Set(["channel/1", "channel/2"])
  assert.equal(valueInLayer(values[0], nodes, devices, knownDevices), true)
  assert.equal(valueInLayer({ ...values[0], device_id: "channel/2" }, nodes, devices, knownDevices), false)
  assert.equal(valueInLayer({ ...values[0], point_id: "aggregate", device_id: "aggregate" }, nodes, devices, knownDevices), false)
  assert.equal(valueInLayer({ ...values[0], point_id: "room", device_id: "room" }, nodes, devices, knownDevices), true)
})

test("layer alarms use the full subtree, not only six rendered metrics", () => {
  const nodes = new Set(["room", "pressure", "other-point"])
  const devices = new Set(["channel/1"])
  assert.equal(alarmInLayer({ device_id: "room" }, nodes, devices), true)
  assert.equal(alarmInLayer({ point_id: "other-point" }, nodes, devices), true)
  assert.equal(alarmInLayer({ device_id: "channel/1" }, nodes, devices), true)
  assert.equal(alarmInLayer({ device_id: "channel/2", point_id: "outside" }, nodes, devices), false)
})

test("boolean labels use the declared business semantics", () => {
  assert.equal(displaySample({ value: { kind: "number", number: 0.48000000000000004 } }), "0.48")
  assert.equal(displaySample(values[1], "1#泵运行反馈"), "运行")
  assert.equal(displaySample({ value: { kind: "bool", bool: false } }, "远程模式"), "就地")
  assert.equal(displaySample({ value: { kind: "bool", bool: true } }, "故障"), "故障")
  assert.equal(displaySample({ value: { kind: "bool", bool: false } }, "普通开关"), "否")
})

test("quality, health and percentages distinguish unavailable statistics", () => {
  assert.equal(qualityLabel("good"), "有效")
  assert.equal(qualityLabel("stale"), "过期")
  assert.equal(healthLabel("degraded"), "不稳定")
  assert.equal(formatPercent(0, 0), "—")
  assert.equal(formatPercent(99.25, 12), "99.3%")
  assert.equal(summarizeSuccessRate([{ window_5m: { polls: 8, failures: 2 } }, { window_5m: { polls: 2, failures: 0 } }]), "80.0%")
  assert.equal(summarizeSuccessRate([{ window_5m: { polls: 0, failures: 0 } }]), "—")
})

test("appearance reset restores the real light theme and 42 percent defaults", () => {
  assert.deepEqual(normalizeAppearance({ theme: "invalid", alpha: 500 }), { theme: "light", alpha: 85 })
  assert.deepEqual(normalizeAppearance({ theme: "dark", alpha: 65 }), { theme: "dark", alpha: 65 })
  assert.deepEqual(normalizeAppearance({ theme: "tint", alpha: 20 }), { theme: "tint", alpha: 20 })
  assert.deepEqual(normalizeAppearance(null), DEFAULT_APPEARANCE)
})

test("metric targets come only from the saved screen binding", () => {
  const screen = { blocks: [
    { type: "recent", binding: {} },
    { type: "kpi", binding: { metrics: [{ point_id: "pressure", target: " 0.45 MPa " }, { point_id: "run", target: "" }] } },
  ] }
  assert.equal(screenMetricTarget(screen, "pressure"), "0.45 MPa")
  assert.equal(screenMetricTarget(screen, "run"), "")
  assert.equal(screenMetricTarget(screen, "missing"), "")
  assert.equal(screenMetricTarget(null, "pressure"), "")
})

test("command states never label submitted or unknown as readback confirmed", () => {
  assert.equal(commandStateText("confirmed"), "已回读")
  assert.equal(commandStateText("submitted"), "已下发 · 无回读确认")
  assert.equal(commandStateText("unknown"), "结果未知")
  assert.equal(commandStateText("accepted"), "等待回读")
  assert.equal(isTerminalCommandState("executing"), false)
  assert.equal(isTerminalCommandState("submitted"), true)
  assert.equal(isTerminalCommandState("unknown"), true)
})

test("only writable Modbus outputs from the selected server appear as manual write targets", () => {
  const points = [
    { id: "run", name: "运行", server_id: 1, io_type: "modbusCoil", read_only: false },
    { id: "target", name: "目标", server_id: 1, io_type: "modbusHoldingRegister", read_only: false },
    { id: "pressure", name: "压力", server_id: 1, io_type: "modbusInputRegister", read_only: false },
    { id: "locked", name: "锁定", server_id: 1, io_type: "modbusHoldingRegister", read_only: true },
    { id: "other-unit", name: "其他站号", server_id: 2, io_type: "modbusHoldingRegister", read_only: false },
  ]
  assert.deepEqual(unitPointsForServer(points, 1).map(point => point.id), ["run", "target", "pressure", "locked"])
  assert.deepEqual(writableUnitPoints(unitPointsForServer(points, 1)).map(point => point.id), ["run", "target"])
})

test("24 hour trend keeps real hourly values and explicit missing buckets", () => {
  const start = new Date("2026-11-23T00:00:00Z")
  const end = new Date("2026-11-23T23:30:00Z")
  const records = [
    { point_id: "pressure", source_time: "2026-11-23T00:00:00Z", value: 0, quality: "good", source: "modbus_runtime" },
    { point_id: "pressure", source_time: "2026-11-23T02:00:00Z", value: 4, quality: "good", source: "modbus_runtime" },
    { point_id: "pressure", source_time: "2026-11-23T03:00:00Z", quality: "stale", kind: "quality_event", source: "modbus_runtime" },
  ]
  const trend = buildHourlyTrend(records, start, end)
  assert.equal(trend.buckets.length, 24)
  assert.equal(trend.buckets[0].value, 0)
  assert.equal(trend.buckets[0].height, 0)
  assert.equal(trend.buckets[1].record, undefined)
  assert.equal(trend.buckets[1].value, undefined)
  assert.equal(trend.buckets[2].height, 100)
  assert.equal(trend.buckets[3].value, undefined)
  assert.equal(trend.hasData, true)
  assert.equal(buildHourlyTrend([], start, end).hasData, false)
})
