import { useCallback, useEffect, useRef, useState } from "react"
import { IonCardContent } from "@ionic/react"
import { ApiError, CalculationDefinition, CalculationList, CalculationPoint, CalculationResult, CalculationSample, Node, nameOf, request } from "./api"
import { EmsButton, EmsCard, EmsOverlay, InlineNotice, StatusBadge } from "./components/ems"
import { isObservationExpired } from "./observations"

type Impact = { confirmation: string; references: { id: string; name: string; reason: string }[]; restricted: boolean; coverage: string }
type Pending = { point: CalculationPoint; payload: { revision: number; operation_id: string }; impact: Impact }
const initial: CalculationDefinition = { operator: "add", inputs: ["", ""], kind: "number", unit: "kW" }
const sources: Record<string, string> = { observation: "现场观测", configuration: "配置常量", manual: "人工输入", simulation: "模拟输入（非实测）", calculation: "计算点" }
const reasons: Record<string, string> = {
  output_unit: "输出单位不匹配：加法保留输入单位，同单位除法输出 1",
  division_by_zero: "分母为零", unit_mismatch: "输入或输出单位不匹配", input_type: "输入类型不匹配", cycle: "存在循环引用",
  missing_reference: "引用未绑定或不存在", expired: "原始输入已过期", time_skew: "原始观测时间差超限", non_finite: "计算结果不是有限数值",
  restricted_dependency: "部分依赖不在当前权限范围内", not_applied: "尚未应用", pending_calculation: "等待后台计算",
  revision_conflict: "配置已变化，请重新读取后操作", impact_confirmation_conflict: "下游配置已变化，请重新预览影响",
  missing_or_invalid_trial_input: "请完整填写所有固定输入", missing_trial_time: "请提供固定观测时间", invalid_trial_time: "固定时间不能为空或晚于服务端当前时间",
  configuration_changed: "采集配置已变化，等待匹配的新观测", read_failed: "采集失败", input_unavailable: "输入不可用",
}
const display = (value?: number) => value === undefined || value === null ? "—" : String(value)
const date = (value?: string) => value ? new Date(value).toLocaleString() : "—"
const explain = (reason?: string) => Object.entries(reasons).find(([key]) => reason?.includes(key))?.[1] || reason || "不可用"
const errorText = (error: unknown) => error instanceof ApiError ? explain(error.code || error.message) : (error as Error).message

