import type { EmsStateValue, Node } from "./api"

export type ObservationQuality = "good" | "stale" | "invalid" | "uninitialized"

const qualityLabels: Record<ObservationQuality, string> = {
  good: "正常", stale: "过期", invalid: "无效", uninitialized: "无值",
}
const reasonLabels: Record<string, string> = {
  read_failed: "本点读取失败，保留最后有效观测",
  decode_failed: "本点解码失败，保留最后有效观测",
  missing_source_time: "缺少可确认的观测时间",
  future_source_time: "观测时间晚于接收或当前时刻",
  conflicting_source_time: "同一观测时刻出现矛盾数据",
  invalid_value: "来源数值无效",
  source_expired: "最后有效观测已过期",
  quality_invalid: "来源标记为无效",
  quality_stale: "来源标记为过期",
  quality_uninitialized: "来源尚无有效观测",
  expiry_unconfigured: "未声明有效期",
}
const timeSources: Record<string, string> = {
  platform_collection: "平台采集", device_observation: "设备观测",
}

function validTime(value?: string) {
  return typeof value === "string" && !value.startsWith("0001-") && Number.isFinite(Date.parse(value)) ? value : ""
}

export function observationKey(deviceID: string, pointID: string) {
  return JSON.stringify([deviceID, pointID])
}

export function observationTimeLabel(value?: string) {
  const time = validTime(value)
  return time ? new Date(time).toLocaleString("zh-CN", { hour12: false }) : "—"
}

export type ObservationOptions = { error?: string; loading?: boolean; evaluatedAt?: string; elapsedMs?: number }
export type ObservationSource = ObservationOptions & { values: Record<string, EmsStateValue> }

export function describeObservation(value?: EmsStateValue, options: ObservationOptions = {}) {
  const sourceTime = validTime(value?.source_time)
  const receivedTime = validTime(value?.received_time)
  const raw = value?.value
  let numericValue: number | undefined
  let text = "—"
  if (sourceTime && raw?.kind === "number" && typeof raw.number === "number" && Number.isFinite(raw.number)) {
    numericValue = raw.number
    text = String(raw.number)
  } else if (sourceTime && raw?.kind === "bool" && typeof raw.bool === "boolean") {
    numericValue = raw.bool ? 1 : 0
    text = raw.bool ? "合" : "分"
  } else if (sourceTime && raw?.kind === "enum" && typeof raw.enum === "string") {
    text = raw.enum || "—"
  }
  let quality: ObservationQuality = value?.quality && Object.prototype.hasOwnProperty.call(qualityLabels, value.quality)
    ? value.quality as ObservationQuality : "uninitialized"
  if (!sourceTime && quality === "good") quality = "uninitialized"
  const expiry = validTime(value?.expires_at)
  const evaluatedAt = validTime(options.evaluatedAt)
  if (quality === "good" && expiry && evaluatedAt && Date.parse(evaluatedAt) + Math.max(0, options.elapsedMs || 0) > Date.parse(expiry)) quality = "stale"
  if (quality === "good" && !expiry) quality = "uninitialized"
  let label = qualityLabels[quality]
  let reason = value?.reason ? reasonLabels[value.reason] || "观测无效，请检查来源" : quality === "good" ? "数据有效" : quality === "stale" ? "最后有效观测已过期" : "尚无有效观测"
  if (quality === "uninitialized" && value?.quality === "good" && !expiry) {
    reason = reasonLabels.expiry_unconfigured
    label = "状态未知"
  }
  if (value?.reason === "expiry_unconfigured") {
    quality = "uninitialized"
    label = "状态未知"
  }
  if (value?.reason === "read_failed") label = "读取失败"
  if (value?.reason === "decode_failed") label = "解码失败"
  if (!sourceTime && ["read_failed", "decode_failed"].includes(value?.reason || "")) reason = `${label}，尚无有效观测`
  if (["missing_source_time", "future_source_time", "conflicting_source_time"].includes(value?.reason || "")) label = "时间异常"
  if (options.error) {
    quality = "invalid"
    label = "读取失败"
    reason = `观测接口读取失败：${options.error}；显示的值仅为上次查询结果`
  } else if (options.loading && !value) {
    quality = "uninitialized"
    label = "读取中"
    reason = "正在读取观测状态"
  }
  return {
    quality, label, reason, text, numericValue, sourceTime, receivedTime,
    timeSource: !sourceTime ? "尚无有效观测" : value?.time_source ? timeSources[value.time_source] || "时间来源未声明" : "时间来源未声明",
    lastAttemptTime: validTime(value?.last_attempt_time),
  }
}

export function observationForNode(node: Node, source: ObservationSource) {
  if (node.type !== "modbusIo") return describeObservation({ device_id: "", point_id: node.id, value: { kind: "unknown" }, quality: "uninitialized", reason: "expiry_unconfigured" }, source)
  const server = node.points.find((item) => item.type === "id")?.value ?? 1
  return describeObservation(source.values[observationKey(`${node.parent}/${server}`, node.id)], source)
}
