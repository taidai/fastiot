import { useCallback, useEffect, useRef, useState } from "react"
import { EmsButton, InlineNotice } from "./components/ems"
import { request } from "./api"
import ScreenRevision from "./ScreenRevision"

type Point = { id: string; name: string; kind: string; unit?: string; writable: boolean }
type Finding = {
  code: string
  origin: string
  severity: string
  message: string
  binding?: string
  element?: string
  selector?: string
  line?: number
  column?: number
  detail?: Record<string, number>
}
type Report = { findings: Finding[]; ready: boolean; blocking: number; warnings: number }
type SimValue = { value: number | string | boolean | null; time: string; quality: string; simulated: boolean }
type Session = { id: string; capability: string; frame_url: string; points: Point[]; report: Report; simulated: boolean }
type WriteLog = { point: string; value: number; status: string; reason?: string }

const codeText: Record<string, string> = {
  binding_unknown: "点位不存在",
  binding_out_of_scope: "点位越权",
  binding_not_selected: "点位未包含在本次范围",
  write_read_only: "只读点位不能写",
  write_value_invalid: "写值无效",
  write_element_not_button: "写绑定应放在按钮上",
  forbidden_api: "使用了隔离环境禁止的能力",
  external_resource: "引用外部资源",
  inline_handler: "内联事件不会执行",
  too_many_bindings: "绑定数量过多",
  runtime_error: "运行错误",
  layout_overflow: "布局溢出",
  layout_clipped: "内容被裁切",
  layout_invisible: "绑定元素不可见",
  layout_offscreen: "元素在可视区域之外",
}

function findingLocation(finding: Finding) {
  const parts: string[] = []
  if (finding.line) parts.push(`第 ${finding.line} 行${finding.column ? `:${finding.column}` : ""}`)
  if (finding.binding) parts.push(`绑定 ${finding.binding}`)
  if (finding.selector) parts.push(finding.selector)
  return parts.join(" · ")
}

