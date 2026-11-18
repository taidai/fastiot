import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { IonCardContent } from "@ionic/react"
import { downloadHistory, EmsAlarm, fetchAlarms, fetchHistory, HistoryPointResult, HistoryRecord, nameOf, Node, point, request } from "./api"
import { EmsButton, EmsCard, EmsInput, EmsSectionHeader, EmsSelect, EmsSelectOption, EmptyState, InlineNotice, SegmentedTabs } from "./components/ems"
import { gapReasonText, reasonLabel, trendGeometry, type GapBand } from "./historyGaps"

const ranges = [
  { key: "1h", label: "1 小时", hours: 1 },
  { key: "24h", label: "24 小时", hours: 24 },
  { key: "7d", label: "7 天", hours: 24 * 7 },
]

const seriesColors = ["#2f6f9f", "#c07a1f", "#2d8f78", "#b14f6f", "#6b4fa0", "#5f8f3f", "#8a5a2b", "#3f7f7f"]
const severityColors: Record<string, string> = { critical: "#b14f6f", warning: "#b07a2a", info: "#2f6f9f" }

type SavedQuery = { name: string; points: string[]; range: string; interval: string }
type QualityCounts = { good: number; stale: number; invalid: number; none: number; events: number }
type TrendSample = {
  time: number; value?: number; quality?: string; kind: "observation" | "quality_event"; reason?: string; late?: boolean; received?: number
  seriesId: string; seriesName: string; color: string; units: string
}
type TrendSeries = { id: string; name: string; color: string; units: string; cycleMs: number; latestValue?: number; latestTime?: number; latestQuality?: string; samples: TrendSample[] }
type AlarmMark = { at: number; severity: string; title: string }

const savedQueryKey = "fastems-history-queries"

function loadSavedQueries(): SavedQuery[] {
  try { return JSON.parse(localStorage.getItem(savedQueryKey) || "[]") as SavedQuery[] } catch { return [] }
}

function recentPoint(points: Node[]) {
  const ranked = points.map((node) => {
    const time = point(node, "value")?.time || ""
    return { id: node.id, time }
  }).filter((item) => item.time).sort((a, b) => b.time.localeCompare(a.time))
  return ranked[0]?.id || points[0]?.id || ""
}

