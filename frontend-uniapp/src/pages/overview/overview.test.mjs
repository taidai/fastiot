import test from "node:test"
import assert from "node:assert/strict"
import {
  boundDevicesForLayer,
  descendantNodeIds,
  displaySample,
  formatPercent,
  healthLabel,
  isBusinessLayer,
  metricForPoint,
  qualityLabel,
  summarizeSuccessRate,
  valueInLayer,
  alarmInLayer,
} from "./overview.ts"

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
