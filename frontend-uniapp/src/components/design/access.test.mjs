import test from "node:test"
import assert from "node:assert/strict"
import { automaticPointMap, batchCSV, canCommandAccess, canConfigureAccess, deviceAccessChipClass, deviceAccessLabel, deviceAccessState, prepareModelSave, safeVerificationCommands, verificationError } from "./access.ts"

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
  assert.equal(deviceAccessState({ bound: true, verified: true }), "access")
  assert.equal(deviceAccessState({ profile_id: "vendor-v1" }), "pending", "a persisted profile without an explicit bound flag is still bound")
  assert.equal(deviceAccessLabel("unbound"), "未绑定")
  assert.equal(deviceAccessLabel("pending"), "已绑定待验证")
  assert.equal(deviceAccessLabel("access"), "已接入")
  assert.equal(deviceAccessChipClass("pending"), "", "pending must not borrow the connected style")
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
