import test from "node:test"
import assert from "node:assert/strict"
import { automaticPointMap, batchCSV, batchVerificationOutcome, boundBusinessIDs, canCommandAccess, canConfigureAccess, deviceAccessChipClass, deviceAccessLabel, deviceAccessState, deviceBindingProgress, prepareModelSave, safeVerificationCommands, summarizeBatchVerification, verificationError } from "./access.ts"

const profile = {
  id: "vendor-v1",
  point_specs: [{ name: "Active Power" }, { name: "运行 状态" }, { name: "Missing" }],
}
const points = [
  { id: "p-other", name: "Active Power", server_id: 2 },
  { id: "p-power", name: " active power ", server_id: 1 },
  { id: "p-state", name: "运行状态", server_id: 1 },
]

test("automaticPointMap matches normalized names only inside the selected station", () => {
  assert.deepEqual(automaticPointMap(profile, points, 1), {
    "Active Power": "p-power",
    "运行 状态": "p-state",
  })
  assert.deepEqual(automaticPointMap(profile, points, 3), {})
  assert.equal(profile.point_specs.length, 3, "does not mutate the Profile")
})

test("prepareModelSave removes empty mappings and trims verification metadata without dropping model gates", () => {
  const input = {
    name: " 泵 1 ",
    profile_id: "vendor-v1",
    business_node_id: "node-1",
    point_map: { power: " p1 ", empty: "" },
    verified: false,
    verifier: " 张工 ",
    verified_at: "2026-11-22",
    device_serial: " SN-1 ",
    verification: " 已逐点核对 ",
    verification_required: true,
    replaced_from: "old-vendor",
  }
  const result = prepareModelSave(input)
  assert.deepEqual(result.point_map, { power: "p1" })
  assert.equal(result.name, "泵 1")
  assert.equal(result.verifier, "张工")
  assert.equal(result.device_serial, "SN-1")
  assert.equal(result.verification, "已逐点核对")
  assert.equal(result.verification_required, true)
  assert.equal(result.replaced_from, "old-vendor")
  assert.deepEqual(input.point_map, { power: " p1 ", empty: "" }, "does not mutate the draft")
})

test("verificationError follows the backend contract for manual field verification", () => {
  const base = { name: "泵 1", profile_id: "vendor-v1", point_map: {}, verified: false }
  const metadata = { verifier: "张工", verified_at: "2026-11-22", device_serial: "SN-1" }
  assert.equal(verificationError(base), "")
  assert.equal(verificationError({ ...base, profile_id: "" }), "先选择并保存设备 Profile")
  assert.equal(verificationError({ ...base, verified: true }), "标记已验证时必须填写验证人、日期和设备序列号")
  assert.equal(verificationError({ ...base, ...metadata, point_map: { power: "p1" }, verified: true }), "")
})

test("canConfigureAccess follows resolved actions before role defaults", () => {
  const snapshot = (policy, actions) => ({ user: { user_id: "u", policy, ...(actions ? { actions } : {}) }, roots: [], nodes: [] })
  assert.equal(canConfigureAccess(snapshot({ admin: true, role: "admin", actions: [] }, [])), true)
  assert.equal(canConfigureAccess(snapshot({ admin: false, role: "implementer" })), true)
  assert.equal(canConfigureAccess(snapshot({ admin: false, role: "implementer" }, ["export"])), false)
  assert.equal(canConfigureAccess(snapshot({ admin: false, role: "implementer", actions: ["export"] })), false)
  assert.equal(canConfigureAccess(snapshot({ admin: false, role: "viewer" }, ["configure"])), true)
  assert.equal(canConfigureAccess(snapshot({ admin: false, role: "viewer", actions: ["configure"] })), true)
})

test("deviceAccessState keeps unbound, bound-awaiting-verification and connected apart", () => {
  assert.equal(deviceAccessState({ bound: false }), "unbound")
  assert.equal(deviceAccessState({ bound: true, verified: false }), "pending")
  assert.equal(deviceAccessState({ bound: true, verified: true }), "pending", "a manual sign-off without L2 point evidence is not access")
  assert.equal(deviceAccessState({ bound: true, verified: true, verified_points: 10 }), "pending", "an unknown required-point count is not access")
  assert.equal(deviceAccessState({ bound: true, verified: true, required_points: 10, verified_points: 4 }), "pending", "required-point evidence overrides the manual mark")
  assert.equal(deviceAccessState({ bound: true, verified: false, required_points: 10, verified_points: 10 }), "access", "every required point verified counts as connected")
  assert.equal(deviceAccessState({ bound: true, verified: false, required_points: 0, verified_points: 0 }), "pending", "no required points is not evidence of access")
  assert.equal(deviceAccessState({ profile_id: "vendor-v1" }), "pending", "a persisted profile without an explicit bound flag is still bound")
  assert.equal(deviceAccessLabel("unbound"), "未绑定")
  assert.equal(deviceAccessLabel("pending"), "已绑定待验证")
  assert.equal(deviceAccessLabel("access"), "已接入")
  assert.equal(deviceAccessChipClass("pending"), "", "pending must not borrow the connected style")
})

