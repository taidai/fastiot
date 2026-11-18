import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import ts from "typescript"

const source = readFileSync(new URL("./src/historyGaps.ts", import.meta.url), "utf8")
const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } })
const { trendGeometry, gapReasonText } = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`)

const observation = (time, value, quality = "good") => ({ time, value, quality, kind: "observation" })
const event = (time, reason, quality = "invalid") => ({ time, kind: "quality_event", reason, quality })

const gap = trendGeometry([
  observation(0, 1), observation(1000, 2), observation(2000, 3),
  observation(100000, 4), observation(101000, 5),
], 1000, -5000, 110000)
assert.equal(gap.segments.length, 2)
assert.equal(gap.segments[0].length, 3)
assert.equal(gap.segments[1].length, 2)
assert.equal(gap.gaps.length, 3)
const middle = gap.gaps.find((item) => item.from === 2000 && item.to === 100000)
assert.ok(middle, "missing interval was not disclosed")
assert.equal(gapReasonText(middle.reasons), "原因未知（无质量记录）")

const known = trendGeometry([
  observation(0, 1), observation(1000, 2),
  event(20000, "timeout"),
  observation(60000, 3),
], 1000, 0, 60000)
assert.equal(known.gaps.length, 1)
assert.deepEqual(known.gaps[0].reasons, ["timeout"])
assert.equal(gapReasonText(known.gaps[0].reasons), "已知原因：读取超时")

const invalid = trendGeometry([
  observation(0, 1), observation(1000, 2),
  observation(2000, 99, "invalid"),
  observation(3000, 3),
], 1000, 0, 3000)
assert.equal(invalid.segments.length, 2)
assert.equal(invalid.segments[0].length, 2)
assert.equal(invalid.segments[1].length, 1)
assert.deepEqual(invalid.gaps[0].reasons, ["invalid_observation"])

const undeclared = trendGeometry([
  observation(0, 1), observation(600000, 2),
], 0, 0, 600000)
assert.equal(undeclared.segments.length, 1)
assert.equal(undeclared.gaps.length, 0, "undeclared cadence must not invent a gap")

const edges = trendGeometry([observation(50000, 1)], 1000, 0, 100000)
assert.equal(edges.gaps.length, 2)
assert.equal(edges.gaps[0].from, 0)
assert.equal(edges.gaps[1].to, 100000)

const late = trendGeometry([
  observation(0, 1), observation(1000, 2), observation(2000, 3), { ...observation(500, 9), late: true },
], 1000, 0, 2000)
assert.equal(late.observations.length, 4)
assert.equal(late.segments.length, 3)
assert.ok(late.segments.some((segment) => segment.length === 1 && segment[0].late), "late sample bridged neighbouring observations")

const outage = trendGeometry([
  observation(0, 1), event(1000, "timeout"), { ...observation(3000, 9), late: true }, observation(6000, 2),
], 1000, 0, 6000)
assert.ok(outage.segments.every((segment) => segment.length === 1), "late sample connected across a known invalid interval")
assert.ok(outage.gaps.some((gap) => gap.reasons.includes("timeout")))
assert.equal(outage.gaps.length, 2)

const shortEvent = trendGeometry([
  observation(0, 1), event(100, "timeout"), observation(500, 2),
], 1000, 0, 500)
assert.equal(shortEvent.segments.length, 2)
assert.deepEqual(shortEvent.gaps[0].reasons, ["timeout"])
assert.equal(undeclared.events.length, 0)
console.log("History gap checks passed: 3x cadence gaps, known/unknown cause, invalid breaks, undeclared cadence, edges and late samples")
