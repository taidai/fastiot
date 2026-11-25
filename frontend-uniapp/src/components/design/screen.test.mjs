import test from "node:test"
import assert from "node:assert/strict"
import { addScreenBlock, defaultScreen, makeScreenDraft, moveScreenBlock, removeScreenBlock, setScreenMetric, toScreenPayload } from "./screen.ts"

const points = [{ id: "power", label: "总功率" }, { id: "pressure", label: "出口压力" }]

test("defaultScreen creates six declarative KPI bindings in fixed slots", () => {
  const draft = defaultScreen("area-1", "泵房", "区域", points)
  assert.equal(draft.layer, "area-1")
  assert.equal(draft.version, 0)
  assert.deepEqual(draft.slots.top, ["status", "kpi"])
  assert.deepEqual(draft.slots.main, ["children", "devices", "recent"])
  assert.equal(draft.blocks.find(block => block.type === "kpi").binding.metrics.length, 6)
  assert.deepEqual(draft.point_ids, ["power", "pressure"])
  assert.equal(draft.blocks.find(block => block.type === "recent").binding.rows, 5)
})

test("makeScreenDraft converts simple screens without losing their point bindings", () => {
  const draft = makeScreenDraft({ title: "旧画面", columns: 4, point_ids: ["pressure"] }, "area-1", "泵房", "区域", points)
  assert.equal(draft.version, 0)
  assert.equal(draft.columns, 2)
  assert.equal(draft.blocks.find(block => block.type === "kpi").binding.metrics[0].point_id, "pressure")
  assert.deepEqual(draft.point_ids, ["pressure"])
})

test("makeScreenDraft saves an inherited document as a new own version", () => {
  const inherited = { ...defaultScreen("area-parent", "父区域", "区域", points), version: 3, inherited_from: "area-parent" }
  const draft = makeScreenDraft(inherited, "area-child", "子区域", "区域", points)
  assert.equal(draft.layer, "area-child")
  assert.equal(draft.version, 0)
  assert.equal(draft.inherited_from, undefined)
  assert.equal(draft.blocks.length, inherited.blocks.length)
})

test("inherited point references remain explicit and cannot silently become other metrics", () => {
  const inherited = { ...defaultScreen("area-parent", "父区域", "区域", points), version: 3, inherited_from: "area-parent" }
  const draft = makeScreenDraft(inherited, "area-child", "子区域", "区域", [])
  assert.deepEqual(draft.point_ids, ["power", "pressure"])
  assert.deepEqual(draft.blocks.find(block => block.type === "recent").binding.point_ids, ["power", "pressure"])
  assert.equal(draft.blocks.find(block => block.type === "trend").binding.point_id, "power")
  assert.equal(draft.blocks.find(block => block.type === "kpi").binding.metrics[0].point_id, "power")
  assert.equal(inherited.layer, "area-parent", "editing never mutates saved parent")
})

test("moveScreenBlock reorders only inside its slot", () => {
  const draft = defaultScreen("area-1", "泵房", "区域", points)
  moveScreenBlock(draft, "recent", -1)
  assert.deepEqual(draft.slots.main, ["children", "recent", "devices"])
  moveScreenBlock(draft, "kpi", 1)
  assert.deepEqual(draft.slots.top, ["status", "kpi"], "last block cannot leave its slot")
  assert.equal(draft.slots.side.includes("recent"), false)
})

test("catalog additions are kind-aware and removable", () => {
  const draft = defaultScreen("device-1", "1 号泵", "设备", points)
  assert.equal(addScreenBlock(draft, "flow", "设备", points), false, "site-only flow cannot be added to a device")
  removeScreenBlock(draft, "oplog")
  assert.equal(addScreenBlock(draft, "oplog", "设备", points), true)
  assert.equal(draft.slots.side.at(-1), "oplog")
  assert.equal(addScreenBlock(draft, "oplog", "设备", points), false, "a block cannot be duplicated")
  removeScreenBlock(draft, "oplog")
  assert.equal(draft.blocks.some(block => block.type === "oplog"), false)
  assert.equal(draft.slots.side.includes("oplog"), false)
})

test("setScreenMetric switches between point and automatic concepts", () => {
  const draft = defaultScreen("area-1", "泵房", "区域", points)
  setScreenMetric(draft, 0, "auto:active_alarms", points)
  assert.deepEqual(draft.blocks.find(block => block.type === "kpi").binding.metrics[0], { metric: "active_alarms", label: "活动告警", target: "" })
  setScreenMetric(draft, 0, "point:pressure", points)
  assert.equal(draft.blocks.find(block => block.type === "kpi").binding.metrics[0].point_id, "pressure")
  assert.deepEqual(draft.point_ids, ["pressure"])
})

test("toScreenPayload emits the versioned ADR document and same-kind intent", () => {
  const draft = { ...defaultScreen("area-1", "泵房", "区域", points), inherited_from: "parent" }
  const payload = toScreenPayload(draft, true)
  assert.equal(payload.layer, "area-1")
  assert.equal(payload.version, 0)
  assert.equal(payload.apply_to_same, true)
  assert.equal(payload.inherited_from, undefined)
  assert.deepEqual(payload.point_ids, ["power", "pressure"])
})
