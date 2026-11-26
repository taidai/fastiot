import test from "node:test"
import assert from "node:assert/strict"
import {
  actionText,
  authorizationText,
  canAction,
  countText,
  deliveryError,
  resolveActions,
  roleText,
  statusText,
} from "./contracts.ts"

const policy = (role, actions = []) => ({ admin: role === "admin", role, actions })

test("countText keeps a missing count out of the number column", () => {
  assert.equal(countText(0), "0")
  assert.equal(countText(3), "3")
  assert.equal(countText(undefined), "—")
})

test("authorization cells fail closed without a server result and explain scope denial", () => {
  assert.equal(authorizationText(undefined), "未检查")
  assert.equal(authorizationText({ allowed: true, reason: "allowed" }), "授权允许")
  assert.equal(authorizationText({ allowed: false, reason: "forbidden" }), "拒绝 · 对象范围未授权")
  assert.equal(authorizationText({ allowed: false, reason: "admin_required" }), "拒绝 · 动作未授权")
})

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
  assert.equal(statusText("fail"), "失败")
  assert.equal(statusText("future"), "future")
  assert.equal(roleText("implementer"), "实施")
  assert.equal(actionText("account_manage"), "账号管理")
  assert.equal(actionText("future_action"), "future_action")
})

test("deliveryError explains refused dependencies in Chinese without hiding versions", () => {
  const imported = code => deliveryError({ code }, "import")
  assert.equal(
    imported("missing_dependency:profile:pump@abc123"),
    "缺少 Profile 依赖 pump@abc123；已拒绝导入，未创建任何节点",
  )
  assert.equal(
    imported("incompatible_dependency:profile:pump:required@abc123:current@def456"),
    "Profile 依赖版本不匹配：pump 需要 abc123，当前为 def456；已拒绝导入，未创建任何节点",
  )
  assert.equal(
    imported("incompatible_dependency:industry:ems:required@0.9.0:available@1.0.0"),
    "行业包依赖版本不匹配：ems 需要 0.9.0，当前可用 1.0.0；已拒绝导入，未创建任何节点",
  )
  assert.equal(
    imported("missing_dependency:industry:ems@1.0.0:not_installed"),
    "目标实例未安装行业包依赖 ems@1.0.0；已拒绝导入，未创建任何节点",
  )
  assert.equal(
    imported("undeclared_dependency:industry:ems"),
    "项目包未声明所需的行业包依赖 ems；已拒绝导入，未创建任何节点",
  )
  assert.equal(
    deliveryError({ code: "missing_dependency:profile:pump@abc123" }),
    "缺少 Profile 依赖 pump@abc123",
    "export/read errors must not claim an import was refused",
  )
})

test("deliveryError explains confirmation and rollback/package limits", () => {
  assert.equal(deliveryError({ status: 409, code: "preflight_failed", message: "preflight_failed" }), "预检存在阻断项；请修复后重新预检，不能发布可交付版本")
  assert.equal(deliveryError({ status: 409, code: "calculation_rollback_unsupported", message: "x" }), "当前版本或工程包含计算点，后端不支持回退")
  assert.equal(deliveryError({ status: 409, code: "virtual_input_rollback_unsupported", message: "x" }), "当前版本或工程包含虚拟输入点，后端不支持回退")
  assert.equal(deliveryError({ status: 409, code: "package_in_use", message: "x" }), "行业配置仍在使用，后端已拒绝停用")
  assert.equal(deliveryError(new Error("网络失败")), "网络失败")
})

test("rollback errors retain resource blockers and recovery baselines", () => {
  const blocked = {
    code: "calculation_rollback_unsupported", message: "x",
    details: { retryable: true, blockers: [{ code: "calculation_rollback_unsupported", scope: "version", node_id: "calc-23", node_type: "calculation" }] },
  }
  assert.equal(deliveryError(blocked), "当前版本或工程包含计算点，后端不支持回退；目标版本 calculation calc-23；未写入配置，可修复阻断后重试")
  assert.equal(
    deliveryError({ code: "rollback_failed_recovered", message: "x", details: { baseline: "pv-baseline-23", recovered: true, retryable: true } }),
    "回退写入失败，已恢复回退前基线 pv-baseline-23；可排查原因后重试",
  )
  assert.equal(
    deliveryError({ code: "rollback_recovery_failed", message: "x", details: { baseline: "pv-baseline-23", recovered: false, retryable: false } }),
    "回退失败且基线 pv-baseline-23 的恢复未确认；当前配置不可视为完整版本，不要盲目重试",
  )
})
