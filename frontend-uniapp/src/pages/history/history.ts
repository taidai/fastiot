import type { HistoryRecord } from "../../types/api"

export function qualityText(quality?: string) {
  return ({ good: "有效", stale: "过期", invalid: "无效", uninitialized: "未初始化" } as Record<string, string>)[quality || ""] || quality || "—"
}

export function historyValueText(record: Pick<HistoryRecord, "value" | "text" | "kind">) {
  if (record.kind === "quality_event") return "质量事件"
  if (record.text) return record.text
  return record.value === undefined ? "—" : String(Number(record.value.toPrecision(12)))
}

export function historyTimeText(record: Pick<HistoryRecord, "source_time" | "received_time">) {
  const value = record.source_time || record.received_time
  if (!value) return "—"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (part: number) => String(part).padStart(2, "0")
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

export type TrendPoint = {
  record: HistoryRecord
  time: number
  value: number
  x: number
  y: number
}

export function buildTrend(records: HistoryRecord[], start: Date, end: Date, width = 960, height = 360, cycleMs = 0) {
  const left = 58
  const right = width - 18
  const top = 20
  const bottom = height - 48
  const startMs = start.getTime()
  const endMs = end.getTime()
  const duration = Math.max(1, endMs - startMs)
  const ordered = records.map(record => ({ record, time: new Date(record.source_time || record.received_time || "").getTime() }))
    .filter(item => Number.isFinite(item.time) && item.time >= startMs && item.time <= endMs)
    .sort((a, b) => a.time - b.time)
  const numeric = ordered.filter(item => item.record.kind !== "quality_event" && item.record.quality !== "invalid" && item.record.value !== undefined && Number.isFinite(item.record.value))
  const rawValues = numeric.map(item => item.record.value as number)
  const min = rawValues.length ? Math.min(...rawValues) : 0
  const max = rawValues.length ? Math.max(...rawValues) : 1
  const padding = min === max ? (Math.abs(min) || 1) / 2 : (max - min) * 0.08
  const yMin = min - padding
  const yMax = max + padding
  const ySpan = yMax - yMin || 1
  let interruptions = 0
  const segments: TrendPoint[][] = []
  let segment: TrendPoint[] = []
  const samples: TrendPoint[] = []
  const flush = () => {
    if (segment.length) segments.push(segment)
    segment = []
  }
  for (const item of ordered) {
    const value = item.record.value
    const valid = item.record.kind !== "quality_event" && item.record.quality !== "invalid" && value !== undefined && Number.isFinite(value)
    if (!valid) {
      interruptions += 1
      flush()
      continue
    }
    const point = {
      record: item.record,
      time: item.time,
      value: value as number,
      x: left + ((item.time - startMs) / duration) * (right - left),
      y: bottom - (((value as number) - yMin) / ySpan) * (bottom - top),
    }
    if (cycleMs > 0 && segment.length && item.time - segment[segment.length - 1].time > cycleMs * 2.5) {
      interruptions += 1
      flush()
    }
    samples.push(point)
    segment.push(point)
  }
  flush()
  return { samples, segments, interruptions, left, right, top, bottom, width, height, yMin, yMax, startMs, endMs }
}
