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
  members?: { node_id: string; name: string; value?: number | null; quality: string; time?: string }[]
  window?: { start: string; end: string; members: number }
}

export function createAggregateConfig(group: string, op: AggregateConfig["op"], members: string[], window: AggregateConfig["window"], quality: NonNullable<AggregateConfig["quality"]>, units: string): AggregateConfig {
  if (!group || !members.length) throw new Error("至少选择一个成员")
  return { group, op, members: [...new Set(members)], ...(units ? { units } : {}), ...(window ? { window } : {}), quality }
}

export function aggregateValueText(value: number | null | undefined, units = "") {
  return value === null || value === undefined ? "—" : `${value}${units ? ` ${units}` : ""}`
}
