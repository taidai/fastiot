export type GapSample = {
  time: number
  value?: number
  quality?: string
  kind: "observation" | "quality_event"
  reason?: string
  late?: boolean
  seriesId?: string
  seriesName?: string
  color?: string
}

export type GapBand = { from: number; to: number; reasons: string[]; seriesId?: string; seriesName?: string; color?: string }

export type TrendGeometry<T extends GapSample = GapSample> = {
  observations: T[]
  segments: T[][]
  gaps: GapBand[]
  events: T[]
}

const reasonLabels: Record<string, string> = {
  timeout: "读取超时",
  read_failed: "读取失败",
  decode_failed: "解码失败",
  disconnect: "连接断开",
  crc: "校验错误",
  source_expired: "采样过期",
  invalid_observation: "无效观测",
  late_observation: "迟到补入",
}

export function reasonLabel(reason: string): string {
  return reasonLabels[reason] || reason
}

export function gapReasonText(reasons: string[]): string {
  return reasons.length ? `已知原因：${reasons.map(reasonLabel).join("、")}` : "原因未知（无质量记录）"
}

export function trendGeometry<T extends GapSample>(samples: T[], cycleMs: number, start: number, end: number): TrendGeometry<T> {
  const inWindow = (sample: T) => sample.time >= start && sample.time <= end
  const observations = samples
    .filter((sample) => sample.kind === "observation" && sample.value !== undefined && inWindow(sample) && (!sample.quality || sample.quality === "good"))
    .sort((a, b) => a.time - b.time) as T[]
  const invalid = samples.filter((sample) => sample.kind === "observation" && sample.value !== undefined && sample.quality && sample.quality !== "good" && inWindow(sample))
  const events = samples.filter((sample) => sample.kind === "quality_event" && inWindow(sample)).concat(invalid).sort((a, b) => a.time - b.time)
  const reasonsBetween = (from: number, to: number) => {
    const reasons = new Set<string>()
    for (const entry of events) {
      if (entry.time < from || entry.time > to) continue
      reasons.add(entry.reason || (entry.kind === "quality_event" ? entry.quality || "quality_event" : "invalid_observation"))
    }
    return [...reasons]
  }
  const segments: T[][] = []
  let current: T[] = []
  const flush = () => { if (current.length) segments.push(current); current = [] }
  const border = (sample: T) => ({ seriesId: sample.seriesId, seriesName: sample.seriesName, color: sample.color })
  for (const sample of observations) {
    const previous = current[current.length - 1]
    const eventBetween = previous && events.some((entry) => entry.time > previous.time && entry.time <= sample.time)
    const invalidBetween = previous && invalid.some((entry) => entry.time > previous.time && entry.time < sample.time)
    if (sample.late || previous?.late) flush()
    else if (previous && cycleMs > 0 && sample.time - previous.time > cycleMs * 3) flush()
    else if (eventBetween || invalidBetween) flush()
    current.push(sample)
    if (sample.late) flush()
  }
  flush()
  const gaps: GapBand[] = []
  for (let index = 0; index < segments.length - 1; index++) {
    const from = segments[index][segments[index].length - 1].time
    const to = segments[index + 1][0].time
    gaps.push({ from, to, reasons: reasonsBetween(from, to), ...border(segments[index + 1][0]) })
  }
  if (cycleMs > 0 && observations.length > 0) {
    const first = observations[0]
    const last = observations[observations.length - 1]
    if (first.time - start > cycleMs * 3) gaps.push({ from: start, to: first.time, reasons: reasonsBetween(start, first.time), ...border(first) })
    if (end - last.time > cycleMs * 3) gaps.push({ from: last.time, to: end, reasons: reasonsBetween(last.time, end), ...border(last) })
  }
  return { observations, segments, gaps, events }
}