test("boundBusinessIDs reads only persisted UnitModel bindings from Modbus channels", () => {
  const nodes = [{
    id: "channel-1", type: "modbus", parent: "site-1", points: [
      { type: "easyemsUnitModel", key: "1", text: JSON.stringify({ profile_id: "p1", business_node_id: "device-1", point_map: {} }) },
      { type: "easyemsUnitModel", key: "2", tombstone: 1, text: JSON.stringify({ profile_id: "p1", business_node_id: "device-deleted", point_map: {} }) },
      { type: "easyemsUnitModel", key: "broken", text: "not json" },
    ],
  }]
  assert.deepEqual([...boundBusinessIDs(nodes)], ["device-1"])
})

test("deviceBindingProgress keeps a persisted manual sign-off out of the access count", () => {
  const nodes = [
    { id: "channel-1", type: "modbus", parent: "site-1", points: [
      { type: "easyemsUnitModel", key: "1", text: JSON.stringify({ profile_id: "p1", business_node_id: "device-1", point_map: { level: "p1" }, verified: true }) },
    ] },
    { id: "device-1", type: "device", parent: "area-1", points: [] },
    { id: "device-2", type: "device", parent: "area-1", points: [] },
  ]
  assert.deepEqual(deviceBindingProgress(nodes), { devices: 2, bound: 1, verified: 0 })
})

test("verificationError rejects empty mappings and command-required gaps", () => {
  const config = { name: "泵 1", profile_id: "vendor-v1", point_map: {}, verified: true, verifier: "张工", verified_at: "2026-11-22", device_serial: "SN-1" }
  assert.equal(verificationError(config), "至少映射并核对一个 Profile 点位，才能标记已验证")
  assert.equal(verificationError({ ...config, point_map: { power: "p1" } }, ["Active Power"]), "命令必需点未映射，不能标记已验证：Active Power")
})

test("batchCSV uses stable device IDs instead of ambiguous display names", () => {
  const text = batchCSV([{ id: "device-1", name: "泵,1" }, { id: "device-2", name: "泵 2" }], "profile-1", "channel-1", 17)
  assert.equal(text, "设备,型号,通道,站号\ndevice-1,profile-1,channel-1,17\ndevice-2,profile-1,channel-1,18")
  assert.equal(batchCSV([], "profile-1", "channel-1", 1), "设备,型号,通道,站号")
})

test("safeVerificationCommands exposes only explicit fixed safe commands with readback", () => {
  const profile = {
    point_specs: [{ name: "test", read_only: false, note: "L3_SAFE 固定测试" }, { name: "ordinary", read_only: false }, { name: "feedback", read_only: true }],
    command_specs: [
      { id: "safe", name: "安全测试", point: "test", value: 1, readback: "feedback" },
      { id: "ordinary", name: "启动", point: "ordinary", value: 1, readback: "feedback" },
      { id: "parameter", name: "参数", point: "test", readback: "feedback", parameters: [{ name: "value", point: "test" }] },
      { id: "no-readback", name: "无回读", point: "test", value: 1 },
    ],
  }
  assert.deepEqual(safeVerificationCommands(profile).map(command => command.id), ["safe"])
})

test("canCommandAccess independently gates L3 from configure permission", () => {
  const snapshot = { user: { user_id: "u", policy: { admin: false, role: "implementer" }, actions: ["configure"] }, roots: [], nodes: [] }
  assert.equal(canCommandAccess(snapshot), false)
  assert.equal(canCommandAccess({ ...snapshot, user: { ...snapshot.user, actions: ["configure", "command"] } }), true)
})

test("batch verification summary keeps failed and unconfirmed devices visible", () => {
  const record = (level, passed, current = true, reasons = []) => ({ at: "2026-11-26T08:00:00Z", actor: "张工", level, passed, current, reasons })
  const outcomes = [
    batchVerificationOutcome("1号泵", "L1", [record("L1", true)]),
    batchVerificationOutcome("1号泵", "L2", [record("L2", true)]),
    batchVerificationOutcome("2号泵", "L1", [record("L1", true)]),
    batchVerificationOutcome("2号泵", "L2", [record("L2", false, true, ["必需点缺失"])]),
    batchVerificationOutcome("3号泵", "L1", [], "L1 结果未确认：超时"),
    batchVerificationOutcome("3号泵", "L2", [record("L2", true, false)]),
  ]
  const summary = summarizeBatchVerification(outcomes)
  assert.equal(summary.total, 6)
  assert.equal(summary.passed, 3)
  assert.equal(summary.failed, 1)
  assert.equal(summary.unknown, 2)
  assert.match(summary.text, /3\/6 项通过/)
  assert.match(summary.text, /未通过/)
  assert.match(summary.text, /未验证或结果未确认/)
  assert.doesNotMatch(summary.text, /全部通过/)
  assert.ok(summary.failures.some(item => item.includes("2号泵") && item.includes("L2") && item.includes("必需点缺失")))
  assert.ok(summary.failures.some(item => item.includes("3号泵") && item.includes("L1") && item.includes("结果未确认")))
  assert.ok(summary.failures.some(item => item.includes("3号泵") && item.includes("L2") && item.includes("记录已失效")))
})
