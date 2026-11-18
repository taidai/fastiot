import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { IonCardContent } from "@ionic/react"
import { ackAlarm, AlarmEvent, closeAlarm, EmsAlarm, maintainAlarm, nameOf, Node, releaseAlarm, request } from "./api"
import AlarmTypesPanel from "./AlarmTypesPanel"
import AlarmStatsPanel, { AlarmSoundSettings, inSilenceWindow, loadAlarmSound, saveAlarmSound } from "./AlarmStatsPanel"
import AckDialog from "./AckDialog"
import { EmsButton, EmsCard, EmsInput, EmsOverlay, EmsSelect, EmsSelectOption, EmptyState, FilterToolbar, InlineNotice, MetricStrip, SegmentedTabs, StatusBadge, type EmsTone } from "./components/ems"
import { Actions } from "./capabilities"
import { useCapabilities } from "./capabilitiesContext"

const severityNames: Record<string, string> = { critical: "严重", warning: "重要", info: "提示" }
const stateNames: Record<string, string> = {
  active: "活动 · 未确认",
  acknowledged: "活动 · 已确认",
  resolved: "已恢复 · 待关闭",
  closed: "已关闭",
}

const severityTone: Record<string, EmsTone> = { critical: "danger", warning: "warning", info: "info" }
const stateTone: Record<string, EmsTone> = { active: "danger", acknowledged: "warning", resolved: "success", closed: "neutral" }

function timeOf(text: string) {
  if (!text) return "—"
  return new Date(text).toLocaleString("zh-CN", { hour12: false })
}

