import test from "node:test"
import assert from "node:assert/strict"
import { buildTrend, historyTimeText, historyValueText, qualityText } from "./history.ts"

const at = second => `2026-11-22T08:00:${String(second).padStart(2, "0")}.000Z`

test("history values preserve zero and text while quality events have no fabricated value", () => {
  assert.equal(historyValueText({ value: 0 }), "0")
  assert.equal(historyValueText({ value: 0.48000000000000004 }), "0.48")
  assert.equal(historyValueText({ value: 1e-13 }), "1e-13")
  assert.equal(historyValueText({ text: "运行" }), "运行")
  assert.equal(historyValueText({ kind: "quality_event", quality: "invalid" }), "质量事件")
  assert.equal(historyValueText({}), "—")
  assert.equal(qualityText("good"), "有效")
  assert.equal(qualityText("invalid"), "无效")
})

test("history time prefers observation time and remains readable", () => {
  assert.equal(historyTimeText({ source_time: at(5), received_time: at(8) }), "2026-11-22 16:00:05")
  assert.equal(historyTimeText({}), "—")
})

test("trend sorts observations and breaks at invalid values and quality events", () => {
  const trend = buildTrend([
    { point_id: "p", source_time: at(4), value: 4, quality: "good", source: "runtime" },
    { point_id: "p", source_time: at(0), value: 0, quality: "good", source: "runtime" },
    { point_id: "p", source_time: at(2), kind: "quality_event", quality: "invalid", source: "runtime" },
    { point_id: "p", source_time: at(3), value: 3, quality: "invalid", source: "runtime" },
    { point_id: "p", source_time: at(5), value: 5, quality: "good", source: "runtime" },
  ], new Date(at(0)), new Date(at(10)))
  assert.deepEqual(trend.segments.map(segment => segment.map(point => point.value)), [[0], [4, 5]])
  assert.equal(trend.samples.length, 3)
  assert.equal(trend.interruptions, 2)
  assert.ok(Math.abs(trend.segments[1][0].x - trend.segments[0][0].x - (trend.right - trend.left) * 0.4) < 1e-9)
})

test("declared sampling cadence breaks long missing intervals without inventing values", () => {
  const trend = buildTrend([
    { point_id: "p", source_time: at(0), value: 1, quality: "good", source: "runtime" },
    { point_id: "p", source_time: at(8), value: 2, quality: "good", source: "runtime" },
  ], new Date(at(0)), new Date(at(10)), 960, 360, 1000)
  assert.deepEqual(trend.segments.map(segment => segment.map(point => point.value)), [[1], [2]])
  assert.equal(trend.interruptions, 1)
})

test("constant series gets a finite domain and single observations remain visible", () => {
  const trend = buildTrend([
    { point_id: "p", source_time: at(1), value: 7, quality: "good", source: "runtime" },
    { point_id: "p", source_time: at(2), value: 7, quality: "good", source: "runtime" },
  ], new Date(at(0)), new Date(at(10)))
  assert.equal(trend.segments.length, 1)
  assert.ok(Number.isFinite(trend.segments[0][0].y))
  assert.ok(trend.yMax > trend.yMin)
  assert.equal(new Set(trend.segments[0].map(point => point.y)).size, 1)
})
