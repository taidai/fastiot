import type { Snapshot } from "../../types/api"

export type AccessPolicy = {
  admin: boolean
  role: string
  root_node_ids?: string[]
  actions?: string[]
}

export type AuthorizationCheck = { operation: string; action: string; allowed: boolean; reason: string }
export type AuthorizationExplanation = { read_only: true; user_id: string; target_id: string; checks: AuthorizationCheck[] }

export function authorizationText(check?: Pick<AuthorizationCheck, "allowed" | "reason">): string {
  if (!check) return "未检查"
  if (check.allowed) return "授权允许"
  if (check.reason === "forbidden" || check.reason === "source_forbidden") return "拒绝 · 对象范围未授权"
  if (["admin_required", "operator_required", "operation_not_allowed"].includes(check.reason)) return "拒绝 · 动作未授权"
  if (check.reason === "no_access") return "拒绝 · 账号无有效范围"
  return `拒绝 · ${check.reason}`
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
  version_conflict: "工程已被修改，请重新预检后发布；未生成版本",
  fingerprint_required: "请先读取当前工程预检，再发布版本",
  version_not_deliverable: "此记录是非可交付快照，不能作为已发布项目包导出",
  preflight_unavailable: "预检服务不可用，不能把读取失败当作空工程或通过",
  package_integrity_mismatch: "项目包完整性校验失败",
  version_integrity_mismatch: "冻结版本完整性校验失败",
  preflight_failed: "预检存在阻断项；请修复后重新预检，不能发布可交付版本",
  calculation_rollback_unsupported: "当前版本或工程包含计算点，后端不支持回退",
  virtual_input_rollback_unsupported: "当前版本或工程包含虚拟输入点，后端不支持回退",
  package_in_use: "行业配置仍在使用，后端已拒绝停用",
  project_industry_dependency_unavailable: "项目依赖的 EMS 行业包未启用，相关配置与控制已阻断",
  industry_package_not_installed: "EMS 行业包未启用，此行业能力不可用",
  industry_scope_required: "行业包启停影响整个运行实例，需要项目根范围的站点设置权限",
  forbidden: "当前账号无权执行此操作",
  requires_site: "此操作需要选择真实 Site，项目根不能代替 Site",
  source_forbidden: "当前账号无权操作此项目",
  target_parent_unavailable: "导入目标项目不可用或当前账号无权访问",
  invalid_package_format: "文件不是 FastIOT 项目包",
  version_not_found: "版本不存在或不属于当前 Site",
  version_integrity_missing: "原始版本缺少完整性校验，未执行回退",
  version_package_missing: "可交付版本缺少原项目包引用，未执行回退",
  version_package_mismatch: "版本与项目包资源不一致，未执行回退",
  rollback_baseline_failed: "回退前基线保存或回读失败；未写入配置，可修复存储后重试",
  rollback_baseline_unrestorable: "当前工程不能形成可恢复基线；未写入配置",
  rollback_failed_recovered: "回退写入失败，已恢复回退前基线",
  rollback_recovery_failed: "回退失败且基线恢复未确认；当前配置不可视为完整版本",
}

function dependencyError(code: string, operation?: "import"): string {
  const rejected = operation === "import" ? "；已拒绝导入，未创建任何节点" : ""
  const kindText = (kind: string) => kind === "industry" ? "行业包依赖" : "Profile 依赖"
  let match = code.match(/^missing_dependency:(profile|industry):([^:@]+)@([^:]+?)(:not_installed)?$/)
  if (match) {
    const [, kind, id, version, notInstalled] = match
    const prefix = kind === "industry" && notInstalled ? "目标实例未安装" : "缺少 "
    return `${prefix}${kindText(kind)} ${id}@${version}${rejected}`
  }
  match = code.match(/^incompatible_dependency:(profile|industry):([^:]+):required@([^:]+):(?:current|available)@([^:]+)$/)
  if (match) {
    const [, kind, id, required, current] = match
    return `${kindText(kind)}版本不匹配：${id} 需要 ${required}，当前${kind === "industry" ? "可用" : "为"} ${current}${rejected}`
  }
  match = code.match(/^undeclared_dependency:(profile|industry):([^:]+)$/)
  if (match) return `项目包未声明所需的${kindText(match[1])} ${match[2]}${rejected}`
  return ""
}

type RollbackErrorDetails = {
  baseline?: string
  retryable?: boolean
  recovered?: boolean
  blockers?: { code: string; scope: string; node_id: string; node_type: string }[]
}

export function deliveryError(error: unknown, operation?: "import"): string {
  const code = error && typeof error === "object" && "code" in error ? String(error.code || "") : ""
  const details = error && typeof error === "object" && "details" in error
    ? (error as { details?: RollbackErrorDetails }).details
    : undefined
  if ((code === "calculation_rollback_unsupported" || code === "virtual_input_rollback_unsupported") && details?.blockers?.length && ERROR_LABELS[code]) {
    const resources = details.blockers.map(blocker => `${blocker.scope === "version" ? "目标版本" : "当前工程"} ${blocker.node_type} ${blocker.node_id}`).join("；")
    return `${ERROR_LABELS[code]}；${resources}；未写入配置，可修复阻断后重试`
  }
  if (code === "rollback_failed_recovered" && details?.baseline) {
    return `回退写入失败，已恢复回退前基线 ${details.baseline}；可排查原因后重试`
  }
  if (code === "rollback_recovery_failed" && details?.baseline) {
    return `回退失败且基线 ${details.baseline} 的恢复未确认；当前配置不可视为完整版本，不要盲目重试`
  }
  if (ERROR_LABELS[code]) return ERROR_LABELS[code]
  const dependency = dependencyError(code, operation)
  if (dependency) return dependency
  if (error instanceof Error) return error.message
  return String(error || "操作失败")
}

export function clock(value?: string) {
  if (!value) return "—"
  const time = new Date(value)
  return Number.isFinite(time.getTime()) ? time.toLocaleString("zh-CN", { hour12: false }) : "—"
}
