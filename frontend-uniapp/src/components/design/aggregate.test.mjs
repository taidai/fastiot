import test from "node:test"
import assert from "node:assert/strict"
import { aggregateValueText, createAggregateConfig } from "./aggregate.ts"

test("aggregate config uses existing API window and quality contract", () => {
  assert.deepEqual(createAggregateConfig("site", "avg", ["a", "b"], "5m", "degrade", "kW"), {
    group: "site", op: "avg", members: ["a", "b"], units: "kW", window: "5m", quality: "degrade",
  })
  assert.throws(() => createAggregateConfig("site", "avg", [], "", "count", "kW"), /至少选择一个成员/)
})

test("aggregate missing values stay missing", () => {
  assert.equal(aggregateValueText(null, "kW"), "—")
  assert.equal(aggregateValueText(0, "kW"), "0 kW")
})