function Trend({ series, alarms, start, end, onOpenPoints }: { series: TrendSeries[]; alarms: AlarmMark[]; start: number; end: number; onOpenPoints?: () => void }) {
  const width = 760
  const height = 260
  const pad = 30
  const plotW = width - pad * 2
  const plotH = height - pad * 2
  const geometry = series.map((item) => ({ item, ...trendGeometry(item.samples, item.cycleMs, start, end) }))
  const observations = geometry.flatMap((entry) => entry.observations)
  const values = observations.map((sample) => sample.value as number)
  const min = values.length ? Math.min(...values) : 0
  const max = values.length ? Math.max(...values) : 1
  const span = max - min || 1
  const domain = end > start ? end - start : 1
  const xOf = (time: number) => pad + ((time - start) / domain) * plotW
  const yOf = (value: number) => height - pad - ((value - min) / span) * plotH
  const inWindow = (sample: TrendSample) => sample.time >= start && sample.time <= end
  const all = series.flatMap((item) => item.samples.filter((sample) => sample.time >= start && sample.time <= end)).sort((a, b) => a.time - b.time || a.seriesId.localeCompare(b.seriesId))
  const [selected, setSelected] = useState(-1)
  useEffect(() => { setSelected((current) => current >= 0 && current < all.length ? current : -1) }, [all.length])
  const gaps = geometry.flatMap((entry) => entry.gaps)
  const displayGaps = [...gaps.reduce((map, gap) => {
    const key = `${gap.seriesId}-${gap.from}-${gap.to}`
    const previous = map.get(key)
    map.set(key, previous ? { ...previous, reasons: [...new Set([...previous.reasons, ...gap.reasons])] } : gap)
    return map
  }, new Map<string, GapBand>()).values()].sort((a, b) => a.from - b.from)
  const marks = alarms.filter((alarm) => alarm.at >= start && alarm.at <= end)
  const selectedSample = selected >= 0 ? all[selected] : undefined

  const selectNearest = (clientX: number, target: SVGSVGElement) => {
    if (all.length === 0) return
    const bounds = target.getBoundingClientRect()
    const viewX = (clientX - bounds.left) * (width / bounds.width)
    const time = start + ((viewX - pad) / plotW) * domain
    let best = 0
    for (let index = 1; index < all.length; index++) {
      if (Math.abs(all[index].time - time) < Math.abs(all[best].time - time)) best = index
    }
    setSelected(best)
  }
  const moveSelection = (delta: number) => {
    if (all.length === 0) return
    setSelected((current) => Math.max(0, Math.min(all.length - 1, (current < 0 ? all.length - 1 : current) + delta)))
  }
  const selectSample = (sample: TrendSample) => {
    const index = all.findIndex((item) => item.seriesId === sample.seriesId && item.time === sample.time && item.kind === sample.kind)
    if (index >= 0) setSelected(index)
  }

  return <div className="trend-wrap">
    <div className="trend-legend" role="list" aria-label="对比图例">
      {series.map((item) => <span key={item.id} className="trend-legend-item" role="listitem">
        <i style={{ background: item.color }} />
        <b>{item.name}</b>
        <em>{item.latestValue === undefined ? "无有效观测" : `${item.latestValue}${item.units}${item.latestQuality ? "" : "（质量未记录）"}${item.latestTime ? ` · ${new Date(item.latestTime).toLocaleString("zh-CN", { hour12: false })}` : ""}${item.latestQuality ? " · 最后有效值，非当前" : ""}`}</em>
      </span>)}
    </div>
    {observations.length === 0 ? <div className="trend-empty">
      <span>{all.length > 0 ? "所选范围内只有质量事件，没有有效观测。" : "所选范围内没有样本。"}</span>
      {all.length > 0 && <ul className="trend-events-list">{all.map((sample, index) => <li key={index}>
        {sample.seriesName} · {new Date(sample.time).toLocaleString("zh-CN", { hour12: false })} · {sample.quality || "invalid"} · {reasonLabel(sample.reason || "") || "原因未知"}
      </li>)}</ul>}
      {all.length === 0 && onOpenPoints && <button type="button" className="secondary-button" onClick={onOpenPoints}>去检查点位</button>}
    </div> : <svg
      className="trend-chart"
      viewBox={`0 0 ${width} ${height}`}
      role="group"
      tabIndex={0}
      aria-label="历史趋势；方向键或鼠标可读取样本时间、数值与质量"
      onMouseMove={(event) => selectNearest(event.clientX, event.currentTarget)}
      onMouseLeave={() => setSelected(-1)}
      onKeyDown={(event) => {
        if (event.key === "ArrowRight") { event.preventDefault(); moveSelection(1) }
        else if (event.key === "ArrowLeft") { event.preventDefault(); moveSelection(-1) }
        else if (event.key === "Home") { event.preventDefault(); setSelected(0) }
        else if (event.key === "End") { event.preventDefault(); setSelected(all.length - 1) }
      }}
    >
      {[0.25, 0.5, 0.75].map((ratio) => <line key={ratio} x1={pad} x2={width - pad} y1={pad + ratio * plotH} y2={pad + ratio * plotH} className="trend-grid" />)}
      {gaps.map((gap, index) => <rect key={`gap-${index}`} x={xOf(gap.from)} y={pad} width={Math.max(1, xOf(gap.to) - xOf(gap.from))} height={plotH} className="trend-gap" style={{ stroke: gap.color, fill: gap.color }}>
        <title>缺口 · {gapReasonText(gap.reasons)}{gap.seriesName ? ` · ${gap.seriesName}` : ""}</title>
      </rect>)}
      {marks.map((alarm) => {
        const x = xOf(alarm.at)
        return <line key={`${alarm.at}-${alarm.title}`} x1={x} x2={x} y1={pad} y2={height - pad} className="trend-alarm" style={{ stroke: severityColors[alarm.severity] || severityColors.info }}>
          <title>{alarm.title} · {new Date(alarm.at).toLocaleString("zh-CN", { hour12: false })}</title>
        </line>
      })}
      {geometry.map(({ item, segments, events }) => <g key={item.id}>
        {segments.map((segment, index) => segment.length > 1
          ? <polyline key={index} points={segment.map((sample) => `${xOf(sample.time)},${yOf(sample.value as number)}`).join(" ")} className={`trend-line${item.cycleMs === 0 ? " trend-line-undeclared" : ""}`} style={{ stroke: item.color }} />
          : <circle key={index} cx={xOf(segment[0].time)} cy={yOf(segment[0].value as number)} r={2.5} className="trend-dot" style={{ stroke: item.color }} onMouseEnter={() => selectSample(segment[0])} />)}
        {item.samples.filter((sample) => sample.late && sample.value !== undefined && inWindow(sample)).map((sample, index) =>
          <circle key={`late-${index}`} cx={xOf(sample.time)} cy={yOf(sample.value as number)} r={3} className="trend-late" style={{ stroke: item.color }} onMouseEnter={() => selectSample(sample)}><title>迟到补入 · 观测 {new Date(sample.time).toLocaleString("zh-CN", { hour12: false })} · 接收 {sample.received ? new Date(sample.received).toLocaleString("zh-CN", { hour12: false }) : "未记录"} · 未参与当时实时控制</title></circle>)}
        {events.map((sample, index) => <g key={`event-${index}`} className={`trend-event ${sample.quality || "invalid"}`} onMouseEnter={() => selectSample(sample)}>
          <line x1={xOf(sample.time)} x2={xOf(sample.time)} y1={pad} y2={height - pad} />
          <circle cx={xOf(sample.time)} cy={sample.kind === "observation" && sample.value !== undefined ? yOf(sample.value) : pad} r={3} />
          <title>{sample.kind === "observation" ? "无效观测" : "质量事件"} · {sample.quality || "invalid"} · {reasonLabel(sample.reason || "") || "原因未知"} · {new Date(sample.time).toLocaleString("zh-CN", { hour12: false })}</title>
        </g>)}
      </g>)}
      {selectedSample && <circle
        cx={xOf(selectedSample.time)}
        cy={selectedSample.value === undefined ? height / 2 : yOf(selectedSample.value)}
        r={5}
        className="trend-selected"
        style={{ stroke: selectedSample.color }}
      />}
      <text x={pad} y={pad - 8} className="trend-label">{max.toFixed(1)}</text>
      <text x={pad} y={height - pad + 16} className="trend-label">{min.toFixed(1)}</text>
    </svg>}
    {selectedSample && <p className="trend-readout" role="status" aria-live="polite">
      {selectedSample.seriesName} · {new Date(selectedSample.time).toLocaleString("zh-CN", { hour12: false })} · {selectedSample.kind === "quality_event"
        ? `质量事件 ${selectedSample.quality || "invalid"} · ${reasonLabel(selectedSample.reason || "") || "原因未知"}`
        : `值 ${selectedSample.value}${selectedSample.units} · 质量 ${selectedSample.quality || "未记录"}`}
      {selectedSample.late ? ` · 迟到补入，接收于 ${selectedSample.received ? new Date(selectedSample.received).toLocaleString("zh-CN", { hour12: false }) : "未记录"}，未参与当时实时控制` : ""}
    </p>}
    {displayGaps.length > 0 && <ul className="trend-gaps" aria-label="缺口区间">
      {displayGaps.map((gap, index) => <li key={index}>{gap.seriesName ? `${gap.seriesName}：` : ""}{new Date(gap.from).toLocaleString("zh-CN", { hour12: false })} 至 {new Date(gap.to).toLocaleString("zh-CN", { hour12: false })}：{gapReasonText(gap.reasons)}</li>)}
    </ul>}
    {marks.length > 0 && <p className="trend-alarm-note">图上竖线为所选时间段内与所选点位相关的报警事件，悬停查看详情。</p>}
  </div>
}

