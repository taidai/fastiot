import test from "node:test"
import assert from "node:assert/strict"
import { flattenLedger, screenSummary, summarizeLedger } from "./design.ts"

const snapshot = {
  roots: ["site-1"],
  nodes: [
    { id: "site-1", type: "site", parent: "", points: [{ type: "description", text: "教学泵房" }] },
    { id: "area-1", type: "area", parent: "site-1", points: [{ type: "description", text: "泵房" }] },
    { id: "device-1", type: "device", parent: "area-1", points: [{ type: "description", text: "1 号泵" }] },
    { id: "point-1", type: "variable", parent: "device-1", points: [{ type: "description", text: "出口压力" }] },
    { id: "rule-1", type: "decisionTable", parent: "site-1", points: [{ type: "description", text: "压力策略" }] },
  ],
}

test("flattenLedger keeps the real business hierarchy and excludes points and rules", () => {
  assert.deepEqual(flattenLedger(snapshot).map(row => [row.node.id, row.depth, row.kind]), [
    ["site-1", 0, "现场"],
    ["area-1", 1, "区域"],
    ["device-1", 2, "设备"],
  ])
})

test("flattenLedger supports the backend siteRoot and physical channels", () => {
  const actual = {
    roots: ["easyems"],
    nodes: [
      { id: "easyems", type: "siteRoot", parent: "root", points: [{ type: "description", text: "FastIOT" }] },
      { id: "channel-1", type: "modbus", parent: "easyems", points: [{ type: "description", text: "模拟温度" }] },
      { id: "point-1", type: "modbusIo", parent: "channel-1", points: [{ type: "description", text: "温度 A" }] },
      { id: "rule-1", type: "decisionTable", parent: "easyems", points: [] },
    ],
  }
  assert.deepEqual(flattenLedger(actual).map(row => [row.node.id, row.kind]), [["easyems", "现场"], ["channel-1", "通道"]])
})

test("summarizeLedger counts actual objects without treating points as devices", () => {
  assert.deepEqual(summarizeLedger(snapshot.nodes), { areas: 1, units: 0, devices: 1, rules: 1 })
})

test("screenSummary describes the simple backend screen or its absence", () => {
  assert.equal(screenSummary({ title: "教学画面", columns: 3, point_ids: ["p1", "p2"] }), "教学画面 · 3 列 · 2 个点位")
  assert.equal(screenSummary(null), "未配置")
})
