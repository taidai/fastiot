import test from "node:test"
import assert from "node:assert/strict"
import {
  actionText,
  canAction,
  deliveryError,
  resolveActions,
  roleText,
  statusText,
} from "./contracts.ts"

const policy = (role, actions = []) => ({ admin: role === "admin", role, actions })

test("resolveActions follows backend role defaults", () => {
  assert.ok(resolveActions(policy("admin")).includes("account_manage"))
  assert.ok(resolveActions(policy("implementer")).includes("configure"))
  assert.ok(resolveActions(policy("implementer")).includes("export"))
  assert.ok(!resolveActions(policy("implementer")).includes("account_manage"))
  assert.deepEqual(resolveActions(policy("operator")), [
    "write_value", "command", "alarm_ack", "alarm_maintain", "virtual_input_write",
  ])
  assert.deepEqual(resolveActions(policy("viewer")), [])
})

test("a non-empty action whitelist replaces role defaults; empty means defaults", () => {
  assert.deepEqual(resolveActions(policy("implementer", ["audit_view"])), ["audit_view"])
  assert.deepEqual(resolveActions(policy("viewer", ["configure", "audit_view"])), ["configure", "audit_view"])
  assert.ok(resolveActions(policy("implementer", [])).includes("configure"))
  assert.deepEqual(resolveActions(policy("viewer", [])), [])
})

test("canAction prefers resolved snapshot actions and always allows admin", () => {
  const snapshot = actions => ({ user: { policy: policy("viewer"), actions } })
  assert.equal(canAction(snapshot(["audit_view"]), "audit_view"), true)
  assert.equal(canAction(snapshot([]), "configure"), false)
  assert.equal(canAction({ user: { policy: policy("admin"), actions: [] } }, "account_manage"), true)
  assert.equal(canAction(null, "configure"), false)
})

test("Chinese labels preserve unknown backend values", () => {
  assert.equal(statusText("pass"), "通过")
  assert.equal(statusText("warn"), "注意")
  assert.equal(statusText("fail"), "阻断")
  assert.equal(statusText("future"), "future")
  assert.equal(roleText("implementer"), "实施")
  assert.equal(actionText("account_manage"), "账号管理")
  assert.equal(actionText("future_action"), "future_action")
})

test("deliveryError explains confirmation and rollback/package limits", () => {
  assert.equal(deliveryError({ status: 409, code: "preflight_failed", message: "preflight_failed" }), "预检存在阻断项；确认后仍可冻结，但冻结不等于部署")
  assert.equal(deliveryError({ status: 409, code: "calculation_rollback_unsupported", message: "x" }), "当前版本或工程包含计算点，后端不支持回退")
  assert.equal(deliveryError({ status: 409, code: "virtual_input_rollback_unsupported", message: "x" }), "当前版本或工程包含虚拟输入点，后端不支持回退")
  assert.equal(deliveryError({ status: 409, code: "package_in_use", message: "x" }), "行业配置仍在使用，后端已拒绝停用")
  assert.equal(deliveryError(new Error("网络失败")), "网络失败")
})
