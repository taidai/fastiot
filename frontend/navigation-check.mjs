// 导航分组与角色可见性检查：不依赖浏览器，直接验证规则本身。
// 运行：cd frontend && node navigation-check.mjs
// 规则见 docs/jobs.md：一级导航是交付任务，任务内是页签；隐藏页只由页面内链接进入。
import assert from "node:assert/strict"
import { navGroupsFor, pageDefs, visiblePagesFor } from "./src/navigation.ts"

const keys = (list) => list.map((def) => def.key)
const allow = (actions) => (action) => actions.includes(action)
const none = () => false
const all = () => true
const implementer = allow(["configure", "export", "delete", "write_value", "command", "rule_publish", "virtual_input_apply", "virtual_input_write", "calculation_apply"])
const operator = allow(["write_value", "command", "alarm_ack", "alarm_maintain", "virtual_input_write"])

// 注册表完整性：页面不重复，每个页面都有分组。
const allKeys = keys(pageDefs)
assert.equal(new Set(allKeys).size, allKeys.length, "页面注册表存在重复 key")
assert.ok(pageDefs.every((def) => Boolean(def.group)), "存在没有任务分组的页面")
assert.equal(keys(visiblePagesFor("customer", none))[0], "devices", "客户工作台应默认落在运行总览")
assert.equal(keys(visiblePagesFor("delivery", all))[0], "deploy", "交付工作台应默认落在交付验收")

// 查看者/操作员：只有客户工作台，且没有交付页面。
const customerPages = ["devices", "screen", "alarms", "history", "dispatchTrace", "health"]
for (const [role, can] of [
  ["viewer", none],
  ["operator", operator],
]) {
  assert.deepEqual(keys(visiblePagesFor("customer", can)), customerPages, `${role} 的客户工作台页面不符`)
  assert.deepEqual(visiblePagesFor("delivery", can), [], `${role} 不应看到交付工作台`)
  const groups = navGroupsFor("customer", can)
  assert.deepEqual(groups.map((group) => group.key), ["run"], `${role} 的客户一级导航应为运行`)
  assert.deepEqual(keys(groups[0].pages), ["devices", "screen", "alarms"], `${role} 的运行页签不符`)
}

// 实施：两个工作台都能进；交付工作台没有账号与审计。
const implementerDelivery = keys(visiblePagesFor("delivery", implementer))
assert.deepEqual(
  implementerDelivery,
  ["deploy", "ledger", "channels", "points", "aggregate", "opcua", "energyUnits", "unitWiring", "resourceGroups", "rules", "allocators"],
  "实施角色的交付工作台页面不符",
)
assert.ok(!implementerDelivery.includes("users") && !implementerDelivery.includes("audit"), "实施角色不应看到账号与审计")

const implementerNav = navGroupsFor("delivery", implementer)
assert.deepEqual(implementerNav.map((group) => group.key), ["access", "assets", "strategy", "acceptance"], "交付一级导航应为四个任务")
assert.deepEqual(keys(implementerNav.find((group) => group.key === "access").pages), ["channels", "points", "aggregate", "opcua"], "接入页签不符")
assert.deepEqual(keys(implementerNav.find((group) => group.key === "assets").pages), ["energyUnits", "unitWiring", "resourceGroups"], "能源设备页签不符")
assert.deepEqual(keys(implementerNav.find((group) => group.key === "strategy").pages), ["rules", "allocators"], "策略页签不符")
assert.deepEqual(keys(implementerNav.find((group) => group.key === "acceptance").pages), ["deploy", "ledger"], "交付验收页签不符")

// 管理员：交付工作台多出系统任务。
const adminNav = navGroupsFor("delivery", all)
assert.deepEqual(adminNav.map((group) => group.key), ["access", "assets", "strategy", "acceptance", "system"], "管理员交付一级导航应多出系统")
assert.deepEqual(keys(adminNav.find((group) => group.key === "system").pages), ["users", "audit"], "系统页签不符")

// 只有对应动作的账号才看到系统页。
const auditOnly = allow(["audit_view"])
assert.deepEqual(keys(visiblePagesFor("delivery", auditOnly)).filter((key) => key === "audit" || key === "users"), ["audit"], "只有审计动作时应只看到审计页")
assert.deepEqual(keys(visiblePagesFor("delivery", allow(["account_manage"]))).filter((key) => key === "audit" || key === "users"), ["users"], "只有账号动作时应只看到账号页")

// 隐藏页不得出现在任何一级导航或页签里。
const shownKeys = new Set(adminNav.flatMap((group) => keys(group.pages)))
for (const hidden of ["history", "dispatchTrace", "health"]) {
  assert.ok(!shownKeys.has(hidden), `隐藏页 ${hidden} 不应出现在导航`)
}

// 任何角色都不能从客户工作台看到配置类页面。
for (const def of visiblePagesFor("customer", all)) {
  assert.ok(!["channels", "points", "energyUnits", "rules", "deploy"].includes(def.key), `客户工作台混入了配置页 ${def.key}`)
}

// 行业包未安装：行业页面不可达，底座页面与分组保持完整；能源设备分组整体消失。
const packageAbsent = (id) => id !== "ems"
assert.deepEqual(
  keys(visiblePagesFor("delivery", implementer, packageAbsent)),
  ["deploy", "ledger", "channels", "points", "aggregate", "opcua", "rules"],
  "未安装行业包时交付工作台只留底座页面",
)
assert.deepEqual(
  navGroupsFor("delivery", implementer, packageAbsent).map((group) => group.key),
  ["access", "strategy", "acceptance"],
  "未安装行业包时能源设备分组应消失",
)
assert.deepEqual(
  keys(navGroupsFor("delivery", implementer, packageAbsent).find((group) => group.key === "strategy").pages),
  ["rules"],
  "未安装行业包时策略只剩底座决策表",
)
assert.deepEqual(keys(visiblePagesFor("customer", none, packageAbsent)), customerPages, "未安装行业包不影响客户工作台")

console.log("navigation-check passed:", {
  pages: allKeys.length,
  customerGroups: navGroupsFor("customer", all).length,
  implementerGroups: implementerNav.length,
  adminGroups: adminNav.length,
})