// GeneratedScreenPreview runs the model's document in a sandboxed frame. The
// frame has an opaque origin, no network and no credential; values and writes
// cross only through this page, and writes go to the preview simulator.
export default function GeneratedScreenPreview({ token, nodeID, pointIDs, source }: {
  token: string
  nodeID: string
  pointIDs: string[]
  source: string
}) {
  const frame = useRef<HTMLIFrameElement>(null)
  const loadToken = useRef("")
  const findings = useRef<Finding[]>([])
  const firstValues = useRef(true)
  const submitTimer = useRef<number | null>(null)
  const [session, setSession] = useState<Session | null>(null)
  const [report, setReport] = useState<Report | null>(null)
  const [checksRan, setChecksRan] = useState(false)
  const [writes, setWrites] = useState<WriteLog[]>([])
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [confirming, setConfirming] = useState(false)
  const [revision, setRevision] = useState(0)
  const pointKey = pointIDs.join(",")

  useEffect(() => {
    let cancelled = false
    request<Session>(token, "/generated-screens/preview", "POST", {
      node_id: nodeID, source, point_ids: pointKey.split(",").filter(Boolean),
    })
      .then((result) => {
        if (cancelled) return
        findings.current = []
        firstValues.current = true
        setWrites([])
        setChecksRan(false)
        setRevision(0)
        setSession(result)
        setReport(result.report)
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : "预览创建失败")
      })
    return () => { cancelled = true }
  }, [token, nodeID, source, pointKey])

  const post = useCallback((message: Record<string, unknown>) => {
    frame.current?.contentWindow?.postMessage({ ...message, token: loadToken.current }, "*")
  }, [])

  // A revision replaces the preview source in place. The frame URL gains the
  // revision so the sandbox reloads, and the browser checks must run again
  // before the publish gate accepts the modified document.
  const applyRevision = useCallback((applied: { revision: number; frame_url: string; report: Report; changes: unknown[] }) => {
    findings.current = []
    firstValues.current = true
    setWrites([])
    setChecksRan(false)
    setSession((current) => current ? { ...current, frame_url: applied.frame_url, report: applied.report } : current)
    setReport(applied.report)
    setRevision(applied.revision)
    setNotice(`已应用修改（${applied.changes?.length || 0} 处变化）；需要在预览中重新检查后才能发布`)
    setError("")
  }, [])

  const submitFindings = useCallback((delay = 0) => {
    if (!session) return
    if (submitTimer.current) window.clearTimeout(submitTimer.current)
    submitTimer.current = window.setTimeout(() => {
      const current = session
      request<{ report: Report }>(token, `/generated-screens/${current.id}/diagnostics`, "POST", {
        findings: findings.current.map((finding) => ({
          code: finding.code, origin: finding.origin, severity: finding.severity, message: finding.message,
          binding: finding.binding || "", element: finding.element || "", selector: finding.selector || "",
          line: finding.line || 0, column: finding.column || 0, detail: finding.detail,
        })),
      })
        .then((result) => { setReport(result.report); setChecksRan(true); setError("") })
        .catch((cause) => setError(cause instanceof Error ? cause.message : "检查结果提交失败"))
    }, delay)
  }, [session, token])

  const replaceFindings = useCallback((incoming: Finding[], delay = 300) => {
    const runtime = incoming.filter((finding) => finding.origin === "runtime")
    const layout = incoming.filter((finding) => finding.origin === "layout")
    const keep = findings.current.filter((finding) => finding.origin !== "runtime" && finding.origin !== "layout")
    findings.current = [...keep, ...runtime, ...layout]
    submitFindings(delay)
  }, [submitFindings])

  const fetchValues = useCallback(async (current: Session) => {
    try {
      const result = await request<{ values: Record<string, SimValue> }>(token, `/generated-screens/${current.id}/read`, "POST", {
        ids: current.points.map((point) => point.id),
      })
      frame.current?.contentWindow?.postMessage({ type: "fastiot:values", token: loadToken.current, values: result.values }, "*")
      if (firstValues.current) {
        firstValues.current = false
        window.setTimeout(() => post({ type: "fastiot:check" }), 300)
      }
    } catch {
      /* a polling failure leaves the last simulated values in place */
    }
  }, [token, post])

  useEffect(() => {
    if (!session) return
    const interval = window.setInterval(() => { void fetchValues(session) }, 1500)
    return () => window.clearInterval(interval)
  }, [session, fetchValues])

  useEffect(() => () => {
    if (submitTimer.current) window.clearTimeout(submitTimer.current)
  }, [])

  useEffect(() => {
    function onMessage(event: MessageEvent) {
      const target = frame.current?.contentWindow
      if (!target || event.source !== target) return
      const data = event.data
      if (!data || typeof data !== "object" || data.token !== loadToken.current) return
      if (!session) return
      switch (data.type) {
        case "fastiot:ready":
          replaceFindings(Array.isArray(data.findings) ? data.findings : [])
          void fetchValues(session)
          window.setTimeout(() => post({ type: "fastiot:check" }), 400)
          break
        case "fastiot:findings":
          replaceFindings(Array.isArray(data.findings) ? data.findings : [])
          break
        case "fastiot:error":
          if (data.finding) {
            const incoming = data.finding as Finding
            const kept = findings.current.filter((item) => item.origin !== "runtime" ||
              (item.message !== incoming.message || item.line !== incoming.line))
            findings.current = [...kept, incoming].slice(-50)
            submitFindings(200)
          }
          break
        case "fastiot:write": {
          const binding = String(data.binding || "")
          const value = Number(data.value)
          if (!pointKey.split(",").includes(binding) || !Number.isFinite(value)) return
          request<{ status: string; entry: { point: string; value: number; status: string } }>(
            token, `/generated-screens/${session.id}/write`, "POST", { point_id: binding, value },
          )
            .then((result) => {
              setWrites((current) => [{ point: result.entry.point, value: result.entry.value, status: result.entry.status }, ...current].slice(0, 5))
              post({ type: "fastiot:write-result", requestId: data.requestId, status: result.status, value: result.entry.value })
            })
            .catch((cause) => {
              setWrites((current) => [{ point: binding, value, status: "rejected", reason: cause instanceof Error ? cause.message : "" }, ...current].slice(0, 5))
              post({ type: "fastiot:write-result", requestId: data.requestId, status: "rejected" })
            })
          break
        }
        case "fastiot:locate-result":
          setNotice(data.found ? "已在画面中定位该元素" : "该元素在画面中不存在")
          break
      }
    }
    window.addEventListener("message", onMessage)
    return () => window.removeEventListener("message", onMessage)
  }, [session, pointKey, token, post, fetchValues, replaceFindings, submitFindings])

  function onFrameLoad() {
    loadToken.current = Math.random().toString(36).slice(2) + Date.now().toString(36)
    post({ type: "fastiot:init" })
  }

  async function confirm() {
    if (!session) return
    setConfirming(true)
    setError("")
    setNotice("")
    try {
      await request<{ confirmed: boolean }>(token, `/generated-screens/${session.id}/confirm`, "POST", {})
      setNotice("检查通过，画面源码已保存为工程草稿；发布到运行版属于后续步骤")
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "确认发布被拒绝")
    } finally {
      setConfirming(false)
    }
  }

  return <div className="flex flex-col gap-3">
    <div className="flex flex-wrap items-center gap-2 text-sm">
      <span className="rounded-full border border-ems-line px-2 py-0.5 text-xs">模拟设备</span>
      <span className="text-xs text-ems-muted">预览中的读写只发生在模拟设备上，不会发送到现场设备</span>
      {report && <span className={`text-xs ${report.ready ? "text-ems-mint" : "text-ems-amber"}`}>
        {report.ready ? "检查通过" : `${report.blocking} 项问题阻止确认发布`}
      </span>}
    </div>

    {error && <InlineNotice tone="danger">{error}</InlineNotice>}
    {notice && <InlineNotice tone="info">{notice}</InlineNotice>}

    {session ? <iframe
      ref={frame}
      title="隔离预览"
      src={session.frame_url}
      sandbox="allow-scripts"
      referrerPolicy="no-referrer"
      onLoad={onFrameLoad}
      style={{ width: "100%", height: 380, border: "1px solid var(--color-ems-line)", borderRadius: 8, background: "#fff" }}
    /> : <div className="panel empty-state">正在创建隔离预览…</div>}

    <div className="flex flex-wrap items-center gap-2">
      <EmsButton variant="secondary" disabled={!session} onClick={() => post({ type: "fastiot:check" })}>重新检查</EmsButton>
      <EmsButton disabled={!session || !report?.ready || !checksRan || confirming} busy={confirming} busyLabel="确认中…" onClick={() => void confirm()}>确认发布</EmsButton>
      {!checksRan && <span className="text-xs text-ems-muted">需要先在预览中运行检查，才能确认发布</span>}
    </div>

    {report && report.findings.length > 0 && <section className="rounded-lg border border-ems-line p-3 text-sm">
      <div className="flex items-center justify-between gap-2">
        <strong>检查结果</strong>
        <span className="text-xs text-ems-muted">{report.blocking} 项阻断 · {report.warnings} 项提示</span>
      </div>
      <ul className="m-0 mt-2 flex list-none flex-col gap-2 p-0">
        {report.findings.map((finding, index) => <li key={`${finding.code}-${index}`} className="rounded border border-ems-line p-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <strong className={finding.severity === "blocking" ? "text-ems-danger" : "text-ems-muted"}>
              {codeText[finding.code] || finding.code}
            </strong>
            {finding.element && <button type="button" className="text-xs" onClick={() => post({ type: "fastiot:locate", element: finding.element })}>在画面中定位</button>}
          </div>
          <div className="mt-1 text-xs text-ems-muted">{finding.message}</div>
          <div className="mt-1 text-xs text-ems-muted">{findingLocation(finding)}</div>
        </li>)}
      </ul>
    </section>}

    {writes.length > 0 && <section className="rounded-lg border border-ems-line p-3 text-sm">
      <strong>模拟写入记录</strong>
      <ul className="m-0 mt-2 flex list-none flex-col gap-1 p-0 text-xs">
        {writes.map((write, index) => <li key={index}>
          {write.point} ← {write.value} · {write.status === "simulated" ? "已写入模拟设备" : `已拒绝${write.reason ? `（${write.reason}）` : ""}`}
        </li>)}
      </ul>
    </section>}

    {session && <ScreenRevision
      token={token}
      sessionID={session.id}
      revision={revision}
      onApplied={applyRevision}
      onLocate={(element) => post({ type: "fastiot:locate", element })}
    />}
  </div>
}
