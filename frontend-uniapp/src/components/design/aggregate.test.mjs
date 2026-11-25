import test from "node:test"
import assert from "node:assert/strict"
import { aggregateValueText, aggregateWindowText, createAggregateConfig } from "./aggregate.ts"

test("aggregate config uses existing API window and quality contract", () => {
  assert.deepEqual(createAggregateConfig("site", "avg", ["a", "b"], "5m", "degrade", "kW"), {
    group: "site", op: "avg", members: ["a", "b"], units: "kW", window: "5m", quality: "degrade",
  })
  assert.throws(() => createAggregateConfig("site", "avg", [], "", "count", "kW"), /至少选择一个成员/)
})

test("pending window is not presented as instantaneous; closed window exposes actual lag", () => {
  const config = createAggregateConfig("site", "sum", ["a"], "5m", "count", "kW")
  assert.equal(aggregateWindowText({ config, quality: "pending", configured: true }), "等待完整窗口关闭 · 5m")
  assert.equal(aggregateWindowText({ config, quality: "good", configured: true, window: { start: "start", end: "end", members: 1 }, lag_seconds: 301.2 }), "start 至 end · 1 个成员有样本 · 当前滞后 302s")
  assert.equal(aggregateWindowText({ config: { ...config, window: "" }, quality: "good", configured: true }), "瞬时 · 后台持续采样")
})

test("aggregate missing values stay missing", () => {
  assert.equal(aggregateValueText(null, "kW"), "—")
  assert.equal(aggregateValueText(0, "kW"), "0 kW")
  assert.equal(aggregateValueText(NaN, "kW"), "—")
  assert.equal(aggregateValueText(Infinity, "kW"), "—")
})
