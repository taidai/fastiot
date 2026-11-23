import test from "node:test"
import assert from "node:assert/strict"
import {
  businessId,
  conditionText,
  createSimulationPayload,
  isWritable,
  moveRows,
  parseDocument,
  timeConditionText,
  withBindings,
} from "./strategy.ts"

const nodes = [
  { id: "n1", type: "variable", parent: "root", points: [
    { type: "description", key: "", time: "", text: "出口压力" },
    { type: "businessId", key: "", time: "", text: "pressure" },
  ] },
  { id: "n2", type: "modbusIo", parent: "root", points: [
    { type: "description", key: "", time: "", text: "泵启停" },
    { type: "modbusIoType", key: "", time: "", text: "modbusCoil" },
  ] },
  { id: "n3", type: "modbusIo", parent: "root", points: [
    { type: "modbusIoType", key: "", time: "", text: "modbusInputRegister" },
  ] },
]

test("parseDocument normalizes missing arrays and rejects malformed drafts", () => {
  const empty = parseDocument("")
  assert.deepEqual(empty, { table: { rows: [] }, bindings: { points: {}, actions: {} } })

  const parsed = parseDocument(JSON.stringify({ table: { rows: [{ row_id: "r1" }] } }))
  assert.deepEqual(parsed.table.rows[0], { row_id: "r1", conditions: [], actions: [] })
  assert.throws(() => parseDocument("{"), /草稿格式无法读取/)
})

test("withBindings adds real node targets without replacing existing bindings", () => {
  const document = parseDocument(JSON.stringify({
    table: { rows: [] },
    bindings: { points: { pressure: { node_id: "legacy", point_type: "value" } } },
  }))
  const result = withBindings(document, nodes)
  assert.equal(result.bindings.points.pressure.node_id, "legacy")
  assert.deepEqual(result.bindings.points.n2, { node_id: "n2", point_type: "value" })
  assert.deepEqual(result.bindings.actions.n2, { node_id: "n2", point_type: "valueSet" })
  assert.equal(result.bindings.actions.n3, undefined)
  assert.equal(document.bindings.points.n2, undefined, "does not mutate the input")
})

test("moveRows preserves order semantics without mutating the draft", () => {
  const rows = [{ row_id: "a" }, { row_id: "b" }, { row_id: "c" }]
  assert.deepEqual(moveRows(rows, 1, -1).map(row => row.row_id), ["b", "a", "c"])
  assert.deepEqual(moveRows(rows, 0, -1).map(row => row.row_id), ["a", "b", "c"])
  assert.deepEqual(rows.map(row => row.row_id), ["a", "b", "c"])
})

test("createSimulationPayload keeps numeric zero and omits empty optional inputs", () => {
  const document = parseDocument("")
  assert.deepEqual(createSimulationPayload(document, { pressure: 0 }, { state: "", mode: "auto" }, ""), {
    document,
    inputs: { pressure: 0 },
    text_inputs: { mode: "auto" },
  })
  assert.equal(createSimulationPayload(document, {}, {}, "2026-11-22T14:00").at, "2026-11-22T14:00")
})

test("conditionText describes only backend-supported condition kinds", () => {
  const label = id => ({ pressure: "出口压力" })[id] || id
  assert.equal(conditionText({ point_id: "pressure", operator: "ge", value: 0.4 }, label), "出口压力 ≥ 0.4")
  assert.equal(conditionText({ point_id: "permit", operator: "eq", value: 1, kind: "bool" }, label), "permit = 是")
  assert.equal(conditionText({ point_id: "state", operator: "ne", value: 0, kind: "enum", text: "故障" }, label), "state ≠ 故障")
  assert.equal(conditionText({ point_id: "", operator: "", value: 0, kind: "time", start: "22:00", end: "08:00" }, label), "每天 22:00–次日08:00")
})

test("timeConditionText handles weekday masks and all-day windows", () => {
  assert.equal(timeConditionText({ weekday_mask: 3 }), "周日、周一 全天")
  assert.equal(timeConditionText({ start: "08:00", end: "20:00" }), "每天 08:00–20:00")
})

test("businessId and isWritable follow the existing node contract", () => {
  assert.equal(businessId(nodes[0]), "pressure")
  assert.equal(businessId(nodes[1]), "n2")
  assert.equal(isWritable(nodes[0]), true)
  assert.equal(isWritable(nodes[1]), true)
  assert.equal(isWritable(nodes[2]), false)
})
