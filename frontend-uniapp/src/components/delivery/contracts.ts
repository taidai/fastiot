import type { Snapshot } from "../../types/api"

export type AccessPolicy = {
  admin: boolean
  role: string
  root_node_ids?: string[]
  actions?: string[]
}

const ACTION_ORDER = [
  "configure", "write_value", "command", "rule_publish", "alarm_ack", "alarm_maintain",
  "delete", "export", "account_manage", "audit_view", "site_settings",
  "virtual_input_apply", "virtual_input_write", "calculation_apply",
]

const ROLE_DEFAULTS: Record<string, string[]> = {
  implementer: [
    "configure", "rule_publish", "delete", "export", "virtual_input_apply", "calculation_apply",
    "write_value", "command", "alarm_ack", "alarm_maintain", "virtual_input_write",
  ],
  operator: ["write_value", "command", "alarm_ack", "alarm_maintain", "virtual_input_write"],
}

export function resolveActions(policy: AccessPolicy): string[] {
  if (policy.admin || policy.role === "admin") return [...ACTION_ORDER]
  if (policy.actions?.length) return ACTION_ORDER.filter(action => policy.actions!.includes(action))
  return ACTION_ORDER.filter(action => (ROLE_DEFAULTS[policy.role] || []).includes(action))
}

export function canAction(snapshot: Snapshot | null, action: string): boolean {
  const user = snapshot?.user
  if (!user) return false
  if (user.policy.admin) return true
  if (Array.isArray(user.actions)) return user.actions.includes(action)
  return resolveActions(user.policy).includes(action)
}

const STATUS_LABELS: Record<string, string> = { pass: "通过", warn: "注意", fail: "阻断" }
const ROLE_LABELS: Record<string, string> = { admin: "管理员", implementer: "实施", operator: "操作员", viewer: "查看者" }
const ACTION_LABELS: Record<string, string> = {
  configure: "工程配置",
  write_value: "写入数值",
  command: "设备命令",
  rule_publish: "规则发布",
  alarm_ack: "报警确认",
  alarm_maintain: "报警维护",
  delete: "删除对象",
  export: "导出",
  account_manage: "账号管理",
  audit_view: "审计查看",
  site_settings: "站点设置",
  virtual_input_apply: "虚拟输入配置",
  virtual_input_write: "虚拟输入写入",
  calculation_apply: "计算点配置",
}

export function statusText(status?: string) {
  return status ? STATUS_LABELS[status] || status : "未运行"
}
export function roleText(role?: string) {
  return role ? ROLE_LABELS[role] || role : "—"
}
export function actionText(action: string) {
  return ACTION_LABELS[action] || action
}

const ERROR_LABELS: Record<string, string> = {
  preflight_failed: "预检存在阻断项；确认后仍可冻结，但冻结不等于部署",
  calculation_rollback_unsupported: "当前版本或工程包含计算点，后端不支持回退",
  virtual_input_rollback_unsupported: "当前版本或工程包含虚拟输入点，后端不支持回退",
  package_in_use: "行业配置仍在使用，后端已拒绝停用",
  forbidden: "当前账号无权执行此操作",
  requires_site: "此操作需要选择真实 Site，项目根不能代替 Site",
  source_forbidden: "当前账号无权操作此项目",
  target_parent_unavailable: "导入目标项目不可用或当前账号无权访问",
  invalid_package_format: "文件不是 FastIOT 项目包",
  version_not_found: "版本不存在或不属于当前 Site",
}

export function deliveryError(error: unknown): string {
  const code = error && typeof error === "object" && "code" in error ? String(error.code || "") : ""
  if (ERROR_LABELS[code]) return ERROR_LABELS[code]
  if (error instanceof Error) return error.message
  return String(error || "操作失败")
}

export function clock(value?: string) {
  if (!value) return "—"
  const time = new Date(value)
  return Number.isFinite(time.getTime()) ? time.toLocaleString("zh-CN", { hour12: false }) : "—"
}