export default function CalculationPoints({ token, nodes, customer = false }: { token: string; nodes: Node[]; customer?: boolean }) {
  const text = customer
    ? {
        title: "计算值",
        intro: "计算值自动更新，不依赖页面打开；只用于展示和计算，不写设备。",
        create: "新增计算值",
        loading: "正在读取计算值…",
        empty: "尚无计算值。先准备带单位的数值输入，再建立加法或除法。",
        createTitle: "新增计算值",
        nameLabel: "计算值名称",
        parentLabel: "计算值分组",
        sourceLabel: "计算值",
      }
    : {
        title: "只读计算点",
        intro: "后台持续计算，不依赖页面打开。保存草稿不生效，应用不会写设备。",
        create: "新增计算点",
        loading: "正在读取计算点…",
        empty: "尚无计算点。先准备带单位的数值输入，再建立加法或除法。",
        createTitle: "新增计算点",
        nameLabel: "计算点名称",
        parentLabel: "计算点分组",
        sourceLabel: "计算点",
      }
  const sourceLabel = (key: string) => key === "calculation" ? text.sourceLabel : sources[key] || key
  const parents = nodes.filter(n => n.type === "group" || n.parent === "root")
  const scope = parents.map(n => n.id).sort().join(",")
  const key = `${token}/${scope}`
  const currentKey = useRef(key); currentKey.current = key
  const authorization = useRef<CalculationList | undefined>(undefined)
  const authorized = (id: string, action: "edit" | "apply") => !!authorization.current?.calculations.find(p => p.id === id)?.permissions[action]
  const [snapshot, setSnapshot] = useState<{ key: string; data: CalculationList; started: number }>()
  const [loadError, setLoadError] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [refresh, setRefresh] = useState(0)
  const [, tick] = useState(0)
  const [editing, setEditing] = useState<CalculationPoint | null | undefined>()
  const [name, setName] = useState("")
  const [parent, setParent] = useState("")
  const [definition, setDefinition] = useState<CalculationDefinition>(initial)
  const operation = useRef("")
  const [pending, setPending] = useState<Pending>()
  const [trial, setTrial] = useState<CalculationPoint>()
  const [trialValues, setTrialValues] = useState<Record<string, string>>({})
  const [trialTimes, setTrialTimes] = useState<Record<string, string>>({})
  const [trialResults, setTrialResults] = useState<Record<string, CalculationResult>>()
  const [percent, setPercent] = useState<Record<string, boolean>>({})
  const closeEditor = useCallback(() => setEditing(undefined), [])
  const closeConfirmation = useCallback(() => setPending(undefined), [])
  const closeTrial = useCallback(() => setTrial(undefined), [])
  const ready = snapshot?.key === key
  const data = ready ? snapshot.data : undefined
  const visible = new Set(nodes.map(n => n.id))
  const points = data?.calculations.filter(p => visible.has(p.id)) || []
  const sourceList = data?.sources || []
  const label = (id: string) => sourceList.find(s => s.id === id)?.name || points.find(p => p.id === id)?.name || id || "未绑定"
  const formula = (d?: CalculationDefinition) => d ? `${label(d.inputs[0])} ${d.operator === "add" ? "+" : "÷"} ${label(d.inputs[1])}` : "—"
  const expired = (r: CalculationResult) => isObservationExpired(r.valid_until, snapshot?.data.evaluated_at, snapshot ? performance.now() - snapshot.started : 0)

  useEffect(() => {
    let stopped = false, inFlight = false
    const controller = new AbortController()
    authorization.current = undefined
    setSnapshot(undefined); setLoadError(""); setError(""); setEditing(undefined); setPending(undefined); setTrial(undefined); setBusy(false)
    async function load() {
      if (inFlight) return
      inFlight = true
      const started = performance.now()
      try {
        const result = await request<CalculationList>(token, "/calculations", "GET", undefined, AbortSignal.any([controller.signal, AbortSignal.timeout(5000)]))
        if (!stopped) {
          authorization.current = result
          setSnapshot({ key, data: result, started }); setLoadError("")
          setEditing(old => old === null ? result.can_create ? old : undefined : old && authorized(old.id, "edit") ? old : undefined)
          setPending(old => old && authorized(old.point.id, "apply") ? old : undefined)
          setTrial(old => old && authorized(old.id, "edit") ? old : undefined)
        }
      } catch (e) {
        if (!stopped) {
          authorization.current = undefined
          setSnapshot(undefined); setLoadError(errorText(e)); setEditing(undefined); setPending(undefined); setTrial(undefined); setTrialResults(undefined); setTrialValues({}); setTrialTimes({})
        }
      } finally { inFlight = false }
    }
    void load()
    const poll = window.setInterval(load, 2000), expiry = window.setInterval(() => tick(n => n + 1), 1000)
    return () => { stopped = true; controller.abort(); window.clearInterval(poll); window.clearInterval(expiry) }
  }, [token, scope, key, refresh])

  async function run(action: () => Promise<void>) {
    setBusy(true); setError("")
    try { await action() } catch (e) { if (currentKey.current === key) setError(errorText(e)) }
    finally { if (currentKey.current === key) setBusy(false) }
  }
  function edit(point?: CalculationPoint) {
    operation.current = crypto.randomUUID(); setError(""); setEditing(point || null)
    setName(point?.name || ""); setParent(point?.parent || parents[0]?.id || "")
    setDefinition(point?.draft || initial)
  }
  async function save() {
    if (!name.trim() || !definition.unit.trim()) throw new Error("请填写名称和单位")
    const payload = editing ? { revision: editing.revision, operation_id: operation.current, definition }
      : { parent, name, definition, operation_id: operation.current }
    await request(token, editing ? `/calculations/${editing.id}/draft` : "/calculations", "POST", payload)
    if (currentKey.current !== key) return
    setEditing(undefined); setRefresh(n => n + 1)
  }
  async function preview(point: CalculationPoint) {
    const payload = { revision: point.revision, operation_id: crypto.randomUUID() }
    const impact = await request<Impact>(token, `/calculations/${point.id}/impact`, "POST", payload)
    if (currentKey.current === key && authorized(point.id, "apply")) setPending({ point, payload, impact })
  }
  async function apply() {
    if (!pending) return
    await request(token, `/calculations/${pending.point.id}/apply`, "POST", { ...pending.payload, confirmation: pending.impact.confirmation })
    if (currentKey.current !== key) return
    setPending(undefined); setRefresh(n => n + 1)
  }
  function leaves(point: CalculationPoint) {
    const visited = new Set<string>(), inputs = new Set<string>()
    const visit = (id: string, d?: CalculationDefinition) => {
      if (visited.has(id)) return
      visited.add(id)
      for (const ref of d?.inputs || []) {
        const upstream = data?.calculations.find(p => p.id === ref)
        if (upstream?.applied) visit(ref, upstream.applied)
        else if (ref) inputs.add(ref)
      }
    }
    visit(point.id, point.draft)
    return [...inputs].sort()
  }
  function openTrial(point: CalculationPoint) {
    setError(""); setTrial(point); setTrialValues({}); setTrialResults(undefined)
    setTrialTimes(Object.fromEntries(leaves(point).map(id => [id, data?.evaluated_at || ""])))
  }
  async function evaluateTrial() {
    if (!trial) return
    const values: Record<string, number> = {}, times: Record<string, string> = {}
    for (const id of leaves(trial)) {
      if (!trialValues[id]?.trim() || !Number.isFinite(Number(trialValues[id]))) throw new Error(`请填写 ${label(id)} 的有限数值`)
      values[id] = Number(trialValues[id])
      if (sourceList.find(s => s.id === id)?.source === "observation" && trialTimes[id]) {
        const at = new Date(trialTimes[id]); if (!Number.isFinite(at.getTime())) throw new Error(`请填写 ${label(id)} 的有效固定时间`)
        times[id] = at.toISOString()
      }
    }
    const result = await request<{ results: Record<string, CalculationResult> }>(token, `/calculations/${trial.id}/trial`, "POST", { revision: trial.revision, values, times })
    if (currentKey.current === key && authorized(trial.id, "edit")) setTrialResults(result.results)
  }
  function evidence(inputs?: CalculationSample[]) {
    return <ul className="grid gap-3">{inputs?.map((s, index) => <li key={`${s.id}/${index}`} className="break-words">
      <strong>{label(s.id)}</strong>：{display(s.value)} {s.unit}；{s.quality === "good" ? "可用" : explain(s.reason)}
      <p>{sourceLabel(s.source)}{s.simulated ? "；模拟试算 / 非实测" : ""}；原始观测 {date(s.source_time)}；接收 {date(s.received_time)}；变更 {date(s.changed_at)}；原到期 {date(s.valid_until)}</p>
      <small className="break-all">ID {s.id}；输入版本 {s.version}；时间依据 {s.time_source || "非现场采样"}</small>
    </li>)}</ul>
  }
  const modalError = error || (!ready ? loadError || "正在重新确认数据，暂不能提交" : "")
  return <EmsCard className="calculation-points"><IonCardContent>
    <div className="flex flex-wrap items-center justify-between gap-3"><h2>{text.title}</h2>{data?.can_create && <EmsButton disabled={busy || !parents.length} onClick={() => edit()}>{text.create}</EmsButton>}</div>
    <p>{text.intro}</p>
    {loadError && <InlineNotice tone="danger">读取失败，当前计算值不可确认：{loadError}<EmsButton onClick={() => setRefresh(n => n + 1)}>重新读取计算</EmsButton></InlineNotice>}
    {error && <p role="alert" className="inline-error">{error}</p>}
    {!ready && !loadError && <p>{text.loading}</p>}
    {ready && !points.length && <p>{text.empty}</p>}
    <div className="grid gap-3">{points.map(point => {
      const r = point.state, stale = expired(r), good = r.quality === "good" && !stale
      const previous = stale && r.value !== undefined ? r : r.retained
      return <article key={point.id} data-calculation-id={point.id} className="rounded-xl border border-ems-line p-4 min-w-0">
        <div className="flex flex-wrap items-center justify-between gap-2"><h3>{point.name}</h3><StatusBadge tone={good ? "success" : "warning"}>{good ? "可用" : stale ? "原始输入已过期" : explain(r.reason)}</StatusBadge></div>
        <p>当前可用值：<strong data-calculation-value>{good ? percent[point.id] ? `${Number((r.value! * 100).toPrecision(12))}%` : display(r.value) : "—"}</strong> {good && !percent[point.id] ? r.unit : ""}{r.simulated && "（含模拟来源，非实测）"}</p>
        <p>生效公式：{formula(point.applied)}；单位 {point.applied?.unit || "—"}；公式 v{point.version}{point.draft && JSON.stringify(point.draft) !== JSON.stringify(point.applied) ? "；有未应用草稿" : ""}</p>
        {point.applied?.unit === "1" && <label className="!flex items-center gap-2"><input type="checkbox" className="!h-4 !w-4 !min-h-0" checked={!!percent[point.id]} onChange={e => setPercent({ ...percent, [point.id]: e.target.checked })} />百分比显示（原始值与下游输入不变）</label>}
        <p>计算时间 {date(r.calculated_at)}；原到期 {date(r.valid_until)}</p>
        {previous && <p>历史保留值（非当前）：{display(previous.value)} {previous.unit}；公式 v{previous.version}；原计算时间 {date(previous.calculated_at)}</p>}
        <details><summary>公式与输入依据</summary><p className="break-all">稳定 ID {point.id}；结果批次 {r.batch_id || "—"}；读取批次 {data?.batch_id || "—"}</p>
          {point.draft && <p>草稿：{formula(point.draft)}；单位 {point.draft.unit}；修订 {point.revision}</p>}
          <p>最大现场观测时间差：{point.applied?.max_skew_ms === undefined ? "未声明，不额外判断同步性" : `${point.applied.max_skew_ms} 毫秒`}</p>
          <h4>直接输入快照</h4>{evidence(r.inputs)}<h4>原始叶子依据</h4>{evidence(r.leaves)}
          {previous && <details><summary>历史保留值的原始依据</summary><p>{formula(previous.definition)}</p>{evidence(previous.leaves)}</details>}
        </details>
        <div className="flex flex-wrap gap-2 mt-3">
          {point.permissions.edit && <><EmsButton variant="secondary" disabled={busy} onClick={() => edit(point)}>编辑计算草稿</EmsButton><EmsButton variant="secondary" disabled={busy} onClick={() => openTrial(point)}>固定输入试算</EmsButton></>}
          {point.permissions.apply && point.draft && <EmsButton disabled={busy} onClick={() => void run(() => preview(point))}>查看下游并预览应用</EmsButton>}
        </div>
      </article>
    })}</div>
    <EmsOverlay open={editing !== undefined} title={editing ? "编辑计算草稿" : text.createTitle} onClose={closeEditor} busy={busy}
      actions={<><EmsButton variant="secondary" disabled={busy} onClick={closeEditor}>取消</EmsButton><EmsButton disabled={busy || !ready} onClick={() => void run(save)}>保存计算草稿（不生效）</EmsButton></>}>
      <div className="grid gap-3">
        <label>名称<input aria-label={text.nameLabel} value={name} disabled={!!editing} onChange={e => setName(e.target.value)} /></label>
        <label>所属分组<select aria-label={text.parentLabel} value={parent} disabled={!!editing} onChange={e => setParent(e.target.value)}>{parents.map(n => <option key={n.id} value={n.id}>{nameOf(n)}</option>)}</select></label>
        <label>运算<select aria-label="计算运算" value={definition.operator} onChange={e => { const operator = e.target.value as CalculationDefinition["operator"]; setDefinition({ ...definition, operator, unit: operator === "divide" ? "1" : sourceList.find(s => s.id === definition.inputs[0])?.unit || "kW" }) }}><option value="add">加法（相同单位）</option><option value="divide">除法（同单位，输出无量纲）</option></select></label>
        {[0, 1].map(index => <label key={index}>输入 {index + 1}<select aria-label={`计算输入${index + 1}`} value={definition.inputs[index]} onChange={e => setDefinition({ ...definition, inputs: definition.inputs.map((id, i) => i === index ? e.target.value : id) })}>
          <option value="">请选择 / 待重绑定</option>{sourceList.filter(s => s.kind === "number" && s.id !== editing?.id && visible.has(s.id)).map(s => <option key={s.id} value={s.id}>{s.name} [{s.unit || "未声明单位"}] — {sourceLabel(s.source)}</option>)}
        </select></label>)}
        <label>输出单位<input aria-label="计算输出单位" value={definition.unit} onChange={e => setDefinition({ ...definition, unit: e.target.value })} /></label>
        <label>最大现场观测时间差（毫秒，可留空）<input aria-label="计算时间差" type="number" min="0" step="1" value={definition.max_skew_ms ?? ""} onChange={e => setDefinition({ ...definition, max_skew_ms: e.target.value === "" ? undefined : Number(e.target.value) })} /></label>
        <p>不自动换算单位；除法原值为比例，例如 0.5。草稿可保存待排错，应用前会校验完整受影响链。</p>
        {modalError && <p role="alert" className="inline-error">{modalError}</p>}
      </div>
    </EmsOverlay>
    <EmsOverlay open={!!pending} title="确认计算定义应用" onClose={closeConfirmation} busy={busy}
      actions={<><EmsButton variant="secondary" disabled={busy} onClick={closeConfirmation}>取消</EmsButton><EmsButton disabled={busy || !ready} onClick={() => void run(apply)}>确认应用计算（不写设备）</EmsButton></>}>
      {pending && <><p>{pending.point.name}：{formula(pending.point.applied)} → {formula(pending.point.draft)}</p><h3>已知下游影响</h3>
        {pending.impact.references.length ? <ul>{pending.impact.references.map((ref, i) => <li key={i}>{ref.name}：{ref.reason}</li>)}</ul> : <p>未发现当前账号可见的已知引用。</p>}
        {pending.impact.restricted && <p>另有权限范围外引用，名称与内容不披露。</p>}<p>{pending.impact.coverage}</p><p>应用后相关结果先失效，再由后台重新计算。</p></>}
      {modalError && <p role="alert" className="inline-error">{modalError}</p>}
    </EmsOverlay>
    <EmsOverlay open={!!trial} title="固定输入试算（模拟，非实测）" onClose={closeTrial} busy={busy}
      actions={<><EmsButton variant="secondary" disabled={busy} onClick={closeTrial}>关闭</EmsButton><EmsButton disabled={busy || !ready} onClick={() => void run(evaluateTrial)}>运行固定试算</EmsButton></>}>
      <p>请完整提供叶子输入；不会补读现场值，不修改生效公式或运行结果。上游使用生效公式，本点使用已保存草稿。</p>
      <div className="grid gap-3">{trial && leaves(trial).map(id => <div key={id}>
        <label>{label(id)} [{sourceList.find(s => s.id === id)?.unit || "—"}]<input aria-label={`试算值 ${label(id)}`} type="number" step="any" value={trialValues[id] || ""} onChange={e => { setTrialValues({ ...trialValues, [id]: e.target.value }); setTrialResults(undefined) }} /></label>
        {sourceList.find(s => s.id === id)?.source === "observation" && <label>假定观测时间（ISO，非现场时间）<input aria-label={`试算时间 ${label(id)}`} value={trialTimes[id] || ""} onChange={e => { setTrialTimes({ ...trialTimes, [id]: e.target.value }); setTrialResults(undefined) }} /></label>}
      </div>)}</div>
      {trialResults && <div role="status">{Object.values(trialResults).map(r => <section key={r.id} className="mt-3"><h3>{label(r.id)}：{display(r.value)} {r.unit}（模拟）</h3><p>{formula(r.definition)}；{r.quality === "good" ? "试算可用" : explain(r.reason)}</p>{evidence(r.inputs)}</section>)}</div>}
      {modalError && <p role="alert" className="inline-error">{modalError}</p>}
    </EmsOverlay>
  </IonCardContent></EmsCard>
}