export default function AlarmsView({ token, rootID, nodes, alarms, loading, queryError, onRefreshAlarms, canOperate, canConfigure, onError, openHistory }: {
  token: string
  rootID: string
  nodes: Node[]
  alarms: EmsAlarm[]
  loading: boolean
  queryError: string
  onRefreshAlarms: () => Promise<void>
  canOperate: boolean
  canConfigure: boolean
  onError: (message: string) => void
  openHistory: (pointId: string) => void
}) {
  const { can } = useCapabilities()
  const [tab, setTab] = useState<"records" | "types" | "stats">("records")
  const [filter, setFilter] = useState<"active" | "all">("active")
  const [busyId, setBusyId] = useState("")
  const [sound, setSound] = useState<AlarmSoundSettings>(() => loadAlarmSound())
  const audioRef = useRef<AudioContext | null>(null)
  const soundedRef = useRef<Set<string>>(new Set())
  const [maintaining, setMaintaining] = useState<EmsAlarm | null>(null)
  const [ackTarget, setAckTarget] = useState<EmsAlarm | null>(null)
  const [maintenanceReason, setMaintenanceReason] = useState("")
  const [maintenanceMinutes, setMaintenanceMinutes] = useState(120)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [detailID, setDetailID] = useState("")
  const [events, setEvents] = useState<AlarmEvent[]>([])
  const [eventsLoading, setEventsLoading] = useState(false)
  const [eventsError, setEventsError] = useState("")
  const detail = alarms.find(alarm => alarm.id === detailID)
  const closeDetails = useCallback(() => setDetailID(""), [])
  const closeMaintenance = useCallback(() => setMaintaining(null), [])
  useEffect(() => {
    if (loading || queryError) { setAckTarget(null); setMaintaining(null); setDetailID(""); return }
    if (ackTarget && !alarms.some(a => a.id === ackTarget.id && a.permissions?.ack && a.active && a.state === "active")) setAckTarget(null)
    if (maintaining && !alarms.some(a => a.id === maintaining.id && a.permissions?.maintain)) setMaintaining(null)
  }, [alarms, loading, queryError, ackTarget, maintaining])
  useEffect(() => {
    let cancelled = false
    const controller = new AbortController()
    setEvents([]); setEventsError("")
    if (!detail) return
    setEventsLoading(true)
    void request<{ events: AlarmEvent[] }>(token, `/alarm-events?occurrence_id=${encodeURIComponent(detail.id)}`, "GET", undefined, AbortSignal.any([controller.signal, AbortSignal.timeout(5000)]))
      .then(result => { if (!cancelled) setEvents(result.events) })
      .catch(error => { if (!cancelled) setEventsError((error as Error).message) })
      .finally(() => { if (!cancelled) setEventsLoading(false) })
    return () => { cancelled = true; controller.abort() }
  }, [token, detail?.id, detail?.revision])

  const names = new Map(nodes.map((node) => [node.id, nameOf(node)]))
  const nameOfDevice = (id?: string) => (id ? names.get(id) || id : "—")
  const objectName = (alarm: EmsAlarm) => alarm.point_id ? nameOfDevice(alarm.point_id) : alarm.target?.kind === "device" ? `${nameOfDevice(alarm.target.channel_id)} / 站号 ${alarm.target.server_id}` : nameOfDevice(alarm.target?.node_id || alarm.device_id)
  const relatedPoints = (alarm: EmsAlarm) => (alarm.target?.point_ids?.length ? alarm.target.point_ids : nodes.filter(n => n.type === "modbusIo" && n.parent === alarm.target?.node_id).map(n => n.id)).filter(id => nodes.some(n => n.id === id && (n.type === "modbusIo" || n.type === "variable")))

  // 页面标题与设备树都按站点裁剪，报警列表也必须一致：只展示引用到当前站点节点（或无引用）的报警。
  const scopeIDs = useMemo(() => new Set(nodes.map((node) => node.id)), [nodes])
  const scopedAlarms = useMemo(() => alarms.filter((alarm) => {
    const refs = [alarm.point_id, alarm.target?.node_id, ...(alarm.target?.point_ids || []), alarm.device_id].filter(Boolean) as string[]
    return refs.some((id) => scopeIDs.has(id))
  }), [alarms, scopeIDs])

  function updateSound(next: AlarmSoundSettings) {
    setSound(next)
    saveAlarmSound(next)
  }

  function playTone() {
    try {
      const Ctor = window.AudioContext || (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
      if (!Ctor) return
      let context = audioRef.current
      if (!context || context.state === "closed") {
        context = new Ctor()
        audioRef.current = context
      }
      if (context.state === "suspended") void context.resume()
      const start = context.currentTime
      const gainValue = Math.max(0.001, (sound.volume / 100) * 0.35)
      for (const [index, frequency] of [880, 660].entries()) {
        const oscillator = context.createOscillator()
        const gain = context.createGain()
        oscillator.type = "sine"
        oscillator.frequency.value = frequency
        const at = start + index * 0.2
        gain.gain.setValueAtTime(0.0001, at)
        gain.gain.exponentialRampToValueAtTime(gainValue, at + 0.02)
        gain.gain.exponentialRampToValueAtTime(0.0001, at + 0.17)
        oscillator.connect(gain)
        gain.connect(context.destination)
        oscillator.start(at)
        oscillator.stop(at + 0.18)
      }
    } catch {
      // Browsers may block audio until a user gesture; the test button unlocks it.
    }
  }

  useEffect(() => {
    const active = scopedAlarms.filter((alarm) => alarm.active && !(alarm.maintenance_until && new Date(alarm.maintenance_until).getTime() > Date.now()))
    const keys = new Set(active.map((alarm) => alarm.id))
    for (const key of soundedRef.current) if (!keys.has(key)) soundedRef.current.delete(key)
    if (!sound.enabled || inSilenceWindow(sound)) return
    const fresh = active.filter((alarm) => !soundedRef.current.has(alarm.id))
    if (fresh.length === 0) return
    for (const alarm of fresh) soundedRef.current.add(alarm.id)
    playTone()
  }, [scopedAlarms, sound])

  async function act(alarm: EmsAlarm, action: "ack" | "close" | "release") {
    if (action === "ack") { setAckTarget(alarm); return }
    setBusyId(alarm.id)
    try {
      if (action === "close") await closeAlarm(token, alarm.id)
      else await releaseAlarm(token, alarm.id)
      await onRefreshAlarms()
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "操作失败")
    } finally {
      setBusyId("")
    }
  }

  async function submitMaintenance() {
    if (!maintaining) return
    if (!maintenanceReason.trim()) {
      onError("请填写维护原因")
      return
    }
    setBusyId(maintaining.id)
    try {
      const until = new Date(Date.now() + maintenanceMinutes * 60000).toISOString()
      await maintainAlarm(token, maintaining.id, until, maintenanceReason.trim())
      setMaintaining(null)
      setMaintenanceReason("")
      await onRefreshAlarms()
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "维护设置失败")
    } finally {
      setBusyId("")
    }
  }

  function isMaintained(alarm: EmsAlarm) {
    return Boolean(alarm.maintenance_until && new Date(alarm.maintenance_until).getTime() > Date.now())
  }

  const needsAttention = (alarm: EmsAlarm) => alarm.active && !isMaintained(alarm)
  const active = scopedAlarms.filter(needsAttention)
  const unacked = active.filter((alarm) => alarm.state === "active")
  const maintained = scopedAlarms.filter((alarm) => alarm.active && isMaintained(alarm))
  const resolved = scopedAlarms.filter((alarm) => !alarm.active && alarm.state !== "closed")

  const childrenByGroup = new Map<string, EmsAlarm[]>()
  for (const alarm of scopedAlarms) {
    if (!alarm.group_id) continue
    const list = childrenByGroup.get(alarm.group_id) || []
    list.push(alarm)
    childrenByGroup.set(alarm.group_id, list)
  }
  const visibleChildren = (id: string) => (childrenByGroup.get(id) || []).filter((child) => filter === "all" || needsAttention(child))
  const shown = scopedAlarms.filter((alarm) => !alarm.group_id && (filter === "all" || needsAttention(alarm) || visibleChildren(alarm.id).length > 0))

  function toggleGroup(id: string) {
    setExpanded((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function exportAlarms() {
    const rows = [["时间", "级别", "标题", "详情", "对象", "根因", "次数", "状态", "确认人", "处理说明"]]
    for (const alarm of scopedAlarms) {
      rows.push([timeOf(alarm.created_at), alarm.severity, alarm.title, alarm.detail || "", nameOfDevice(alarm.device_id), nameOfDevice(alarm.root_cause), String(alarm.count || 1), alarm.state, alarm.acked_by || "", alarm.ack_note || ""])
    }
    const csv = rows.map((row) => row.map((cell) => `"${String(cell).replaceAll('"', '""')}"`).join(",")).join(String.fromCharCode(10))
    const url = URL.createObjectURL(new Blob([String.fromCharCode(0xFEFF) + csv], { type: "text/csv;charset=utf-8" }))
    const link = document.createElement("a")
    link.href = url
    link.download = "fastiot-alarms.csv"
    link.click()
    URL.revokeObjectURL(url)
  }

  function renderCard(alarm: EmsAlarm, isChild: boolean) {
    const children = isChild ? [] : visibleChildren(alarm.id)
    const isOpen = expanded.has(alarm.id)
    return <div className="min-w-0" key={alarm.id}>
      <EmsCard className={`alarm-card ${alarm.severity} ${alarm.state} min-w-0`} data-alarm-id={alarm.id}>
        <div className="alarm-head">
          <StatusBadge tone={severityTone[alarm.severity] || "neutral"}>{severityNames[alarm.severity] || alarm.severity}</StatusBadge>
          <strong className="min-w-0 break-words">{alarm.title}</strong>
          {(alarm.count || 0) > 1 && <StatusBadge tone="neutral" dot={false}>×{alarm.count}</StatusBadge>}
          <span className="alarm-state"><StatusBadge tone={stateTone[alarm.state] || "neutral"} dot={false}>{stateNames[alarm.state] || alarm.state}</StatusBadge></span>
        </div>
        <p className="min-w-0 break-words">{alarm.detail || "没有更多说明"}</p>
        <div className="alarm-meta min-w-0 gap-x-4 gap-y-1">
          <span className="min-w-0 break-words">对象 {objectName(alarm)}</span>
          <span>首次发生 {timeOf(alarm.created_at)}</span>
          <span>最近触发 {timeOf(alarm.last_raised_at || alarm.created_at)}</span>
          {alarm.acked_by && <span className="min-w-0 break-words">接手 {alarm.acked_by} · {timeOf(alarm.acked_at || "")}{alarm.ack_note ? ` · ${alarm.ack_note}` : ""}</span>}
          {alarm.resolved_at && <span>有效恢复 {timeOf(alarm.resolved_at)}</span>}
          {alarm.closed_at && <span>{alarm.input_reason?.startsWith("definition_") ? "配置处置" : "关闭"} {timeOf(alarm.closed_at)}</span>}
          <span className="break-all">过程 {alarm.id}</span>
        </div>
        {isMaintained(alarm) && <InlineNotice tone="warning">维护中 · 至 {timeOf(alarm.maintenance_until || "")} · {alarm.maintenance_reason || "未填写原因"} · {alarm.maintained_by || "—"}</InlineNotice>}
        {alarm.input_state === "unknown" && <InlineNotice tone="warning">{alarm.input_reason?.startsWith("definition_") ? "报警定义已停用或移除；不代表物理异常已恢复。" : "原输入不可用，恢复尚未确认；接手状态不变。"}</InlineNotice>}
        <div className="alarm-drilldown flex flex-wrap gap-2">
          <EmsButton variant="quiet" size="small" onClick={() => setDetailID(alarm.id)}>查看过程与原对象</EmsButton>
          {alarm.point_id && scopeIDs.has(alarm.point_id) && <EmsButton variant="quiet" size="small" onClick={() => openHistory(alarm.point_id!)}>查看原点位历史</EmsButton>}
          {!alarm.point_id && <span>设备/通道范围报警，未指定单一点位。</span>}
        </div>
        {(alarm.permissions?.ack || alarm.permissions?.maintain) && <div className="alarm-actions flex flex-wrap gap-2 border-t border-ems-line pt-3">
          {alarm.permissions?.ack && alarm.active && alarm.state === "active" && <EmsButton size="small" disabled={busyId === alarm.id || !ready} onClick={() => void act(alarm, "ack")}>确认接手</EmsButton>}
          {alarm.permissions?.maintain && !isMaintained(alarm) && alarm.state !== "closed" && <EmsButton variant="secondary" size="small" disabled={busyId === alarm.id || !ready} onClick={() => { setMaintaining(alarm); setMaintenanceReason(""); setMaintenanceMinutes(120) }}>维护</EmsButton>}
          {alarm.permissions?.maintain && isMaintained(alarm) && <EmsButton variant="secondary" size="small" disabled={busyId === alarm.id || !ready} onClick={() => void act(alarm, "release")}>结束维护</EmsButton>}
          {alarm.permissions?.ack && !alarm.active && alarm.state !== "closed" && <EmsButton variant="secondary" size="small" disabled={busyId === alarm.id || !ready} onClick={() => void act(alarm, "close")}>关闭记录</EmsButton>}
        </div>}
      </EmsCard>
      {!isChild && children.length > 0 && <div className="mt-2 grid gap-2">
        <div><EmsButton variant="quiet" size="small" onClick={() => toggleGroup(alarm.id)}>{isOpen ? "收起同根因报警" : `展开同根因 ${children.length} 条`}</EmsButton></div>
        {isOpen && <div className="alarm-children grid min-w-0 gap-3 border-l border-ems-line pl-3 sm:pl-6">{children.map((child) => renderCard(child, true))}</div>}
      </div>}
    </div>
  }

  const tabs: { value: "records" | "types" | "stats"; label: string }[] = [
    { value: "records", label: "报警记录" },
    ...(canConfigure ? [{ value: "types" as const, label: "类型管理" }] : []),
    ...(canConfigure || canOperate ? [{ value: "stats" as const, label: "统计与提醒" }] : []),
  ]

  const ready = !loading && !queryError
  return <section className="alarm-page min-w-0">
    {loading && <InlineNotice tone="info">正在读取当前范围的报警，状态尚未确认。</InlineNotice>}
    {queryError && <InlineNotice tone="danger">报警读取失败，当前状态不可确认：{queryError}<EmsButton onClick={() => void onRefreshAlarms()}>重新读取报警</EmsButton></InlineNotice>}
    <div className="alarm-view-tabs min-w-0">
      <SegmentedTabs value={tab} onChange={setTab} ariaLabel="报警与类型" options={tabs} />
    </div>
    {tab === "types" ? <AlarmTypesPanel token={token} rootID={rootID} nodes={nodes} canConfigure={canConfigure} onError={onError} /> : tab === "stats" ? <AlarmStatsPanel token={token} onError={onError} sound={sound} onSound={updateSound} onTest={playTone} /> : <>
      <EmsCard><IonCardContent className="grid min-w-0 gap-4">
        <MetricStrip items={[
          { label: "活动报警", value: ready ? active.length : "—", tone: !ready ? "neutral" : active.length ? "danger" : "neutral" },
          { label: "未确认", value: ready ? unacked.length : "—", tone: unacked.length ? "warning" : "neutral" },
          { label: "已恢复待关闭", value: ready ? resolved.length : "—" },
          { label: "维护中", value: ready ? maintained.length : "—", tone: maintained.length ? "warning" : "neutral" },
        ]} />
        <FilterToolbar trailing={can(Actions.export) ? <EmsButton variant="secondary" size="small" onClick={exportAlarms}>导出 CSV</EmsButton> : undefined}>
          <SegmentedTabs value={filter} onChange={setFilter} ariaLabel="报警记录筛选" options={[
            { value: "active", label: "只看需要处理" },
            { value: "all", label: "全部记录" },
          ]} />
        </FilterToolbar>
      </IonCardContent></EmsCard>

      <div className="alarm-list min-w-0">
        {shown.map((alarm) => renderCard(alarm, false))}
        {ready && shown.length === 0 && <EmptyState
          title={filter === "active" ? "当前筛选下没有待处理报警" : "当前范围内暂无报警记录"}
          description="报警列表为空不证明设备正常，请结合原对象观测与质量判断。"
        />}
      </div>
    </>}

    <EmsOverlay open={!!detail && ready} title="报警过程与原对象" onClose={closeDetails}
      actions={<EmsButton variant="secondary" onClick={closeDetails}>关闭详情</EmsButton>}>
      {detail && <div className="grid gap-3 break-words">
        <h3>{detail.title}</h3><p>原对象：{objectName(detail)}</p>
        <p className="break-all">过程 {detail.id}；定义条件 {detail.key}</p>
        <p>首次 {timeOf(detail.created_at)}；最近触发 {timeOf(detail.last_raised_at || detail.created_at)}</p>
        {!detail.point_id && <p>这是设备/通道范围报警，请明确选择相关点位；不会替你选择其他最近点位。</p>}
        {detail.target?.mapping_changed && <InlineNotice tone="warning">原点位的设备映射已变化；保留原设备身份 {detail.device_id}，不自动定位到新设备。点位历史仍按原点位 ID 查询。</InlineNotice>}
        <h4>当前可见的原对象相关点位</h4>
        {relatedPoints(detail).length ? relatedPoints(detail).map(id => <EmsButton key={id} variant="secondary" onClick={() => { closeDetails(); openHistory(id) }}>查看 {nameOfDevice(id)} 历史</EmsButton>) : <p>没有可定位的相关点位，或原对象已删除/不在权限范围内。</p>}
        <h4>过程记录</h4>
        {eventsLoading && <p>正在读取该过程记录…</p>}
        {eventsError && <InlineNotice tone="danger">过程记录读取失败：{eventsError}</InlineNotice>}
        {!eventsLoading && !eventsError && events.length === 0 && <p>暂无可读取的过程记录。</p>}
        <ol className="grid gap-3">{events.map((event, index) => <li key={`${event.revision}/${index}`}>
          <strong>{({ raised: "发生", changed: "条件/级别变化", acknowledged: "确认接手", resolved: "有效观测恢复", closed: "关闭", retired: "配置处置（非物理恢复）", input_unknown: "输入不可用", maintenance: "进入维护", released: "结束维护" } as Record<string,string>)[event.transition] || event.transition}</strong> · {timeOf(event.time)} · {severityNames[event.severity] || event.severity}
          <p>{event.detail}</p>
          {event.input && <div><p>原始依据：{event.input.kind === "bool" ? `${Boolean(event.input.value)}（判定值 ${event.input.value}）` : event.input.value} {event.input.unit}；质量 {event.input.quality}；来源 {event.input.source === "observation" ? "采集观测" : "旧式变量"}</p><p>原观测 {timeOf(event.input.source_time || "")}；接收 {timeOf(event.input.received_time || "")}</p><small className="break-all">点位 {event.input.point_id}；采集配置 {event.input.config_stamp || "非物理采集"}</small></div>}
          {event.acked_by && <p>处理人 {event.acked_by}；{event.ack_note || "未填写说明"}</p>}{event.maintained_by && <p>维护人 {event.maintained_by}；{event.maintenance_reason}</p>}
        </li>)}</ol>
      </div>}
    </EmsOverlay>

    {ackTarget && ready && alarms.some(a => a.id === ackTarget.id && a.permissions?.ack) && <AckDialog
      alarm={ackTarget}
      busy={busyId === ackTarget.id}
      onCancel={() => setAckTarget(null)}
      onConfirm={(note) => {
        const target = ackTarget
        setAckTarget(null)
        setBusyId(target.id)
        void ackAlarm(token, target.id, note).then(() => onRefreshAlarms()).catch((cause) => onError(cause instanceof Error ? cause.message : "操作失败")).finally(() => setBusyId(""))
      }}
    />}

    <EmsOverlay open={Boolean(maintaining) && !!ready && alarms.some(a => a.id === maintaining?.id && a.permissions?.maintain)} title={maintaining?.title || "报警维护"} busy={busyId === maintaining?.id} onClose={closeMaintenance}
      actions={<>
        <EmsButton variant="secondary" disabled={busyId === maintaining?.id} onClick={() => setMaintaining(null)}>取消</EmsButton>
        <EmsButton busy={busyId === maintaining?.id} busyLabel="保存中…" disabled={!maintenanceReason.trim()} onClick={() => void submitMaintenance()}>开始维护</EmsButton>
      </>}>
      <div className="grid min-w-0 gap-4">
        <InlineNotice tone="info">维护期内该报警条件不会重新产生打扰；已有记录保留。维护结束后若条件仍存在，会重新出现。</InlineNotice>
        <EmsSelect label="维护时长" aria-label="维护时长" value={maintenanceMinutes} onIonChange={(event) => setMaintenanceMinutes(Number(event.detail.value))}>
          <EmsSelectOption value={30}>30 分钟</EmsSelectOption><EmsSelectOption value={120}>2 小时</EmsSelectOption><EmsSelectOption value={480}>8 小时</EmsSelectOption><EmsSelectOption value={1440}>24 小时</EmsSelectOption>
        </EmsSelect>
        <EmsInput label="维护原因" aria-label="维护原因" autoFocus value={maintenanceReason} onIonInput={(event) => setMaintenanceReason(event.detail.value || "")} placeholder="如 检修、备件更换" />
      </div>
    </EmsOverlay>
  </section>
}
