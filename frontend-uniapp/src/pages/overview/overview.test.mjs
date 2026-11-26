import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import {
  boundDevicesForLayer,
  buildHourlyTrend,
  buildRecentTable,
  commandStateText,
  descendantNodeIds,
  deviceStatus,
  displaySample,
  formatPercent,
  healthLabel,
  historyOpenQuery,
  isBusinessLayer,
  isTerminalCommandState,
  metricForPoint,
  overviewBarPoint,
  overviewScreenDocument,
  preferredHistoryPoint,
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

test("overview consumes history=1 in place and resolves a layer point", () => {
  assert.deepEqual(historyOpenQuery(undefined), { open: false, point: "" })
  assert.deepEqual(historyOpenQuery({ history: "0" }), { open: false, point: "" })
  assert.deepEqual(historyOpenQuery({ history: "1" }), { open: true, point: "" })
  assert.deepEqual(historyOpenQuery({ history: "1", point: "p2" }), { open: true, point: "p2" })
  assert.equal(preferredHistoryPoint("", [{ id: "p1" }, { id: "p2" }]), "p1")
  assert.equal(preferredHistoryPoint("p2", [{ id: "p1" }, { id: "p2" }]), "p2")
  assert.equal(preferredHistoryPoint("missing", [{ id: "p1" }]), "p1")
  assert.equal(preferredHistoryPoint("", []), "")
})

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

test("device status composes communication with running feedback and never guesses", () => {
  assert.equal(deviceStatus("offline", true), "离线", "offline communication wins over stale feedback")
  assert.equal(deviceStatus("healthy", true), "在线")
  assert.equal(deviceStatus("healthy", false), "停止")
  assert.equal(deviceStatus("degraded", true), "在线")
  assert.equal(deviceStatus("healthy"), "在线", "no feedback falls back to the communication state")
  assert.equal(deviceStatus("unknown"), "—")
  assert.equal(deviceStatus(undefined), "—")
})

test("recent data aligns points by observation time without interpolating", () => {
  const rows = buildRecentTable([
    { id: "pressure", unit: "MPa", records: [
      { point_id: "pressure", source_time: "2026-11-22T21:34:10Z", value: 0.42, quality: "good", source: "modbus_runtime" },
      { point_id: "pressure", source_time: "2026-11-22T21:34:05Z", value: 0.41, quality: "good", source: "modbus_runtime" },
    ] },
    { id: "level", unit: "%", records: [
      { point_id: "level", source_time: "2026-11-22T21:34:10Z", value: 68, quality: "good", source: "modbus_runtime" },
      { point_id: "level", source_time: "2026-11-22T21:34:07Z", value: 69, quality: "good", source: "modbus_runtime" },
      { point_id: "level", source_time: "2026-11-22T21:34:05Z", quality: "stale", kind: "quality_event", source: "modbus_runtime" },
    ] },
  ], 5)
  assert.equal(rows.length, 3, "union of real observation times, newest first")
  assert.equal(rows[0].cells[0], "0.42")
  assert.equal(rows[0].cells[1], "68")
  assert.equal(rows[1].cells[0], null, "a missing point at that time stays empty")
  assert.equal(rows[1].cells[1], "69")
  assert.equal(rows[2].cells[0], "0.41")
  assert.equal(rows[2].cells[1], null, "quality events are not values")
  assert.equal(buildRecentTable([], 5).length, 0)
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

const screenDocument = {
  layer: "room", version: 0, title: "泵房", columns: 2, point_ids: [],
  slots: { top: ["status", "kpi"], main: ["flow", "devices", "recent"], side: ["alarm", "today", "trend", "summary"] },
  blocks: [
    { type: "status", binding: {}, span: 2 },
    { type: "kpi", binding: { metrics: [{ point_id: "level", label: "水箱液位" }, { point_id: "pressure", label: "出口压力" }] }, span: 2 },
    { type: "flow", binding: {}, span: 2 },
    { type: "devices", binding: {}, span: 2 },
    { type: "recent", binding: {}, span: 2 },
    { type: "alarm", binding: {}, span: 2 },
    { type: "today", binding: {}, span: 2 },
    { type: "trend", binding: { point_id: "level" }, span: 2 },
    { type: "summary", binding: {}, span: 2 },
  ],
}

test("overview keeps metrics in the hero and only the primary visual in the stage", () => {
  const document = overviewScreenDocument(screenDocument)
  assert.deepEqual(document.slots, { top: ["kpi"], main: ["flow", "devices", "recent"], side: [] })
  assert.deepEqual(document.blocks.map(block => block.type), ["kpi", "flow", "devices", "recent"])
  const withoutKPI = { ...screenDocument, blocks: screenDocument.blocks.filter(block => block.type !== "kpi") }
  assert.deepEqual(overviewScreenDocument(withoutKPI).slots.top, [])
})

test("overview bar strip follows outlet pressure before the configured trend", () => {
  const options = [{ id: "level", label: "水箱液位", unit: "m" }, { id: "pressure", label: "出口压力", unit: "MPa" }]
  assert.equal(overviewBarPoint(screenDocument, options).id, "pressure")
  const levelOnly = { ...screenDocument, blocks: screenDocument.blocks.map(block => block.type === "kpi" ? { ...block, binding: { metrics: [{ point_id: "level", label: "水箱液位" }] } } : block) }
  assert.equal(overviewBarPoint(levelOnly, options).id, "level")
  assert.equal(overviewBarPoint({ ...screenDocument, blocks: [] }, options).id, "level")
  assert.equal(overviewBarPoint(screenDocument, []), null)
})

const liveSource = readFileSync(new URL("./LiveOverview.vue", import.meta.url), "utf8")
const rendererSource = readFileSync(new URL("../../components/design/ScreenRenderer.vue", import.meta.url), "utf8")

test("overview renders hero, primary visual, daily strip and colophon in that order", () => {
  assert.match(liveSource, /grid-template-columns:200px minmax\(0,1fr\) 320px/)
  assert.ok(liveSource.indexOf("<ScreenRenderer") < liveSource.indexOf('class="daily'))
  assert.ok(liveSource.indexOf('class="daily') < liveSource.indexOf('class="colophon'))
  assert.ok(rendererSource.indexOf('class="hero') < rendererSource.indexOf('class="screen-body'))
})

test("overview rail renders pending, today execution and handover in that order", () => {
  const pending = liveSource.indexOf("待处理")
  const today = liveSource.indexOf("<TodayExecutionSummary")
  const handover = liveSource.indexOf("<HandoverPanel")
  assert.ok(pending > -1 && pending < today && today < handover)
})
