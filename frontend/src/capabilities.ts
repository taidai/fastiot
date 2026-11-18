export const Actions = {
  configure: "configure",
  writeValue: "write_value",
  command: "command",
  rulePublish: "rule_publish",
  alarmAck: "alarm_ack",
  alarmMaintain: "alarm_maintain",
  delete: "delete",
  export: "export",
  accountManage: "account_manage",
  auditView: "audit_view",
  siteSettings: "site_settings",
  virtualInputApply: "virtual_input_apply",
  virtualInputWrite: "virtual_input_write",
  calculationApply: "calculation_apply",
} as const

export type Action = (typeof Actions)[keyof typeof Actions]
export type CapabilityReason = "not_granted" | "target_disabled" | "read_only" | "server_unavailable"
export type TargetState = { disabled?: boolean; readOnly?: boolean; serverAvailable?: boolean }
export type Verdict = { allowed: boolean; reason?: CapabilityReason }

export function hasAction(actions: ReadonlySet<string> | undefined, action: string): boolean {
  return Boolean(actions?.has(action))
}

export function capability(actions: ReadonlySet<string> | undefined, action: string, target?: TargetState): Verdict {
  if (!hasAction(actions, action)) return { allowed: false, reason: "not_granted" }
  if (target?.disabled) return { allowed: false, reason: "target_disabled" }
  if (target?.readOnly) return { allowed: false, reason: "read_only" }
  if (target?.serverAvailable === false) return { allowed: false, reason: "server_unavailable" }
  return { allowed: true }
}

const reasonTexts: Record<CapabilityReason, string> = {
  not_granted: "当前账号没有此操作权限",
  target_disabled: "该点位已停用",
  read_only: "该点位只读",
  server_unavailable: "设备当前不支持此操作",
}

export function reasonText(reason?: CapabilityReason): string {
  return reason ? reasonTexts[reason] : ""
}