export default function HistoryView({ token, points, initialPoint, onError, isAdmin = false, canExport = true, onOpenPoints }: {
  token: string
  points: Node[]
  initialPoint?: string
  onError: (message: string) => void
  isAdmin?: boolean
  canExport?: boolean
  onOpenPoints?: () => void
}) {
  const [requestedSelection, setSelected] = useState<string[]>(() => {
    const first = initialPoint ? points.some((item) => item.id === initialPoint) ? initialPoint : "" : recentPoint(points)
    return first ? [first] : []
  })
  const scope = points.map(item => item.id).sort().join(",")
  const selected = useMemo(() => requestedSelection.filter(id => scope.split(",").includes(id)), [requestedSelection, scope])
  useEffect(() => { setSelected(current => current.filter(id => scope.split(",").includes(id))) }, [scope])
  const [range, setRange] = useState("24h")
  const [interval, setInterval] = useState("raw")
  const [seriesData, setSeriesData] = useState<Record<string, HistoryRecord[]>>({})
  const [queryResult, setQueryResult] = useState<{ bounds: { start: Date; end: Date }; aggregate: string; results: HistoryPointResult[] }>()
  const [queryError, setQueryError] = useState("")
  const [moreLoading, setMoreLoading] = useState(false)
  const [exportState, setExportState] = useState<{ busy: boolean; message: string; error: boolean }>({ busy: false, message: "", error: false })
  const queryController = useRef<AbortController | null>(null)
  const [queryKey, setQueryKey] = useState(0)
  useEffect(() => {
    queryController.current?.abort()
    setMoreLoading(false)
    setQueryResult(undefined); setSeriesData({}); setQueryError(""); setExportState({ busy: false, message: "", error: false })
  }, [token, scope, range, interval, requestedSelection])
  const [loading, setLoading] = useState(false)
  const [alarms, setAlarms] = useState<EmsAlarm[]>([])
  const [showAlarms, setShowAlarms] = useState(true)
  const [savedQueries, setSavedQueries] = useState<SavedQuery[]>(loadSavedQueries)
  const [queryName, setQueryName] = useState("")
  const [limitDraft, setLimitDraft] = useState(20000)
  const [limitBusy, setLimitBusy] = useState(false)
  const [limitNotice, setLimitNotice] = useState("")

  const hours = ranges.find((item) => item.key === range)?.hours || 24

  const [bounds, setBounds] = useState(() => {
    const end = new Date()
    return { start: new Date(end.getTime() - 24 * 3600 * 1000), end }
  })

  const load = useCallback(async () => {
    if (selected.length === 0) { setSeriesData({}); setQueryResult(undefined); return }
    queryController.current?.abort()
    const controller = new AbortController()
    queryController.current = controller
    const window = { start: new Date(), end: new Date() }
    window.start = new Date(window.end.getTime() - hours * 3600 * 1000)
    setBounds(window)
    setLoading(true); setQueryError("")
    try {
      const result = await fetchHistory(token, selected, window.start, window.end, interval, undefined, controller.signal)
      if (controller.signal.aborted) return
      setSeriesData(Object.fromEntries(result.results.map(item => [item.node_id, item.records || []])))
      setQueryResult({ bounds: window, aggregate: interval, results: result.results })
    } catch (cause) {
      if (!controller.signal.aborted) { setQueryResult(undefined); setSeriesData({}); setQueryError(cause instanceof Error ? cause.message : "历史读取失败") }
    } finally { if (queryController.current === controller) setLoading(false) }
  }, [token, selected, hours, interval])
  useEffect(() => { void load(); return () => queryController.current?.abort() }, [token, queryKey])

  useEffect(() => {
    let cancelled = false
    const loadAlarms = async () => {
      try {
        const result = await fetchAlarms(token)
        if (!cancelled) setAlarms(result.alarms || [])
      } catch {
        // 报警叠加是补充信息，失败不阻塞趋势。
      }
    }
    void loadAlarms()
    const timer = window.setInterval(() => void loadAlarms(), 5000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [token])

  useEffect(() => {
    if (!isAdmin) return
    void request<{ limit: number }>(token, "/history-settings").then((result) => setLimitDraft(result.limit)).catch(() => {})
  }, [token, isAdmin])

  async function saveLimit() {
    setLimitBusy(true)
    setLimitNotice("")
    try {
      const result = await request<{ limit: number }>(token, "/history-settings", "PUT", { limit: limitDraft })
      setLimitDraft(result.limit)
      setLimitNotice("已保存；运行历史立即生效，持久历史重启后生效")
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "历史保留保存失败")
    } finally {
      setLimitBusy(false)
    }
  }

  async function loadMore() {
    if (!queryResult || moreLoading) return
    queryController.current?.abort()
    const controller = new AbortController()
    queryController.current = controller
    setMoreLoading(true); setQueryError("")
    try {
      const pages = await Promise.all(queryResult.results.map(async old => {
        if (!old.truncated || !old.next_before) return old
        const before = new Date(old.next_before)
        const result = await fetchHistory(token, old.node_id, queryResult.bounds.start, queryResult.bounds.end, queryResult.aggregate, before, controller.signal, old.next_before_count)
        const next = result.results[0]
        return { ...next, matched: old.matched, returned: old.returned + next.returned, records: [...old.records, ...next.records].sort((a, b) => new Date(a.source_time || 0).getTime() - new Date(b.source_time || 0).getTime()) }
      }))
      if (controller.signal.aborted || queryController.current !== controller) return
      setQueryResult({ ...queryResult, results: pages })
      setSeriesData(Object.fromEntries(pages.map(item => [item.node_id, item.records])))
    } catch (cause) {
      if (!controller.signal.aborted) setQueryError(cause instanceof Error ? cause.message : "继续读取失败")
    } finally { if (queryController.current === controller) setMoreLoading(false) }
  }
  async function exportSelected() {
    if (!queryResult || selected.length === 0 || exportState.busy) return
    setExportState({ busy: true, message: "", error: false })
    try {
      const result = await downloadHistory(token, selected, queryResult.bounds.start, queryResult.bounds.end, queryResult.aggregate)
      setExportState({ busy: false, message: `已导出 ${result.returned} 条记录 · ${result.filename}`, error: false })
    } catch (cause) {
      setExportState({ busy: false, message: `导出失败，未生成完整文件：${cause instanceof Error ? cause.message : "请重试"}`, error: true })
    }
  }

  const series = useMemo<TrendSeries[]>(() => selected.map((id, index) => {
    const node = points.find((item) => item.id === id)
    const color = seriesColors[index % seriesColors.length]
    const units = node ? point(node, "units")?.text || "" : ""
    const result = queryResult?.results.find((item) => item.node_id === id)
    const bucketMs = queryResult?.aggregate === "5m" ? 5 * 60_000 : queryResult?.aggregate === "1h" ? 60 * 60_000 : 0
    const cycleMs = queryResult?.aggregate === "raw" ? result?.sample_interval_ms || 0 : bucketMs
    const samples = (seriesData[id] || []).flatMap((record): TrendSample[] => {
      const time = new Date(record.source_time || "").getTime()
      if (!Number.isFinite(time)) return []
      const base = { time, seriesId: id, seriesName: node ? nameOf(node) : id, color, units, reason: record.reason, late: record.late, received: record.received_time ? new Date(record.received_time).getTime() : undefined }
      if (record.kind === "quality_event") return [{ ...base, kind: "quality_event", quality: record.quality }]
      const value = Number(record.value)
      return [{ ...base, kind: "observation", quality: record.quality, value: Number.isFinite(value) ? value : undefined }]
    }).sort((a, b) => a.time - b.time)
    const valid = samples.filter((sample) => sample.kind === "observation" && sample.value !== undefined && sample.quality === "good")
    const recorded = samples.filter((sample) => sample.kind === "observation" && sample.value !== undefined)
    const last = valid[valid.length - 1] || recorded[recorded.length - 1]
    return { id, name: node ? nameOf(node) : id, color, units, cycleMs, latestValue: last?.value, latestTime: last?.time, latestQuality: last?.quality, samples }
  }), [selected, points, seriesData, queryResult])

  const qualityTotals = useMemo(() => {
    const totals: QualityCounts = { good: 0, stale: 0, invalid: 0, none: 0, events: 0 }
    for (const result of queryResult?.results || []) {
      for (const record of result.records || []) {
        if (record.kind === "quality_event") { totals.events += 1; continue }
        if (record.quality === "good") totals.good += 1
        else if (record.quality === "stale") totals.stale += 1
        else if (record.quality === "invalid") totals.invalid += 1
        else totals.none += 1
      }
    }
    return totals
  }, [queryResult])

  const qualityEntries = [
    { key: "good", label: "正常", value: qualityTotals.good },
    { key: "stale", label: "过期", value: qualityTotals.stale },
    { key: "invalid", label: "无效", value: qualityTotals.invalid },
    { key: "events", label: "质量事件", value: qualityTotals.events },
    { key: "none", label: "质量未记录", value: qualityTotals.none },
  ]
  const qualityTotal = qualityEntries.reduce((sum, entry) => sum + entry.value, 0)
  const hasUndeclaredCadence = Boolean(queryResult && queryResult.aggregate === "raw" && queryResult.results.some((result) => !result.sample_interval_ms))
  const hasLateSamples = Boolean(queryResult?.results.some((result) => result.records?.some((record) => record.late)))

  const alarmMarks = useMemo<AlarmMark[]>(() => {
    if (!showAlarms) return []
    const selectedSet = new Set(selected)
    return alarms
      .filter((alarm) => {
        const at = new Date(alarm.created_at).getTime()
        if (!Number.isFinite(at) || at < bounds.start.getTime() || at > bounds.end.getTime()) return false
        return selectedSet.has(alarm.point_id || alarm.device_id || "")
      })
      .map((alarm) => ({ at: new Date(alarm.created_at).getTime(), severity: alarm.severity, title: alarm.title }))
  }, [showAlarms, alarms, selected, bounds])

  const primary = series[0]
  const title = selected.length <= 1 ? (primary?.name || "选择点位") : `对比 ${selected.length} 个点位`

  function addPoint(id: string) {
    if (!id || selected.includes(id)) return
    setSelected([...selected, id])
  }

  function removePoint(id: string) {
    if (selected.length <= 1) return
    setSelected(selected.filter((item) => item !== id))
  }

  const saveQueries = (list: SavedQuery[]) => {
    setSavedQueries(list)
    localStorage.setItem(savedQueryKey, JSON.stringify(list))
  }

  const defaultQueryName = useMemo(() => {
    const parts = [
      ...selected.map((id) => points.find((item) => item.id === id) ? nameOf(points.find((item) => item.id === id)!) : id),
      ranges.find((item) => item.key === range)?.label || range,
      interval === "raw" ? "原始" : interval === "5m" ? "5 分钟" : "1 小时",
    ]
    return parts.filter(Boolean).join("·")
  }, [selected, points, range, interval])

  const matchedQuery = savedQueries.find((query) =>
    query.points.join("\u0000") === selected.join("\u0000") && query.range === range && query.interval === interval)

  function applyQuery(name: string) {
    const query = savedQueries.find((item) => item.name === name)
    if (!query) return
    setSelected(query.points.filter((id) => points.some((item) => item.id === id)))
    setRange(query.range)
    setInterval(query.interval)
    setQueryName(query.name)
  }

  function saveQuery() {
    const name = queryName.trim() || defaultQueryName
    saveQueries([...savedQueries.filter((query) => query.name !== name), { name, points: selected, range, interval }])
    setQueryName(name)
  }

  if (points.length === 0) {
    return <section className="history-page">
      <EmptyState title="还没有可查询的点位" description="先在工作台接入 Modbus 设备或添加测试变量，历史与趋势会从这里读取。" />
    </section>
  }

  return <section className="history-page min-w-0">
    {initialPoint && !points.some(item => item.id === initialPoint) && <InlineNotice tone="warning">指定的原点位不可用、已删除或不在当前权限范围内；未自动切换到其他点位。</InlineNotice>}
    <EmsCard className="!block !p-0"><IonCardContent className="grid min-w-0 gap-4">
      <div className="flex min-w-0 flex-wrap items-end gap-3">
        <div className="w-full min-w-0 sm:w-56"><EmsSelect label="对比点位" aria-label="添加对比点位" value="" onIonChange={(event) => addPoint(event.detail.value)}>
          <EmsSelectOption value="">＋ 添加点位…</EmsSelectOption>
          {points.map((item) => <EmsSelectOption key={item.id} value={item.id}>{nameOf(item)}</EmsSelectOption>)}
        </EmsSelect></div>
        <div className="history-series min-w-0 flex-1" role="list" aria-label="已选对比点位">
          {selected.map((id) => {
            const node = points.find((item) => item.id === id)
            const color = seriesColors[selected.indexOf(id) % seriesColors.length]
            return <span key={id} className="history-series-chip min-w-0 max-w-full" role="listitem">
              <i style={{ background: color }} />
              <b className="min-w-0 break-words">{node ? nameOf(node) : id}</b>
              <button aria-label={`移除 ${node ? nameOf(node) : id}`} disabled={selected.length <= 1} onClick={() => removePoint(id)}>×</button>
            </span>
          })}
        </div>
        <EmsButton variant="secondary" size="small" disabled={selected.length === 0 || loading} onClick={() => setQueryKey(key => key + 1)}>读取历史</EmsButton>
        {canExport && <EmsButton variant="secondary" size="small" disabled={!queryResult || selected.length === 0 || exportState.busy} onClick={() => void exportSelected()}>{exportState.busy ? "导出中…" : "导出所选 CSV"}</EmsButton>}
      </div>

      <div className="flex min-w-0 flex-wrap items-end gap-3">
        <div className="grid min-w-0 gap-1"><span className="ems-field-label text-ems-muted">时间范围</span><SegmentedTabs value={range} onChange={setRange} ariaLabel="时间范围" options={ranges.map((item) => ({ value: item.key, label: item.label }))} /></div>
        <div className="w-full min-w-0 sm:w-40"><EmsSelect label="聚合" aria-label="聚合" value={interval} onIonChange={(event) => setInterval(event.detail.value)}>
          <EmsSelectOption value="raw">原始</EmsSelectOption><EmsSelectOption value="5m">5 分钟平均</EmsSelectOption><EmsSelectOption value="1h">1 小时平均</EmsSelectOption>
        </EmsSelect></div>
        <label className="history-alarm-toggle min-h-10"><input type="checkbox" checked={showAlarms} onChange={(event) => setShowAlarms(event.target.checked)} />叠加报警</label>
      </div>

      {isAdmin && <div className="history-limit flex flex-wrap items-end gap-2">
        <div className="w-40"><EmsInput label="每个点位保留" aria-label="历史保留条数" type="number" min="1000" max="200000" step="1000" value={String(limitDraft)} onIonInput={(event) => setLimitDraft(Number(event.detail.value))} /></div>
        <span className="pb-3 text-xs text-ems-muted">条</span>
        <EmsButton variant="secondary" size="small" busy={limitBusy} busyLabel="保存中…" onClick={() => void saveLimit()}>保存</EmsButton>
        {limitNotice && <small className="w-full !whitespace-normal">{limitNotice}</small>}
      </div>}

      <div className="flex min-w-0 flex-wrap items-end gap-2">
        {savedQueries.length > 0 && <div className="w-full min-w-0 sm:w-56"><EmsSelect label="已保存查询" aria-label="保存的查询" value="" onIonChange={(event) => applyQuery(event.detail.value)}>
          <EmsSelectOption value="">查询/模板…</EmsSelectOption>
          {savedQueries.map((query) => <EmsSelectOption key={query.name} value={query.name}>{query.name}</EmsSelectOption>)}
        </EmsSelect></div>}
        <div className="w-full min-w-0 sm:w-56"><EmsInput label="查询名称" aria-label="查询名称" value={queryName} onIonInput={(event) => setQueryName(event.detail.value || "")} placeholder="查询/模板名称" /></div>
        {matchedQuery
          ? <EmsButton variant="secondary" size="small" onClick={() => saveQueries(savedQueries.filter((query) => query !== matchedQuery))}>删查询</EmsButton>
          : <EmsButton variant="secondary" size="small" disabled={selected.length === 0} onClick={saveQuery}>保存查询</EmsButton>}
      </div>
      <span className="history-hint !m-0">每次每点最多 2000 条；完整导出最多 10000 条，超限会明确拒绝。区间包含起止两端点，来源与观测/接收时间分别保留。</span>
      {queryResult && <div className="history-scope" data-testid="history-scope">
        <strong>所选时间窗：{queryResult.bounds.start.toLocaleString()} 至 {queryResult.bounds.end.toLocaleString()}（本地时区，包含两端点）</strong>
        {queryResult.results.map(result => <div key={result.node_id}>
          {points.find(item => item.id === result.node_id)?.points.find(p => p.type === "description")?.text || result.node_id}：{result.truncated ? `本次返回 ${result.returned} 条，仍有未取回数据` : `已返回所选范围内全部 ${result.returned} 条`}；来源 {result.source}；筛选 {queryResult.aggregate}；{result.sample_interval_ms ? `声明采样间隔 ${result.sample_interval_ms} ms` : "采样间隔未声明"}
          {result.truncated && <EmsButton variant="quiet" size="small" disabled={moreLoading} onClick={() => void loadMore()}>{moreLoading ? "继续读取中…" : "继续读取更早记录"}</EmsButton>}
        </div>)}
      </div>}
      {queryError && <InlineNotice tone="danger">查询失败：{queryError}，未显示为无数据。</InlineNotice>}
      {exportState.message && <InlineNotice tone={exportState.error ? "danger" : "success"}>{exportState.message}</InlineNotice>}
      {!queryResult && !loading && !queryError && <p className="text-ems-muted">请选择点位并点击“读取历史”，固定本次时间窗。</p>}
    </IonCardContent></EmsCard>

    <EmsCard className="trend-panel min-w-0">
      <EmsSectionHeader kicker="最近变化" title={title} actions={selected.length === 1 && primary ? <div className="trend-value">
        {primary.latestValue === undefined ? <span className="trend-no-valid">无有效观测</span> : <>{primary.latestValue}<small>{primary.units}</small></>}
        {primary.latestTime ? <span className="trend-asof">{primary.latestQuality
          ? `最后有效观测 ${new Date(primary.latestTime).toLocaleString("zh-CN", { hour12: false })}，非当前正常值`
          : `最后记录值 ${new Date(primary.latestTime).toLocaleString("zh-CN", { hour12: false })}，质量未记录，不能作为有效观测`}</span> : undefined}
      </div> : undefined} />
      <div className="trend-body mt-4">
        <div className="trend-main">
          {loading ? <InlineNotice tone="busy">正在读取历史…</InlineNotice> : queryError ? <InlineNotice tone="warning">本次查询未成功，不能将无结果视为无数据。</InlineNotice> : <Trend series={series} alarms={alarmMarks} start={bounds.start.getTime()} end={bounds.end.getTime()} onOpenPoints={onOpenPoints} />}
        </div>
        <aside className="quality-summary min-w-0" aria-label="数据质量摘要">
          <EmsSectionHeader kicker="数据质量" title="所选时间段" />
          {qualityTotal === 0
            ? <p className="quality-empty">所选范围内没有样本或质量记录。</p>
            : <ul className="quality-bars">
              {qualityEntries.map((entry) => {
                const ratio = qualityTotal ? Math.round((entry.value / qualityTotal) * 100) : 0
                return <li key={entry.key} className={`quality-row ${entry.key}`}>
                  <span className="quality-name">{entry.label}</span>
                  <span className="quality-track"><i style={{ width: `${ratio}%` }} /></span>
                  <span className="quality-count">{entry.value}<em>{ratio}%</em></span>
                </li>
              })}
              <li className="quality-total">共 {qualityTotal} 条记录</li>
            </ul>}
          <p className="quality-note">质量来自同一历史查询；质量事件是读失败或过期记录，不是测量值。</p>
        </aside>
      </div>
      <div className="trend-note mt-3 grid gap-2">
        {hasUndeclaredCadence && <InlineNotice tone="info">该数据源未声明采样间隔，不推断缺口；只显示实际样本与明确质量事件。</InlineNotice>}
        {hasLateSamples && <InlineNotice tone="warning">迟到样本按真实观测时间补入，未参与当时实时控制，也不改变当前状态。</InlineNotice>}
        <InlineNotice tone="info">缺失段不插值；缺口依据声明采样周期的 3 倍或明确质量事件。过期或无效的数据不会冒充实时值。</InlineNotice>
      </div>
    </EmsCard>
  </section>
}
