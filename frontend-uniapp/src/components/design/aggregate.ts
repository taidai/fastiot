export type AggregateConfig = {
  group: string
  op: "sum" | "avg" | "min" | "max" | "count"
  members: string[]
  units?: string
  window?: "" | "5m" | "15m" | "1h"
  quality?: "ignore" | "count" | "degrade"
}
export type AggregateResult = {
  configured: boolean
  config: AggregateConfig
  value?: number | null
  quality: string
  partial?: boolean
  valid?: number
  total?: number
  time?: string
  evaluated_at?: string
  time_source?: string
  lag_seconds?: number
  members?: { node_id: string; name: string; value?: number | null; quality: string; time?: string }[]
  window?: { start: string; end: string; members: number }
}

export function createAggregateConfig(group: string, op: AggregateConfig["op"], members: string[], window: AggregateConfig["window"], quality: NonNullable<AggregateConfig["quality"]>, units: string): AggregateConfig {
  if (!group || !members.length) throw new Error("至少选择一个成员")
  return { group, op, members: [...new Set(members)], ...(units ? { units } : {}), ...(window ? { window } : {}), quality }
}

export function aggregateWindowText(result: AggregateResult) {
  if (!result.config.window) return "瞬时 · 后台持续采样"
  if (!result.window) return `等待完整窗口关闭 · ${result.config.window}`
  const lag = result.lag_seconds === undefined ? "未知" : `${Math.ceil(result.lag_seconds)}s`
  return `${result.window.start} 至 ${result.window.end} · ${result.window.members} 个成员有样本 · 当前滞后 ${lag}`
}

export function aggregateValueText(value: number | null | undefined, units = "") {
  return value === null || value === undefined || !Number.isFinite(value) ? "—" : `${value}${units ? ` ${units}` : ""}`
}
