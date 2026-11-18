import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import ts from "typescript"

// Reuse the project compiler without requiring Node's TypeScript loader.
const source = readFileSync(new URL("./src/observations.ts", import.meta.url), "utf8")
const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } })
const { describeObservation, observationForNode, observationKey, observationTimeLabel } = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`)

const sample = {
  device_id: "channel/1", point_id: "pressure", value: { kind: "number", number: 110 }, quality: "good",
  source_time: "2026-11-15T13:00:00.000Z", received_time: "2026-11-15T13:00:00.100Z", time_source: "platform_collection",
  expires_at: "2026-11-15T13:00:10.000Z",
}
const good = describeObservation(sample, { evaluatedAt: "2026-11-15T13:00:01.000Z" })
assert.equal(good.text, "110")
assert.equal(good.quality, "good")
assert.equal(good.sourceTime, sample.source_time)
assert.equal(good.receivedTime, sample.received_time)
assert.equal(good.timeSource, "平台采集")

const failed = describeObservation({ ...sample, quality: "invalid", reason: "read_failed" })
assert.equal(failed.text, "110")
assert.equal(failed.quality, "invalid")
assert.equal(failed.label, "读取失败")
assert.match(failed.reason, /本点读取失败/)
assert.equal(failed.sourceTime, sample.source_time)

const missing = describeObservation({ device_id: "channel/1", point_id: "missing", value: { kind: "unknown" }, quality: "invalid", reason: "read_failed" })
assert.equal(missing.text, "—")
assert.equal(missing.numericValue, undefined)
assert.equal(missing.sourceTime, "")
assert.equal(missing.receivedTime, "")
assert.match(missing.reason, /尚无有效观测/)
assert.equal(missing.timeSource, "尚无有效观测")
assert.equal(observationTimeLabel(undefined), "—")
assert.equal(observationTimeLabel("0001-01-01T00:00:00Z"), "—")

const expired = describeObservation({ ...sample, quality: "stale", reason: "source_expired" })
assert.equal(expired.label, "过期")
assert.equal(expired.text, "110")
assert.equal(expired.sourceTime, sample.source_time)

const expiredCache = describeObservation({ ...sample, expires_at: "2026-11-15T13:00:02.000Z" }, { evaluatedAt: "2026-11-15T13:00:01.000Z", elapsedMs: 1500 })
assert.equal(expiredCache.quality, "stale")
assert.equal(expiredCache.text, "110")
assert.equal(expiredCache.sourceTime, sample.source_time)

const unavailable = describeObservation(sample, { error: "连接失败" })
assert.equal(unavailable.quality, "invalid")
assert.match(unavailable.reason, /上次查询结果/)
assert.equal(unavailable.text, "110")
assert.equal(describeObservation(undefined, { loading: true }).label, "读取中")
assert.equal(describeObservation({ ...sample, reason: "future_source_time", quality: "invalid" }).label, "时间异常")
assert.equal(describeObservation({ ...sample, value: { kind: "number", number: 0 } }).text, "0")
assert.equal(describeObservation({ ...sample, value: { kind: "bool", bool: false } }).text, "分")
assert.notEqual(observationKey("channel/1", "pressure"), observationKey("channel/2", "pressure"))
assert.notEqual(observationKey("a/b", "c"), observationKey("a", "b/c"))
assert.notEqual(describeObservation({ ...sample, quality: "toString" }).quality, "good")

const noExpiry = describeObservation({ ...sample, expires_at: undefined })
assert.equal(noExpiry.quality, "uninitialized")
assert.equal(noExpiry.label, "状态未知")
assert.equal(noExpiry.reason, "未声明有效期")
const exactBoundary = describeObservation({ ...sample, expires_at: "2026-11-15T13:00:02.000Z" }, { evaluatedAt: "2026-11-15T13:00:02.000Z" })
assert.equal(exactBoundary.quality, "good")
assert.notEqual(expiredCache.reason, "数据有效")
assert.match(expiredCache.reason, /过期/)

const modbusNode = { id: "p", parent: "bus", type: "modbusIo", points: [{ type: "id", value: 1 }] }
const nodeSource = { values: { [observationKey("bus/1", "p")]: sample }, evaluatedAt: "2026-11-15T13:00:01.000Z" }
assert.equal(observationForNode(modbusNode, nodeSource).quality, "good")
const nonEms = observationForNode({ ...modbusNode, type: "variable" }, nodeSource)
assert.equal(nonEms.quality, "uninitialized")
assert.equal(nonEms.reason, "未声明有效期")

console.log("Observation presentation checks passed: values, missing/failed state, provenance, query errors and scoped identity")
